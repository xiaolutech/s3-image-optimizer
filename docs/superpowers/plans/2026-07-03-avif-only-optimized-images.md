# AVIF-only Optimized Images Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Generate trusted AVIF-only optimized image objects in `s3-image-optimizer` and serve them from `s3-static` only when requests advertise `image/avif`.

**Architecture:** `s3-image-optimizer` owns all encoding and writes one hidden AVIF object per source object using a deterministic hash key plus source/profile metadata. `s3-static` stays a thin gateway by delegating AVIF lookup and trust checks to a resolver module, then falling back to the source object for every miss, stale object, unsupported request, or optimized-path error.

**Tech Stack:** Go 1.25, `github.com/gen2brain/avif` for CGo-free AVIF encoding, S3-compatible object metadata, existing `interfaces.Storage` / `worker.Store` adapters, table-driven Go tests.

---

## Scope Check

This plan touches two repos:

- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer`
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static`

The work is coupled by the optimized object key and metadata contract, but it can be implemented in two separately testable phases:

1. `s3-image-optimizer` writes AVIF objects at deterministic hidden keys.
2. `s3-static` resolves those same keys when `Accept: image/avif` is present.

Do not reintroduce access-time triggers. Do not add WebP, JPEG, PNG optimized variants, or manifest files.

## File Structure

### s3-image-optimizer

- Modify `go.mod` / `go.sum`: add `github.com/gen2brain/avif`.
- Modify `internal/config/config.go`: add AVIF config fields and validation.
- Modify `internal/config/config_test.go`: cover AVIF defaults, env loading, and validation.
- Modify `internal/imageopt/optimizer.go`: add AVIF output mode, target-size quality search, and `image/avif` result support.
- Modify `internal/imageopt/optimizer_test.go`: cover AVIF encode path, dimensions, unsupported decode, and quality selection with an injected encoder.
- Modify `internal/worker/worker.go`: use `avifOptimizedKey(source.Key, profile)`, trust/write AVIF metadata, and keep skip markers.
- Modify `internal/worker/worker_test.go`: cover AVIF key, AVIF metadata, stale/profile rewrite, and current AVIF skip.
- Modify `README.md`, `compose.yaml`: document and expose AVIF settings.

### s3-static

- Do not modify `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/config/config.go` for a new format flag; reuse `OPTIMIZED_IMAGE_ENABLED`, `OPTIMIZED_BUCKET_NAME`, `OPTIMIZATION_PROFILE`, and `OPTIMIZED_MIN_BYTES`.
- Create `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimized_variant.go`: AVIF key construction, `Accept` parsing, metadata trust, and resolver statuses.
- Create `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimized_variant_test.go`: focused resolver tests.
- Modify `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/handler.go`: replace same-key `openTrustedOptimized` call with resolver call.
- Modify `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/handler_test.go`: update optimized hit/fallback tests to AVIF key and `Accept` behavior.
- Modify `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/README.md`: document AVIF-only optimized object contract.

---

## Task 1: Add AVIF Config to s3-image-optimizer

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/config/config.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/config/config_test.go`

- [ ] **Step 1: Write failing config tests**

Add assertions to `TestDefaultConfig`:

```go
if cfg.AVIFEnabled {
	t.Fatal("expected AVIFEnabled false by default")
}
if cfg.AVIFTargetBytes != 1024*1024 {
	t.Fatalf("expected AVIF target bytes 1048576, got %d", cfg.AVIFTargetBytes)
}
if cfg.AVIFQualityMin != 35 {
	t.Fatalf("expected AVIF min quality 35, got %d", cfg.AVIFQualityMin)
}
if cfg.AVIFQualityMax != 75 {
	t.Fatalf("expected AVIF max quality 75, got %d", cfg.AVIFQualityMax)
}
if cfg.AVIFSpeed != 6 {
	t.Fatalf("expected AVIF speed 6, got %d", cfg.AVIFSpeed)
}
```

Add env loading inside `TestLoadFromEnv`:

```go
t.Setenv("AVIF_ENABLED", "true")
t.Setenv("AVIF_TARGET_BYTES", "786432")
t.Setenv("AVIF_QUALITY_MIN", "30")
t.Setenv("AVIF_QUALITY_MAX", "70")
t.Setenv("AVIF_SPEED", "8")
```

Then assert:

```go
if !cfg.AVIFEnabled {
	t.Fatal("expected AVIF enabled")
}
if cfg.AVIFTargetBytes != 786432 {
	t.Fatalf("expected AVIF target bytes 786432, got %d", cfg.AVIFTargetBytes)
}
if cfg.AVIFQualityMin != 30 || cfg.AVIFQualityMax != 70 {
	t.Fatalf("unexpected AVIF quality range: min=%d max=%d", cfg.AVIFQualityMin, cfg.AVIFQualityMax)
}
if cfg.AVIFSpeed != 8 {
	t.Fatalf("expected AVIF speed 8, got %d", cfg.AVIFSpeed)
}
```

Add validation cases to `TestValidateRequiresCoreFields`:

```go
{
	name:      "negative AVIF target bytes",
	mutate:    func(cfg *Config) { cfg.AVIFTargetBytes = -1 },
	wantError: "AVIF_TARGET_BYTES",
},
{
	name:      "invalid AVIF min quality low",
	mutate:    func(cfg *Config) { cfg.AVIFQualityMin = -1 },
	wantError: "AVIF_QUALITY_MIN",
},
{
	name:      "invalid AVIF max quality high",
	mutate:    func(cfg *Config) { cfg.AVIFQualityMax = 101 },
	wantError: "AVIF_QUALITY_MAX",
},
{
	name:      "AVIF min quality exceeds max",
	mutate:    func(cfg *Config) { cfg.AVIFQualityMin = 80; cfg.AVIFQualityMax = 70 },
	wantError: "AVIF_QUALITY_MIN",
},
{
	name:      "invalid AVIF speed low",
	mutate:    func(cfg *Config) { cfg.AVIFSpeed = -1 },
	wantError: "AVIF_SPEED",
},
{
	name:      "invalid AVIF speed high",
	mutate:    func(cfg *Config) { cfg.AVIFSpeed = 11 },
	wantError: "AVIF_SPEED",
},
```

- [ ] **Step 2: Run config tests to verify failure**

Run:

```bash
go test ./internal/config
```

Expected: FAIL because `Config` has no AVIF fields yet.

- [ ] **Step 3: Implement AVIF config**

In `Config`, add:

```go
AVIFEnabled     bool
AVIFTargetBytes int64
AVIFQualityMin  int
AVIFQualityMax  int
AVIFSpeed       int
```

In `DefaultConfig`, add:

```go
AVIFEnabled:     false,
AVIFTargetBytes: 1024 * 1024,
AVIFQualityMin:  35,
AVIFQualityMax:  75,
AVIFSpeed:       6,
```

In `Load`, add:

```go
if cfg.AVIFEnabled, err = getenvBool("AVIF_ENABLED", cfg.AVIFEnabled); err != nil {
	return nil, err
}
if cfg.AVIFTargetBytes, err = getenvInt64("AVIF_TARGET_BYTES", cfg.AVIFTargetBytes); err != nil {
	return nil, err
}
if cfg.AVIFQualityMin, err = getenvInt("AVIF_QUALITY_MIN", cfg.AVIFQualityMin); err != nil {
	return nil, err
}
if cfg.AVIFQualityMax, err = getenvInt("AVIF_QUALITY_MAX", cfg.AVIFQualityMax); err != nil {
	return nil, err
}
if cfg.AVIFSpeed, err = getenvInt("AVIF_SPEED", cfg.AVIFSpeed); err != nil {
	return nil, err
}
```

In `Validate`, add:

```go
if c.AVIFTargetBytes < 0 {
	return fmt.Errorf("AVIF_TARGET_BYTES cannot be negative")
}
if c.AVIFQualityMin < 0 || c.AVIFQualityMin > 100 {
	return fmt.Errorf("AVIF_QUALITY_MIN must be between 0 and 100")
}
if c.AVIFQualityMax < 0 || c.AVIFQualityMax > 100 {
	return fmt.Errorf("AVIF_QUALITY_MAX must be between 0 and 100")
}
if c.AVIFQualityMin > c.AVIFQualityMax {
	return fmt.Errorf("AVIF_QUALITY_MIN cannot exceed AVIF_QUALITY_MAX")
}
if c.AVIFSpeed < 0 || c.AVIFSpeed > 10 {
	return fmt.Errorf("AVIF_SPEED must be between 0 and 10")
}
```

- [ ] **Step 4: Run config tests**

Run:

```bash
go test ./internal/config
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add avif optimizer config"
```

---

## Task 2: Add AVIF Encoding Module

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/go.mod`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/go.sum`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/imageopt/optimizer.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/imageopt/optimizer_test.go`

- [ ] **Step 1: Add failing AVIF tests**

Add tests to `internal/imageopt/optimizer_test.go`:

```go
func TestOptimizeWithAVIFOutputPreservesDimensions(t *testing.T) {
	input := encodePNG(t, gradientImage(640, 480))

	result, err := Optimize(input, "image/png", Options{
		MaxWidth:        0,
		JPEGQuality:     82,
		MinSavings:      0,
		AVIFEnabled:     true,
		AVIFTargetBytes: 1024 * 1024,
		AVIFQualityMin:  35,
		AVIFQualityMax:  75,
		AVIFSpeed:       8,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}
	if result.Skipped {
		t.Fatalf("expected AVIF output, skipped with %s", result.Reason)
	}
	if result.ContentType != "image/avif" {
		t.Fatalf("expected image/avif, got %q", result.ContentType)
	}
	if result.Width != 640 || result.Height != 480 {
		t.Fatalf("unexpected dimensions %dx%d", result.Width, result.Height)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(result.Body))
	if err != nil {
		t.Fatalf("decode optimized avif: %v", err)
	}
	if format != "avif" {
		t.Fatalf("expected avif format, got %s", format)
	}
	if cfg.Width != 640 || cfg.Height != 480 {
		t.Fatalf("unexpected decoded dimensions %dx%d", cfg.Width, cfg.Height)
	}
}

func TestOptimizeAVIFSkipsInsufficientSavings(t *testing.T) {
	input := encodePNG(t, gradientImage(16, 16))

	result, err := Optimize(input, "image/png", Options{
		MaxWidth:        0,
		JPEGQuality:     82,
		MinSavings:      0.99,
		AVIFEnabled:     true,
		AVIFTargetBytes: 1024,
		AVIFQualityMin:  35,
		AVIFQualityMax:  75,
		AVIFSpeed:       8,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}
	if !result.Skipped {
		t.Fatal("expected AVIF output to be skipped")
	}
	if result.Reason != "insufficient_savings" {
		t.Fatalf("expected insufficient_savings, got %q", result.Reason)
	}
}
```

- [ ] **Step 2: Run imageopt tests to verify failure**

Run:

```bash
go test ./internal/imageopt -run 'TestOptimize.*AVIF' -count=1
```

Expected: FAIL because `Options` has no AVIF fields and no AVIF encoder exists.

- [ ] **Step 3: Add AVIF dependency**

Run:

```bash
go get github.com/gen2brain/avif@latest
```

This dependency is chosen because it can encode AVIF through libavif/aom compiled to WASM with wazero and does not require CGo in the current Dockerfile.

- [ ] **Step 4: Implement AVIF options and encoding**

In `optimizer.go`, add the import:

```go
import avif "github.com/gen2brain/avif"
```

Add constants:

```go
const (
	ContentTypeJPEG = "image/jpeg"
	ContentTypePNG  = "image/png"
	ContentTypeAVIF = "image/avif"
)
```

Extend `Options`:

```go
type Options struct {
	MaxWidth        int
	JPEGQuality     int
	MinSavings      float64
	AVIFEnabled     bool
	AVIFTargetBytes int64
	AVIFQualityMin  int
	AVIFQualityMax  int
	AVIFSpeed       int
}
```

In `Optimize`, validate:

```go
if opts.AVIFTargetBytes < 0 {
	return Result{}, fmt.Errorf("avif target bytes cannot be negative")
}
if opts.AVIFQualityMin < 0 || opts.AVIFQualityMin > 100 {
	return Result{}, fmt.Errorf("avif min quality must be between 0 and 100")
}
if opts.AVIFQualityMax < 0 || opts.AVIFQualityMax > 100 {
	return Result{}, fmt.Errorf("avif max quality must be between 0 and 100")
}
if opts.AVIFQualityMin > opts.AVIFQualityMax {
	return Result{}, fmt.Errorf("avif min quality cannot exceed max quality")
}
if opts.AVIFSpeed < 0 || opts.AVIFSpeed > 10 {
	return Result{}, fmt.Errorf("avif speed must be between 0 and 10")
}
```

Replace the encode call with:

```go
outputContentType := mediaType
var encoded []byte
if opts.AVIFEnabled {
	outputContentType = ContentTypeAVIF
	encoded, err = encodeAVIF(img, opts)
} else {
	encoded, err = encode(img, mediaType, opts)
}
if err != nil {
	return Result{}, err
}
```

Use `outputContentType` in both skipped and success results.

Add:

```go
func encodeAVIF(img image.Image, opts Options) ([]byte, error) {
	minQuality := opts.AVIFQualityMin
	maxQuality := opts.AVIFQualityMax
	if minQuality == 0 && maxQuality == 0 {
		minQuality = 35
		maxQuality = 75
	}
	speed := opts.AVIFSpeed
	if speed == 0 {
		speed = 6
	}

	best := []byte(nil)
	bestQuality := -1
	low, high := minQuality, maxQuality
	for low <= high {
		quality := low + (high-low)/2
		encoded, err := encodeAVIFAtQuality(img, quality, speed)
		if err != nil {
			return nil, err
		}
		if opts.AVIFTargetBytes == 0 || int64(len(encoded)) <= opts.AVIFTargetBytes {
			best = encoded
			bestQuality = quality
			low = quality + 1
			continue
		}
		if best == nil || len(encoded) < len(best) {
			best = encoded
		}
		high = quality - 1
	}
	if len(best) == 0 || bestQuality < minQuality {
		return best, nil
	}
	return best, nil
}

func encodeAVIFAtQuality(img image.Image, quality, speed int) ([]byte, error) {
	var buf bytes.Buffer
	if err := avif.Encode(&buf, img, avif.Options{
		Quality: quality,
		Speed:   speed,
	}); err != nil {
		return nil, fmt.Errorf("encode avif: %w", err)
	}
	return buf.Bytes(), nil
}
```

- [ ] **Step 5: Register AVIF decoder in tests**

If AVIF decode config fails because no decoder is registered, add this blank import in `optimizer_test.go`:

```go
import _ "github.com/gen2brain/avif"
```

- [ ] **Step 6: Run imageopt tests**

Run:

```bash
go test ./internal/imageopt -count=1
```

Expected: PASS.

- [ ] **Step 7: Verify static Linux build stays CGo-free**

Run:

```bash
CGO_ENABLED=0 GOOS=linux go build -o /tmp/s3-image-optimizer-avif ./cmd/s3-image-optimizer
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/imageopt/optimizer.go internal/imageopt/optimizer_test.go
git commit -m "feat: encode avif optimized images"
```

---

## Task 3: Write AVIF Objects from s3-image-optimizer Worker

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/cmd/s3-image-optimizer/main.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker_test.go`

- [ ] **Step 1: Add failing worker tests for AVIF key and metadata**

Add to `worker_test.go`:

```go
func TestWorkerWritesAVIFOptimizedObjectWhenEnabled(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	source := storage.ObjectInfo{
		Key:         "notes/photo.jpg",
		Size:        int64(len(body)),
		ETag:        "source-etag",
		ContentType: "image/jpeg",
	}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: body}

	cfg := testWorkerConfig()
	cfg.AVIFEnabled = true
	cfg.AVIFTargetBytes = 1024 * 1024
	cfg.AVIFQualityMin = 35
	cfg.AVIFQualityMax = 75
	cfg.AVIFSpeed = 8
	cfg.OptimizationProfile = "v4-avif-target1m-original"

	w := New(store, cfg)
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	key := avifOptimizedKey(source.Key, cfg.OptimizationProfile)
	written := store.objects[objKey("optimized", key)]
	if len(written.body) == 0 {
		t.Fatalf("expected AVIF object at %s", key)
	}
	if written.info.ContentType != "image/avif" {
		t.Fatalf("expected image/avif, got %q", written.info.ContentType)
	}
	if written.info.Metadata["source-key"] != source.Key {
		t.Fatalf("expected source-key metadata, got %#v", written.info.Metadata)
	}
	if written.info.Metadata["source-etag"] != source.ETag {
		t.Fatalf("expected source-etag metadata, got %#v", written.info.Metadata)
	}
	if written.info.Metadata["optimization-profile"] != cfg.OptimizationProfile {
		t.Fatalf("expected profile metadata, got %#v", written.info.Metadata)
	}
	if written.info.Metadata["source-content-type"] != "image/jpeg" {
		t.Fatalf("expected source-content-type metadata, got %#v", written.info.Metadata)
	}
	if written.info.Metadata["variant-format"] != "avif" {
		t.Fatalf("expected variant-format metadata, got %#v", written.info.Metadata)
	}
}
```

Add current-object test:

```go
func TestWorkerSkipsCurrentAVIFOptimizedObject(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{Key: "notes/photo.jpg", Size: int64(len(largeJPEG(t))), ETag: "source-etag", ContentType: "image/jpeg"}
	cfg := testWorkerConfig()
	cfg.AVIFEnabled = true
	cfg.OptimizationProfile = "v4-avif-target1m-original"
	key := avifOptimizedKey(source.Key, cfg.OptimizationProfile)
	store.objects[objKey("optimized", key)] = fakeObject{info: storage.ObjectInfo{
		Key:         key,
		Size:        100,
		ETag:        "optimized-etag",
		ContentType: "image/avif",
		Metadata: map[string]string{
			"source-key":            source.Key,
			"source-etag":           source.ETag,
			"optimization-profile":  cfg.OptimizationProfile,
			"source-content-type":   "image/jpeg",
			"variant-format":        "avif",
		},
	}}

	w := New(store, cfg)
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}
	if store.getCalls != 0 {
		t.Fatalf("expected no source get, got %d", store.getCalls)
	}
}
```

- [ ] **Step 2: Run worker tests to verify failure**

Run:

```bash
go test ./internal/worker -run 'TestWorker.*AVIF' -count=1
```

Expected: FAIL because worker config and AVIF key helpers do not exist.

- [ ] **Step 3: Extend worker config**

Add fields to `worker.Config`:

```go
AVIFEnabled     bool
AVIFTargetBytes int64
AVIFQualityMin  int
AVIFQualityMax  int
AVIFSpeed       int
```

In `cmd/s3-image-optimizer/main.go`, pass config values:

```go
AVIFEnabled:     cfg.AVIFEnabled,
AVIFTargetBytes: cfg.AVIFTargetBytes,
AVIFQualityMin:  cfg.AVIFQualityMin,
AVIFQualityMax:  cfg.AVIFQualityMax,
AVIFSpeed:       cfg.AVIFSpeed,
```

- [ ] **Step 4: Add AVIF key and metadata helpers**

In `worker.go`, add constants:

```go
sourceKeyMetadata         = "source-key"
sourceContentTypeMetadata = "source-content-type"
variantFormatMetadata     = "variant-format"
avifVariantFormat         = "avif"
```

Add:

```go
func avifOptimizedKey(sourceKey, profile string) string {
	sum := sha256.Sum256([]byte(sourceKey))
	return ".s3-image-optimizer/avif/" + hex.EncodeToString(sum[:]) + "/" + profile + "/image.avif"
}
```

Update current-object lookup:

```go
optimizedKey := source.Key
if w.cfg.AVIFEnabled {
	optimizedKey = avifOptimizedKey(source.Key, w.cfg.OptimizationProfile)
}
optimized, err := w.store.HeadObject(headCtx, w.cfg.OptimizedBucket, optimizedKey)
```

Replace `isCurrentOptimized` call with:

```go
if err == nil && w.isCurrentOptimizedForSource(optimized, source) {
	log.Printf("skip current optimized object key=%s optimized_key=%s", source.Key, optimizedKey)
	return false, nil
}
```

Add:

```go
func (w *Worker) isCurrentOptimizedForSource(optimized *storage.ObjectInfo, source storage.ObjectInfo) bool {
	if optimized == nil {
		return false
	}
	if optimized.Metadata[sourceETagMetadata] != source.ETag ||
		optimized.Metadata[profileMetadata] != w.cfg.OptimizationProfile {
		return false
	}
	if !w.cfg.AVIFEnabled {
		return true
	}
	return optimized.Metadata[sourceKeyMetadata] == source.Key &&
		optimized.Metadata[variantFormatMetadata] == avifVariantFormat
}
```

- [ ] **Step 5: Pass AVIF options to image optimizer**

Update the `imageopt.Optimize` call:

```go
result, err := imageopt.Optimize(body, source.ContentType, imageopt.Options{
	MaxWidth:        w.cfg.MaxWidth,
	JPEGQuality:     w.cfg.JPEGQuality,
	MinSavings:      0.05,
	AVIFEnabled:     w.cfg.AVIFEnabled,
	AVIFTargetBytes: w.cfg.AVIFTargetBytes,
	AVIFQualityMin:  w.cfg.AVIFQualityMin,
	AVIFQualityMax:  w.cfg.AVIFQualityMax,
	AVIFSpeed:       w.cfg.AVIFSpeed,
})
```

- [ ] **Step 6: Write AVIF object to hidden key with full metadata**

Before `PutObject`, compute metadata:

```go
metadata := map[string]string{
	sourceETagMetadata: source.ETag,
	profileMetadata:    w.cfg.OptimizationProfile,
}
putKey := source.Key
if w.cfg.AVIFEnabled {
	putKey = avifOptimizedKey(source.Key, w.cfg.OptimizationProfile)
	metadata[sourceKeyMetadata] = source.Key
	metadata[sourceContentTypeMetadata] = source.ContentType
	metadata[variantFormatMetadata] = avifVariantFormat
}
```

Use `putKey` in `PutObject` and the error/log messages.

- [ ] **Step 7: Run worker tests**

Run:

```bash
go test ./internal/worker -count=1
```

Expected: PASS.

- [ ] **Step 8: Run full optimizer repo gate**

Run:

```bash
just validate
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add cmd/s3-image-optimizer/main.go internal/worker/worker.go internal/worker/worker_test.go
git commit -m "feat: write trusted avif optimized objects"
```

---

## Task 4: Document and Configure AVIF in s3-image-optimizer

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/README.md`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/compose.yaml`

- [ ] **Step 1: Update README contract**

In `README.md`, replace the same-key-only contract text with AVIF-specific behavior:

````markdown
When `AVIF_ENABLED=true`, the worker writes AVIF objects to hidden keys:

```text
.s3-image-optimizer/avif/<sha256-source-key>/<optimization-profile>/image.avif
```

Each AVIF object includes:

- `x-amz-meta-source-key`
- `x-amz-meta-source-etag`
- `x-amz-meta-optimization-profile`
- `x-amz-meta-source-content-type`
- `x-amz-meta-variant-format: avif`
````

Add config docs:

```markdown
- `AVIF_ENABLED` - Encode supported source images to hidden AVIF optimized objects. Default: `false`.
- `AVIF_TARGET_BYTES` - Target AVIF output size. Set `0` to disable target-size search. Default: `1048576`.
- `AVIF_QUALITY_MIN` - Lowest AVIF quality considered during search. Default: `35`.
- `AVIF_QUALITY_MAX` - Highest AVIF quality considered during search. Default: `75`.
- `AVIF_SPEED` - AVIF encoder speed, 0 through 10; higher is faster with lower compression efficiency. Default: `6`.
```

- [ ] **Step 2: Update compose defaults without enabling AVIF**

In `compose.yaml`, add disabled-by-default AVIF env values:

```yaml
      AVIF_ENABLED: "false"
      AVIF_TARGET_BYTES: "1048576"
      AVIF_QUALITY_MIN: "35"
      AVIF_QUALITY_MAX: "75"
      AVIF_SPEED: "6"
```

- [ ] **Step 3: Validate docs and compose**

Run:

```bash
docker compose config >/tmp/s3-image-optimizer.compose.yaml
go test ./...
```

Expected: both commands PASS.

- [ ] **Step 4: Commit**

```bash
git add README.md compose.yaml
git commit -m "docs: describe avif optimizer contract"
```

---

## Task 5: Add AVIF Resolver Module to s3-static

**Files:**
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimized_variant.go`
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimized_variant_test.go`

- [ ] **Step 1: Switch to s3-static repo and inspect status**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
git status --short
```

Expected: note any pre-existing changes and do not overwrite them.

- [ ] **Step 2: Write failing resolver tests**

Create `internal/handler/optimized_variant_test.go`:

```go
package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"s3-static/internal/config"
	"s3-static/pkg/interfaces"
)

func TestOptimizedVariantResolverRequiresAVIFAccept(t *testing.T) {
	cfg := optimizedTestConfig()
	source := &interfaces.FileInfo{Path: "photo.png", Size: 1024 * 1024, ETag: "source-etag", ContentType: "image/png"}
	optimizedBase := newMockStorage()
	optimized := &openFileMockStorage{mockStorage: optimizedBase}
	resolver := NewOptimizedVariantResolver(optimized, cfg)

	req := httptest.NewRequest(http.MethodGet, "/photo.png", nil)
	file, status := resolver.Resolve(context.Background(), req, source)

	if file != nil {
		t.Fatal("expected no optimized file")
	}
	if status.Code != optimizedStatusNotAccepted {
		t.Fatalf("expected not-accepted, got %#v", status)
	}
	if optimized.openCalls != 0 {
		t.Fatalf("expected optimized storage not to be opened, got %d", optimized.openCalls)
	}
}

func TestOptimizedVariantResolverReturnsTrustedAVIF(t *testing.T) {
	cfg := optimizedTestConfig()
	cfg.OptimizationProfile = "v4-avif-target1m-original"
	source := &interfaces.FileInfo{Path: "photo.png", Size: 1024 * 1024, ETag: "source-etag", ContentType: "image/png"}
	optimizedBase := newMockStorage()
	optimized := &openFileMockStorage{mockStorage: optimizedBase}
	key := avifOptimizedKey(source.Path, cfg.OptimizationProfile)
	optimizedBase.addFileWithMetadata(key, []byte("avif image"), time.Now().UTC(), "avif-etag", "image/avif", map[string]string{
		optimizedSourceKeyMetadata:     source.Path,
		optimizedSourceETagMetadata:    source.ETag,
		optimizedProfileMetadata:       cfg.OptimizationProfile,
		optimizedVariantFormatMetadata: "avif",
	})
	resolver := NewOptimizedVariantResolver(optimized, cfg)

	req := httptest.NewRequest(http.MethodGet, "/photo.png", nil)
	req.Header.Set("Accept", "image/avif,image/webp,image/*,*/*")
	file, status := resolver.Resolve(context.Background(), req, source)
	defer file.Reader.Close()

	if status.Code != optimizedStatusHit {
		t.Fatalf("expected hit, got %#v", status)
	}
	if file.Info.ContentType != "image/avif" {
		t.Fatalf("expected image/avif, got %q", file.Info.ContentType)
	}
	if optimized.openCalls != 1 {
		t.Fatalf("expected one optimized open, got %d", optimized.openCalls)
	}
}
```

- [ ] **Step 3: Run resolver tests to verify failure**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
go test ./internal/handler -run OptimizedVariantResolver -count=1
```

Expected: FAIL because resolver symbols do not exist.

- [ ] **Step 4: Implement resolver module**

Create `internal/handler/optimized_variant.go`:

```go
package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"s3-static/internal/config"
	"s3-static/pkg/interfaces"
)

const (
	optimizedSourceKeyMetadata     = "source-key"
	optimizedVariantFormatMetadata = "variant-format"
	optimizedVariantAVIF          = "avif"

	optimizedStatusHit             = "hit"
	optimizedStatusMiss            = "miss"
	optimizedStatusStale           = "stale"
	optimizedStatusProfileMismatch = "profile-mismatch"
	optimizedStatusNotAccepted     = "not-accepted"
)

type VariantStatus struct {
	Code   string
	Format string
}

func (s VariantStatus) HeaderValue() string {
	if s.Code == optimizedStatusHit && s.Format != "" {
		return s.Code + "; format=" + s.Format
	}
	return s.Code
}

type OptimizedVariantResolver struct {
	storage interfaces.Storage
	config  *config.Config
}

func NewOptimizedVariantResolver(storage interfaces.Storage, cfg *config.Config) *OptimizedVariantResolver {
	return &OptimizedVariantResolver{storage: storage, config: cfg}
}

func (r *OptimizedVariantResolver) Resolve(ctx context.Context, req *http.Request, source *interfaces.FileInfo) (*interfaces.OpenedFile, VariantStatus) {
	if source == nil || !acceptsAVIF(req.Header.Get("Accept")) {
		return nil, VariantStatus{Code: optimizedStatusNotAccepted}
	}
	key := avifOptimizedKey(source.Path, r.config.OptimizationProfile)
	opened, err := openFileFromBackend(ctx, r.storage, key)
	if err != nil {
		return nil, VariantStatus{Code: optimizedStatusMiss}
	}
	if opened.Info.Metadata[optimizedSourceKeyMetadata] != source.Path {
		_ = opened.Reader.Close()
		return nil, VariantStatus{Code: optimizedStatusStale}
	}
	if opened.Info.Metadata[optimizedSourceETagMetadata] != source.ETag {
		_ = opened.Reader.Close()
		return nil, VariantStatus{Code: optimizedStatusStale}
	}
	if opened.Info.Metadata[optimizedProfileMetadata] != r.config.OptimizationProfile {
		_ = opened.Reader.Close()
		return nil, VariantStatus{Code: optimizedStatusProfileMismatch}
	}
	if opened.Info.Metadata[optimizedVariantFormatMetadata] != optimizedVariantAVIF {
		_ = opened.Reader.Close()
		return nil, VariantStatus{Code: optimizedStatusStale}
	}
	return opened, VariantStatus{Code: optimizedStatusHit, Format: optimizedVariantAVIF}
}

func acceptsAVIF(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		mediaType := strings.ToLower(strings.TrimSpace(strings.Split(part, ";")[0]))
		if mediaType == "image/avif" {
			return true
		}
	}
	return false
}

func avifOptimizedKey(sourceKey, profile string) string {
	sum := sha256.Sum256([]byte(sourceKey))
	return ".s3-image-optimizer/avif/" + hex.EncodeToString(sum[:]) + "/" + profile + "/image.avif"
}

func openFileFromBackend(ctx context.Context, backend interfaces.Storage, path string) (*interfaces.OpenedFile, error) {
	if opener, ok := backend.(interfaces.FileOpener); ok {
		return opener.OpenFileContext(ctx, path)
	}
	var info *interfaces.FileInfo
	var err error
	if storageWithContext, ok := backend.(interfaces.ContextStorage); ok {
		info, err = storageWithContext.GetFileInfoContext(ctx, path)
	} else {
		info, err = backend.GetFileInfo(path)
	}
	if err != nil {
		return nil, err
	}
	var reader io.ReadSeekCloser
	if storageWithContext, ok := backend.(interfaces.ContextStorage); ok {
		reader, err = storageWithContext.GetFileReaderContext(ctx, path)
	} else {
		reader, err = backend.GetFileReader(path)
	}
	if err != nil {
		return nil, err
	}
	return &interfaces.OpenedFile{Info: info, Reader: reader}, nil
}
```

Add `io` to the import list before running tests:

```go
import "io"
```

- [ ] **Step 5: Run resolver tests**

Run:

```bash
go test ./internal/handler -run OptimizedVariantResolver -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/handler/optimized_variant.go internal/handler/optimized_variant_test.go
git commit -m "feat: add avif optimized resolver"
```

---

## Task 6: Wire AVIF Resolver into s3-static Handler

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/handler.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/handler_test.go`

- [ ] **Step 1: Update optimized handler tests for Accept-gated AVIF**

Modify `TestFileHandler_OptimizedImageHit`:

```go
source.addFileWithMetadata("photo.png", []byte("original image"), sourceTime, "source-etag", "image/png", nil)
key := avifOptimizedKey("photo.png", cfg.OptimizationProfile)
optimizedBase.addFileWithMetadata(key, []byte("avif image"), optimizedTime, "optimized-etag", "image/avif", map[string]string{
	optimizedSourceKeyMetadata:     "photo.png",
	optimizedSourceETagMetadata:    "source-etag",
	optimizedProfileMetadata:       cfg.OptimizationProfile,
	optimizedVariantFormatMetadata: "avif",
})

req := httptest.NewRequest(http.MethodGet, "/photo.png", nil)
req.Header.Set("Accept", "image/avif,image/webp,image/*,*/*")
```

Update assertions:

```go
if w.Body.String() != "avif image" {
	t.Fatalf("Expected AVIF body, got %q", w.Body.String())
}
if got := w.Header().Get(optimizedStatusHeader); got != "hit; format=avif" {
	t.Fatalf("Expected optimized hit header, got %q", got)
}
if got := w.Header().Get("Content-Type"); got != "image/avif" {
	t.Fatalf("Expected AVIF content type, got %q", got)
}
if got := w.Header().Get("Vary"); got != "Accept" {
	t.Fatalf("Expected Vary Accept, got %q", got)
}
```

Add a no-Accept test:

```go
func TestFileHandler_OptimizedImageRequiresAVIFAccept(t *testing.T) {
	cfg := optimizedTestConfig()
	logger := config.NewLogger("info")
	source := newMockStorage()
	optimizedBase := newMockStorage()
	optimized := &openFileMockStorage{mockStorage: optimizedBase}
	handler := NewFileHandlerWithOptimizedStorage(source, optimized, cfg, logger)

	modTime := time.Now().UTC().Truncate(time.Second)
	source.addFileWithMetadata("photo.png", []byte("original image"), modTime, "source-etag", "image/png", nil)
	key := avifOptimizedKey("photo.png", cfg.OptimizationProfile)
	optimizedBase.addFileWithMetadata(key, []byte("avif image"), modTime, "optimized-etag", "image/avif", map[string]string{
		optimizedSourceKeyMetadata:     "photo.png",
		optimizedSourceETagMetadata:    "source-etag",
		optimizedProfileMetadata:       cfg.OptimizationProfile,
		optimizedVariantFormatMetadata: "avif",
	})

	req := httptest.NewRequest(http.MethodGet, "/photo.png", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}
	if w.Body.String() != "original image" {
		t.Fatalf("Expected original body, got %q", w.Body.String())
	}
	if optimized.openCalls != 0 {
		t.Fatalf("Expected optimized storage not to be opened, got %d", optimized.openCalls)
	}
}
```

- [ ] **Step 2: Run handler tests to verify failure**

Run:

```bash
go test ./internal/handler -run 'OptimizedImage' -count=1
```

Expected: FAIL because handler still opens same-key optimized object and does not check `Accept`.

- [ ] **Step 3: Add resolver field to handler**

In `FileHandler`, add:

```go
optimizedResolver *OptimizedVariantResolver
```

In `NewFileHandlerWithOptimizedStorage`, initialize:

```go
var resolver *OptimizedVariantResolver
if optimized != nil {
	resolver = NewOptimizedVariantResolver(optimized, cfg)
}
```

Return:

```go
return &FileHandler{
	storage:           storage,
	optimizedStorage:  optimized,
	optimizedResolver: resolver,
	config:            cfg,
	logger:            logger,
}
```

- [ ] **Step 4: Update optimized consideration**

Change `canConsiderOptimized` to use the resolver:

```go
return h.config.OptimizedImageEnabled &&
	h.optimizedResolver != nil &&
	r.Method == http.MethodGet &&
	!shouldServeMetadata(r) &&
	r.Header.Get("Range") == ""
```

- [ ] **Step 5: Replace `openTrustedOptimized` path**

In `handleGetObject`, replace:

```go
optimizedFile, status := h.openTrustedOptimized(ctx, sourceInfo)
w.Header().Set(optimizedStatusHeader, status)
if optimizedFile != nil {
```

with:

```go
optimizedFile, status := h.optimizedResolver.Resolve(ctx, r, sourceInfo)
if status.Code != optimizedStatusNotAccepted {
	w.Header().Set(optimizedStatusHeader, status.HeaderValue())
}
if optimizedFile != nil {
	w.Header().Set("Vary", "Accept")
```

Keep the existing `defer Close`, `serveOpenedFile`, and log behavior.

- [ ] **Step 6: Remove or stop using same-key `openTrustedOptimized`**

Delete `openTrustedOptimized` only after all handler tests compile. If keeping it temporarily causes no dead code issue, remove it before commit to avoid two trust paths.

- [ ] **Step 7: Run handler tests**

Run:

```bash
go test ./internal/handler -count=1
```

Expected: PASS.

- [ ] **Step 8: Run full s3-static gate**

Run:

```bash
go test ./...
go vet ./...
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "feat: serve trusted avif optimized objects"
```

---

## Task 7: Document s3-static AVIF Contract

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/README.md`

- [ ] **Step 1: Update README optimized image section**

Document:

````markdown
When optimized image serving is enabled, `s3-static` serves AVIF sidecar objects only for ordinary `GET` requests whose `Accept` header includes `image/avif`.

The source URL stays unchanged. For source key `notes/photo.png`, `s3-static` checks:

```text
.s3-image-optimizer/avif/<sha256-source-key>/<optimization-profile>/image.avif
```

The AVIF object is trusted only when metadata matches:

- `x-amz-meta-source-key`
- `x-amz-meta-source-etag`
- `x-amz-meta-optimization-profile`
- `x-amz-meta-variant-format: avif`

If the request does not advertise AVIF, or if the AVIF object is missing/stale/profile-mismatched, the source object is served.
````

- [ ] **Step 2: Run docs-adjacent tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: describe avif optimized serving"
```

---

## Task 8: Local End-to-End Verification

**Files:**
- Modify only if validation reveals required config drift:
  - `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/compose.yaml`
  - `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/compose.yaml`
  - `/Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services/nodes/node-local/minio/compose.yaml`

- [ ] **Step 1: Build optimizer with AVIF enabled locally**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
just validate
docker build -t s3-image-optimizer:avif-local .
```

Expected: both commands PASS.

- [ ] **Step 2: Build s3-static locally**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
go test ./...
docker build -t s3-static:avif-local .
```

Expected: both commands PASS.

- [ ] **Step 3: Verify AVIF object contract with unit-level fake before live stack**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
go test ./internal/worker -run AVIF -count=1
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
go test ./internal/handler -run 'OptimizedVariant|OptimizedImage' -count=1
```

Expected: PASS.

- [ ] **Step 4: Verify HTTP behavior in a local stack**

After the local stack has a source image and optimized AVIF object, run:

```bash
curl -sS -D /tmp/no-avif.headers -o /tmp/no-avif.body http://localhost:8080/path/to/source.png
curl -sS -H 'Accept: image/avif,image/*,*/*' -D /tmp/avif.headers -o /tmp/avif.body http://localhost:8080/path/to/source.png
```

Expected:

```bash
grep -i '^Content-Type: image/avif' /tmp/avif.headers
grep -i '^Vary: Accept' /tmp/avif.headers
grep -i '^X-S3-Static-Optimized: hit; format=avif' /tmp/avif.headers
```

Expected for no-AVIF request:

```bash
! grep -i '^Content-Type: image/avif' /tmp/no-avif.headers
```

- [ ] **Step 5: Commit any required config drift**

If compose or deployment config changed, commit per repo:

```bash
git add compose.yaml
git commit -m "chore: wire avif optimized image config"
```

If no config changed, do not create an empty commit.

---

## Final Verification

Run in `s3-image-optimizer`:

```bash
go test ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux go build -o /tmp/s3-image-optimizer-avif ./cmd/s3-image-optimizer
```

Run in `s3-static`:

```bash
go test ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux go build -o /tmp/s3-static-avif ./cmd/s3-static
```

Expected: all commands PASS.

Check for accidental sample image staging:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
git status --short
```

Expected: the sample image may remain untracked, but it must not be staged or committed.

## References

- Design spec: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/docs/superpowers/specs/2026-07-03-avif-only-optimized-images-design.md`
- AVIF encoder package: `github.com/gen2brain/avif`
- `s3-static` optimized fallback code: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/handler.go`
