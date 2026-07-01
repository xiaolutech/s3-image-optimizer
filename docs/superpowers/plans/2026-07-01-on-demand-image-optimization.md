# On-Demand Image Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace startup/full-bucket image optimization scans with request-triggered background optimization while preserving the existing public asset domain and paths.

**Architecture:** `s3-static` remains the only public HTTP service. On a trusted optimized-bucket miss, stale object, or profile mismatch, it returns the source object immediately and asynchronously posts the source key to the internal `s3-image-optimizer` sidecar. `s3-image-optimizer` defaults to no scanning, exposes a bounded `/optimize` queue, processes one key at a time, and still supports explicit `RUN_ONCE=true` or `SCAN_ENABLED=true` scans for maintenance.

**Tech Stack:** Go, AWS SDK v2 S3-compatible MinIO access, Docker Compose, GitHub Container Registry image dispatch, my-services GitOps.

---

## File Structure

This is a three-repository change. Keep each commit scoped to one repository.

### `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer`

- Modify `internal/config/config.go`: add `SCAN_ENABLED`, `TRIGGER_QUEUE_SIZE`, and scan retry settings. Default scanning must be disabled.
- Modify `internal/config/config_test.go`: cover defaults, env loading, and validation for the new fields.
- Modify `internal/worker/worker.go`: add `ProcessKey(ctx, key)` so a single requested key can be optimized without listing the source bucket. Keep `RunOnce` for explicit scans and add bounded retry only around whole-scan list failures.
- Modify `internal/worker/worker_test.go`: prove `ProcessKey` does not call `ListObjects`; prove transient scan list failures retry.
- Create `cmd/s3-image-optimizer/trigger_queue.go`: own the bounded in-process queue and `/optimize` HTTP handler.
- Create `cmd/s3-image-optimizer/trigger_queue_test.go`: prove queue dedupe, full queue response, JSON/query parsing, and worker invocation.
- Modify `cmd/s3-image-optimizer/main.go`: wire the trigger queue, keep `/health`, and run scans only when `RUN_ONCE=true` or `SCAN_ENABLED=true`.
- Modify `README.md`: document on-demand behavior and scan escape hatches.

### `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static`

- Modify `internal/config/config.go`: add `OPTIMIZER_TRIGGER_URL` and `OPTIMIZER_TRIGGER_TIMEOUT`; empty URL disables triggering.
- Modify `internal/config/config_test.go`: cover default disabled trigger, env loading, invalid URL, and invalid timeout.
- Create `internal/handler/optimizer_trigger.go`: define `OptimizerTrigger`, `HTTPOptimizerTrigger`, and a no-op disabled behavior.
- Modify `internal/handler/handler.go`: call the trigger asynchronously after optimized miss, stale object, or profile mismatch; never block source response.
- Modify `internal/handler/handler_test.go`: prove source response still succeeds when trigger is queued or fails; prove no trigger on optimized hit, range request, HEAD request, metadata request, non-image, or below threshold.
- Modify `cmd/s3-static/main.go`: construct the HTTP trigger from config and pass it into the handler.
- Modify `README.md`: document the optional trigger URL and timeout.

### `/Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services`

- Modify `nodes/node-local/minio/compose.yaml`: set `OPTIMIZER_TRIGGER_URL=http://s3-image-optimizer:8080/optimize` on `s3static`; set `SCAN_ENABLED=false` and a bounded `TRIGGER_QUEUE_SIZE` on `s3-image-optimizer`.
- No `deploy.env` changes should be made by hand unless image refs have changed through the dispatch workflow.

---

## Task 1: Stabilize Optimizer Single-Key Worker and Scan Config

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/config/config.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/config/config_test.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker_test.go`

- [ ] **Step 1: Write config tests for default disabled scans and trigger settings**

Add these assertions to `TestDefaultConfig` in `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/config/config_test.go`:

```go
if cfg.ScanEnabled {
	t.Fatal("expected scan enabled false by default")
}
if cfg.TriggerQueueSize != 256 {
	t.Fatalf("expected trigger queue size 256, got %d", cfg.TriggerQueueSize)
}
if cfg.ScanRetryAttempts != 8 {
	t.Fatalf("expected scan retry attempts 8, got %d", cfg.ScanRetryAttempts)
}
if cfg.ScanRetryInitialDelay != 5*time.Second {
	t.Fatalf("expected scan retry initial delay 5s, got %v", cfg.ScanRetryInitialDelay)
}
if cfg.ScanRetryMaxDelay != 2*time.Minute {
	t.Fatalf("expected scan retry max delay 2m, got %v", cfg.ScanRetryMaxDelay)
}
```

- [ ] **Step 2: Write config env loading tests**

Add these env vars to `TestLoadFromEnv` in `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/config/config_test.go`:

```go
t.Setenv("SCAN_ENABLED", "true")
t.Setenv("TRIGGER_QUEUE_SIZE", "32")
t.Setenv("SCAN_RETRY_ATTEMPTS", "4")
t.Setenv("SCAN_RETRY_INITIAL_DELAY", "2s")
t.Setenv("SCAN_RETRY_MAX_DELAY", "30s")
```

Add these assertions in the same test:

```go
if !cfg.ScanEnabled {
	t.Fatal("expected scan enabled true")
}
if cfg.TriggerQueueSize != 32 {
	t.Fatalf("expected trigger queue size 32, got %d", cfg.TriggerQueueSize)
}
if cfg.ScanRetryAttempts != 4 {
	t.Fatalf("expected retry attempts 4, got %d", cfg.ScanRetryAttempts)
}
if cfg.ScanRetryInitialDelay != 2*time.Second {
	t.Fatalf("expected retry initial delay 2s, got %v", cfg.ScanRetryInitialDelay)
}
if cfg.ScanRetryMaxDelay != 30*time.Second {
	t.Fatalf("expected retry max delay 30s, got %v", cfg.ScanRetryMaxDelay)
}
```

- [ ] **Step 3: Write config validation tests**

Add these cases to the existing invalid config table in `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/config/config_test.go`:

```go
{
	name:      "invalid retry attempts",
	mutate:    func(cfg *Config) { cfg.ScanRetryAttempts = 0 },
	wantError: "SCAN_RETRY_ATTEMPTS",
},
{
	name:      "invalid trigger queue size",
	mutate:    func(cfg *Config) { cfg.TriggerQueueSize = 0 },
	wantError: "TRIGGER_QUEUE_SIZE",
},
{
	name:      "negative retry initial delay",
	mutate:    func(cfg *Config) { cfg.ScanRetryInitialDelay = -1 },
	wantError: "SCAN_RETRY_INITIAL_DELAY",
},
{
	name:      "negative retry max delay",
	mutate:    func(cfg *Config) { cfg.ScanRetryMaxDelay = -1 },
	wantError: "SCAN_RETRY_MAX_DELAY",
},
{
	name:      "retry initial delay exceeds max delay",
	mutate:    func(cfg *Config) { cfg.ScanRetryInitialDelay = time.Minute; cfg.ScanRetryMaxDelay = time.Second },
	wantError: "SCAN_RETRY_INITIAL_DELAY",
},
```

Add these invalid env cases to `TestLoadRejectsInvalidEnv`:

```go
{name: "invalid scan enabled", key: "SCAN_ENABLED", val: "sometimes"},
{name: "invalid trigger queue size", key: "TRIGGER_QUEUE_SIZE", val: "many"},
{name: "invalid retry attempts", key: "SCAN_RETRY_ATTEMPTS", val: "many"},
{name: "invalid retry initial delay", key: "SCAN_RETRY_INITIAL_DELAY", val: "soon"},
{name: "invalid retry max delay", key: "SCAN_RETRY_MAX_DELAY", val: "soon"},
```

- [ ] **Step 4: Run config tests and verify they fail before implementation**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
go test ./internal/config
```

Expected before implementation: FAIL with missing fields such as `cfg.ScanEnabled undefined` or validation failures for the new env vars.

- [ ] **Step 5: Implement config fields and env loading**

In `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/config/config.go`, update `Config`:

```go
ScanInterval     time.Duration
ScanEnabled      bool
RunOnce          bool
ProcessDelay     time.Duration
TriggerQueueSize int

ScanRetryAttempts     int
ScanRetryInitialDelay time.Duration
ScanRetryMaxDelay     time.Duration
```

Update `DefaultConfig`:

```go
return &Config{
	Port:                  "8080",
	S3Region:              "us-east-1",
	S3UseSSL:              true,
	OptimizationProfile:   "v2-jpeg82-png-best-original-width",
	MaxWidth:              0,
	JPEGQuality:           82,
	MinBytes:              512 * 1024,
	ScanInterval:          24 * time.Hour,
	ScanEnabled:           false,
	ProcessDelay:          0,
	TriggerQueueSize:      256,
	ScanRetryAttempts:     8,
	ScanRetryInitialDelay: 5 * time.Second,
	ScanRetryMaxDelay:     2 * time.Minute,
}
```

Add env loading in `Load` immediately after `SCAN_INTERVAL` and `PROCESS_DELAY` parsing:

```go
if cfg.ScanEnabled, err = getenvBool("SCAN_ENABLED", cfg.ScanEnabled); err != nil {
	return nil, err
}
if cfg.ProcessDelay, err = getenvDuration("PROCESS_DELAY", cfg.ProcessDelay); err != nil {
	return nil, err
}
if cfg.TriggerQueueSize, err = getenvInt("TRIGGER_QUEUE_SIZE", cfg.TriggerQueueSize); err != nil {
	return nil, err
}
if cfg.ScanRetryAttempts, err = getenvInt("SCAN_RETRY_ATTEMPTS", cfg.ScanRetryAttempts); err != nil {
	return nil, err
}
if cfg.ScanRetryInitialDelay, err = getenvDuration("SCAN_RETRY_INITIAL_DELAY", cfg.ScanRetryInitialDelay); err != nil {
	return nil, err
}
if cfg.ScanRetryMaxDelay, err = getenvDuration("SCAN_RETRY_MAX_DELAY", cfg.ScanRetryMaxDelay); err != nil {
	return nil, err
}
```

Add validation in `Validate` after `PROCESS_DELAY` validation:

```go
if c.TriggerQueueSize < 1 {
	return fmt.Errorf("TRIGGER_QUEUE_SIZE must be at least 1")
}
if c.ScanRetryAttempts < 1 {
	return fmt.Errorf("SCAN_RETRY_ATTEMPTS must be at least 1")
}
if c.ScanRetryInitialDelay < 0 {
	return fmt.Errorf("SCAN_RETRY_INITIAL_DELAY cannot be negative")
}
if c.ScanRetryMaxDelay < 0 {
	return fmt.Errorf("SCAN_RETRY_MAX_DELAY cannot be negative")
}
if c.ScanRetryMaxDelay > 0 && c.ScanRetryInitialDelay > c.ScanRetryMaxDelay {
	return fmt.Errorf("SCAN_RETRY_INITIAL_DELAY cannot exceed SCAN_RETRY_MAX_DELAY")
}
```

Add these keys to the env cleanup helper:

```go
"SCAN_ENABLED",
"TRIGGER_QUEUE_SIZE",
"SCAN_RETRY_ATTEMPTS",
"SCAN_RETRY_INITIAL_DELAY",
"SCAN_RETRY_MAX_DELAY",
```

- [ ] **Step 6: Write worker tests for single-key processing and scan retries**

In `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker_test.go`, add `errors` and `time` imports.

Add this test:

```go
func TestWorkerProcessKeyHeadsSourceWithoutListingBucket(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	store.objects[objKey("source", "photo.jpg")] = fakeObject{
		info: storage.ObjectInfo{
			Key:         "photo.jpg",
			Size:        int64(len(body)),
			ETag:        "photo",
			ContentType: "image/jpeg",
		},
		body: body,
	}

	w := New(store, testWorkerConfig())
	if err := w.ProcessKey(context.Background(), "photo.jpg"); err != nil {
		t.Fatalf("ProcessKey failed: %v", err)
	}

	if store.listCalls != 0 {
		t.Fatalf("expected no list calls, got %d", store.listCalls)
	}
	if _, ok := store.objects[objKey("optimized", "photo.jpg")]; !ok {
		t.Fatal("expected photo.jpg optimized object")
	}
}
```

Add this test:

```go
func TestWorkerRunOnceRetriesTransientListErrors(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	store.objects[objKey("source", "photo.jpg")] = fakeObject{
		info: storage.ObjectInfo{
			Key:         "photo.jpg",
			Size:        int64(len(body)),
			ETag:        "photo",
			ContentType: "image/jpeg",
		},
		body: body,
	}
	store.listErrorsRemaining = 2
	store.listErr = errors.New("connect: connection refused")

	cfg := testWorkerConfig()
	cfg.ScanRetryAttempts = 3
	cfg.ScanRetryInitialDelay = time.Nanosecond
	cfg.ScanRetryMaxDelay = time.Nanosecond

	w := New(store, cfg)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce failed after transient list errors: %v", err)
	}

	if store.listCalls != 3 {
		t.Fatalf("expected 3 list attempts, got %d", store.listCalls)
	}
	if _, ok := store.objects[objKey("optimized", "photo.jpg")]; !ok {
		t.Fatal("expected photo.jpg optimized object")
	}
}
```

Extend `fakeStore`:

```go
type fakeStore struct {
	objects             map[string]fakeObject
	getCalls            int
	listCalls           int
	putKeys             []string
	listBucket          string
	listErrorsRemaining int
	listErr             error
}
```

Update `ListObjects`:

```go
func (s *fakeStore) ListObjects(ctx context.Context, bucket, prefix string, visit func(storage.ObjectInfo) error) error {
	s.listCalls++
	s.listBucket = bucket
	if s.listErrorsRemaining > 0 {
		s.listErrorsRemaining--
		return s.listErr
	}
	var keys []string
	for fullKey, obj := range s.objects {
		if !strings.HasPrefix(fullKey, bucket+"/") {
			continue
		}
		keys = append(keys, strings.TrimPrefix(fullKey, bucket+"/"))
		_ = obj
	}
	sort.Strings(keys)
	for _, key := range keys {
		obj := s.objects[objKey(bucket, key)]
		if err := visit(obj.info); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 7: Run worker tests and verify they fail before implementation**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
go test ./internal/worker
```

Expected before implementation: FAIL with `w.ProcessKey undefined` and missing retry fields on worker config.

- [ ] **Step 8: Implement worker single-key processing and scan retries**

In `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker.go`, update `Config`:

```go
SourceBucket          string
OptimizedBucket       string
OptimizationProfile   string
MaxWidth              int
JPEGQuality           int
MinBytes              int64
ProcessDelay          time.Duration
ScanRetryAttempts     int
ScanRetryInitialDelay time.Duration
ScanRetryMaxDelay     time.Duration
```

Replace `RunOnce` with:

```go
func (w *Worker) RunOnce(ctx context.Context) error {
	attempts := w.cfg.ScanRetryAttempts
	if attempts <= 0 {
		attempts = 1
	}
	delay := w.cfg.ScanRetryInitialDelay

	for attempt := 1; attempt <= attempts; attempt++ {
		err := w.runOnce(ctx)
		if err == nil {
			return nil
		}
		if attempt == attempts {
			return err
		}

		log.Printf("scan attempt failed attempt=%d/%d retry_in=%s err=%v", attempt, attempts, delay, err)
		if err := wait(ctx, delay); err != nil {
			return err
		}
		delay = w.nextRetryDelay(delay)
	}
	return nil
}

func (w *Worker) runOnce(ctx context.Context) error {
	return w.store.ListObjects(ctx, w.cfg.SourceBucket, "", func(info storage.ObjectInfo) error {
		return w.ProcessObject(ctx, info)
	})
}
```

Add `ProcessKey`:

```go
func (w *Worker) ProcessKey(ctx context.Context, key string) error {
	if key == "" {
		return fmt.Errorf("source key is required")
	}
	if err := w.waitForRequestDelay(ctx); err != nil {
		return err
	}
	sourceCtx, sourceCancel := context.WithTimeout(ctx, headObjectTimeout)
	defer sourceCancel()
	source, err := w.store.HeadObject(sourceCtx, w.cfg.SourceBucket, key)
	if err != nil {
		return fmt.Errorf("head source object %s: %w", key, err)
	}
	return w.ProcessObject(ctx, *source)
}
```

Replace `waitForRequestDelay` and add helpers:

```go
func (w *Worker) waitForRequestDelay(ctx context.Context) error {
	return wait(ctx, w.cfg.ProcessDelay)
}

func (w *Worker) nextRetryDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	next := delay * 2
	if next < delay {
		next = delay
	}
	if w.cfg.ScanRetryMaxDelay > 0 && next > w.cfg.ScanRetryMaxDelay {
		return w.cfg.ScanRetryMaxDelay
	}
	return next
}

func wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
```

- [ ] **Step 9: Run optimizer package tests**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
gofmt -w internal/config/config.go internal/config/config_test.go internal/worker/worker.go internal/worker/worker_test.go
go test ./internal/config ./internal/worker
```

Expected: PASS for both packages.

- [ ] **Step 10: Commit optimizer worker/config changes**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
git add internal/config/config.go internal/config/config_test.go internal/worker/worker.go internal/worker/worker_test.go
git commit -m "feat: add on-demand optimizer worker entrypoint"
```

Expected: commit created with only config and worker files.

---

## Task 2: Add Optimizer HTTP Trigger Queue

**Files:**
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/cmd/s3-image-optimizer/trigger_queue.go`
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/cmd/s3-image-optimizer/trigger_queue_test.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/cmd/s3-image-optimizer/main.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/README.md`

- [ ] **Step 1: Write trigger queue tests**

Create `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/cmd/s3-image-optimizer/trigger_queue_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeKeyProcessor struct {
	mu      sync.Mutex
	keys    []string
	started chan string
	block   chan struct{}
}

func newFakeKeyProcessor() *fakeKeyProcessor {
	return &fakeKeyProcessor{
		started: make(chan string, 8),
		block:   make(chan struct{}),
	}
}

func (p *fakeKeyProcessor) ProcessKey(ctx context.Context, key string) error {
	p.mu.Lock()
	p.keys = append(p.keys, key)
	p.mu.Unlock()
	p.started <- key
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.block:
		return nil
	}
}

func (p *fakeKeyProcessor) processedKeys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	keys := make([]string, len(p.keys))
	copy(keys, p.keys)
	return keys
}

func TestOptimizeHandlerQueuesQueryKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := newFakeKeyProcessor()
	defer close(processor.block)
	queue := startTriggerQueue(ctx, processor, 4)

	req := httptest.NewRequest(http.MethodPost, "/optimize?key=notes/photo.jpg", nil)
	rec := httptest.NewRecorder()
	optimizeHandler(queue)(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "queued" || body["key"] != "notes/photo.jpg" {
		t.Fatalf("unexpected body %#v", body)
	}

	select {
	case got := <-processor.started:
		if got != "notes/photo.jpg" {
			t.Fatalf("expected queued key notes/photo.jpg, got %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for processor")
	}
}

func TestOptimizeHandlerQueuesJSONKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := newFakeKeyProcessor()
	defer close(processor.block)
	queue := startTriggerQueue(ctx, processor, 4)

	req := httptest.NewRequest(http.MethodPost, "/optimize", strings.NewReader(`{"key":"json/photo.jpg"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	optimizeHandler(queue)(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case got := <-processor.started:
		if got != "json/photo.jpg" {
			t.Fatalf("expected queued key json/photo.jpg, got %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for processor")
	}
}

func TestTriggerQueueDeduplicatesPendingKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := newFakeKeyProcessor()
	queue := startTriggerQueue(ctx, processor, 4)

	status, err := queue.enqueue("same.jpg")
	if err != nil || status != "queued" {
		t.Fatalf("expected first enqueue queued, status=%s err=%v", status, err)
	}
	status, err = queue.enqueue("same.jpg")
	if err != nil || status != "already_queued" {
		t.Fatalf("expected duplicate already_queued, status=%s err=%v", status, err)
	}

	close(processor.block)
	select {
	case <-processor.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for processor")
	}
}

func TestTriggerQueueRejectsFullQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := newFakeKeyProcessor()
	queue := startTriggerQueue(ctx, processor, 1)

	status, err := queue.enqueue("first.jpg")
	if err != nil || status != "queued" {
		t.Fatalf("expected first enqueue queued, status=%s err=%v", status, err)
	}
	status, err = queue.enqueue("second.jpg")
	if err == nil || status != "" {
		t.Fatalf("expected full queue error, status=%s err=%v", status, err)
	}
	close(processor.block)
}

func TestOptimizeHandlerRejectsInvalidRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := newFakeKeyProcessor()
	defer close(processor.block)
	queue := startTriggerQueue(ctx, processor, 4)

	tests := []struct {
		name string
		req  *http.Request
		code int
	}{
		{name: "method", req: httptest.NewRequest(http.MethodGet, "/optimize?key=x.jpg", nil), code: http.StatusMethodNotAllowed},
		{name: "missing key", req: httptest.NewRequest(http.MethodPost, "/optimize", nil), code: http.StatusBadRequest},
		{name: "invalid json", req: httptest.NewRequest(http.MethodPost, "/optimize", strings.NewReader(`{`)), code: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			optimizeHandler(queue)(rec, tt.req)
			if rec.Code != tt.code {
				t.Fatalf("expected status %d, got %d body=%s", tt.code, rec.Code, rec.Body.String())
			}
		})
	}
}
```

- [ ] **Step 2: Run trigger queue tests and verify they fail before implementation**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
go test ./cmd/s3-image-optimizer
```

Expected before implementation: FAIL with `undefined: startTriggerQueue` and `undefined: optimizeHandler`.

- [ ] **Step 3: Implement trigger queue**

Create `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/cmd/s3-image-optimizer/trigger_queue.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
)

type keyProcessor interface {
	ProcessKey(ctx context.Context, key string) error
}

type triggerQueue struct {
	processor keyProcessor
	keys      chan string
	mu        sync.Mutex
	pending   map[string]struct{}
}

func startTriggerQueue(ctx context.Context, processor keyProcessor, size int) *triggerQueue {
	q := &triggerQueue{
		processor: processor,
		keys:      make(chan string, size),
		pending:   make(map[string]struct{}),
	}
	go q.run(ctx)
	return q
}

func (q *triggerQueue) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case key := <-q.keys:
			log.Printf("on-demand optimize started key=%s", key)
			if err := q.processor.ProcessKey(ctx, key); err != nil {
				log.Printf("on-demand optimize failed key=%s err=%v", key, err)
			} else {
				log.Printf("on-demand optimize completed key=%s", key)
			}
			q.finish(key)
		}
	}
}

func (q *triggerQueue) enqueue(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("key is required")
	}

	q.mu.Lock()
	if _, ok := q.pending[key]; ok {
		q.mu.Unlock()
		return "already_queued", nil
	}
	if len(q.pending) >= cap(q.keys) {
		q.mu.Unlock()
		return "", errors.New("trigger queue is full")
	}
	q.pending[key] = struct{}{}
	q.mu.Unlock()

	select {
	case q.keys <- key:
		return "queued", nil
	default:
		q.finish(key)
		return "", errors.New("trigger queue is full")
	}
}

func (q *triggerQueue) finish(key string) {
	q.mu.Lock()
	delete(q.pending, key)
	q.mu.Unlock()
}

type optimizeRequest struct {
	Key string `json:"key"`
}

func optimizeHandler(triggers *triggerQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		key := r.URL.Query().Get("key")
		if key == "" && r.Body != nil {
			var req optimizeRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
				return
			}
			key = req.Key
		}

		status, err := triggers.enqueue(key)
		if err != nil {
			code := http.StatusBadRequest
			if strings.Contains(err.Error(), "queue is full") {
				code = http.StatusTooManyRequests
			}
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": status, "key": strings.TrimSpace(key)})
	}
}

func writeJSON(w http.ResponseWriter, status int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
```

- [ ] **Step 4: Wire main to run trigger-only mode by default**

In `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/cmd/s3-image-optimizer/main.go`, pass retry fields into `worker.New`:

```go
w := worker.New(store, worker.Config{
	SourceBucket:          cfg.SourceBucket,
	OptimizedBucket:       cfg.OptimizedBucket,
	OptimizationProfile:   cfg.OptimizationProfile,
	MaxWidth:              cfg.MaxWidth,
	JPEGQuality:           cfg.JPEGQuality,
	MinBytes:              cfg.MinBytes,
	ProcessDelay:          cfg.ProcessDelay,
	ScanRetryAttempts:     cfg.ScanRetryAttempts,
	ScanRetryInitialDelay: cfg.ScanRetryInitialDelay,
	ScanRetryMaxDelay:     cfg.ScanRetryMaxDelay,
})
```

Replace health server startup and main scan branch with:

```go
triggers := startTriggerQueue(ctx, w, cfg.TriggerQueueSize)
server := startHealthServer(cfg.Port, triggers)
defer shutdownHealthServer(server)

if cfg.RunOnce {
	if err := w.RunOnce(ctx); err != nil {
		log.Fatalf("run once: %v", err)
	}
	return
}

if cfg.ScanEnabled {
	runLoop(ctx, w, cfg.ScanInterval)
	return
}

log.Printf("scan loop disabled; waiting for on-demand optimize triggers")
<-ctx.Done()
log.Printf("shutdown requested")
```

Change the health server signature and add `/optimize`:

```go
func startHealthServer(port string, triggers *triggerQueue) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	})
	mux.HandleFunc("/optimize", optimizeHandler(triggers))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%s", port),
		Handler: mux,
	}
	go func() {
		log.Printf("health server listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("health server: %v", err)
		}
	}()
	return server
}
```

- [ ] **Step 5: Document optimizer behavior**

In `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/README.md`, change the behavior bullets:

```md
- Waits for on-demand optimization triggers by default.
- Scans `SOURCE_BUCKET` only when `SCAN_ENABLED=true` or `RUN_ONCE=true`.
```

Add this section:

```md
## On-Demand Optimization

Use this path when `s3-static` serves a source object because the optimized copy is missing or stale. The request returns immediately after the object key is queued; optimization runs in the background with one worker, so first access is not blocked by image processing.

```bash
curl -X POST 'http://s3-image-optimizer:8080/optimize?key=notes/photo.jpg'
```

Equivalent JSON body:

```bash
curl -X POST 'http://s3-image-optimizer:8080/optimize' \
  -H 'Content-Type: application/json' \
  -d '{"key":"notes/photo.jpg"}'
```

Responses:

- `202 {"status":"queued","key":"notes/photo.jpg"}` - accepted for background optimization.
- `202 {"status":"already_queued","key":"notes/photo.jpg"}` - this key is already queued or processing.
- `429 {"error":"trigger queue is full"}` - worker is saturated; caller should retry after backlog drains.
```

Add config bullets:

```md
- `SCAN_ENABLED` - Enable continuous full-bucket scanning. Default: `false`.
- `TRIGGER_QUEUE_SIZE` - Maximum on-demand keys queued or processing at once. Default: `256`.
- `SCAN_RETRY_ATTEMPTS` - Whole-scan retry attempts after a failed scan, including the first attempt. Set to `1` to disable scan retries. Default: `8`.
- `SCAN_RETRY_INITIAL_DELAY` - Initial whole-scan retry delay. Default: `5s`.
- `SCAN_RETRY_MAX_DELAY` - Maximum whole-scan retry delay after exponential backoff. Default: `2m`.
```

- [ ] **Step 6: Run optimizer tests**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
gofmt -w cmd/s3-image-optimizer/main.go cmd/s3-image-optimizer/trigger_queue.go cmd/s3-image-optimizer/trigger_queue_test.go
go test ./...
```

Expected: all optimizer packages pass.

- [ ] **Step 7: Commit optimizer HTTP trigger changes**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
git add cmd/s3-image-optimizer/main.go cmd/s3-image-optimizer/trigger_queue.go cmd/s3-image-optimizer/trigger_queue_test.go README.md
git commit -m "feat: accept on-demand optimization triggers"
```

Expected: commit created with HTTP trigger files and docs.

---

## Task 3: Add Non-Blocking Trigger Client to s3-static

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/config/config.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/config/config_test.go`
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimizer_trigger.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/cmd/s3-static/main.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/README.md`

- [ ] **Step 1: Write config tests**

In `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/config/config_test.go`, add default assertions to `TestDefaultConfig`:

```go
if config.OptimizerTriggerURL != "" {
	t.Errorf("Expected default optimizer trigger URL to be empty, got '%s'", config.OptimizerTriggerURL)
}
if config.OptimizerTriggerTimeout != 2*time.Second {
	t.Errorf("Expected default optimizer trigger timeout 2s, got %v", config.OptimizerTriggerTimeout)
}
```

Add env setup to `TestLoadFromEnv_WithEnvironmentVariables`:

```go
os.Setenv("OPTIMIZER_TRIGGER_URL", "http://s3-image-optimizer:8080/optimize")
os.Setenv("OPTIMIZER_TRIGGER_TIMEOUT", "1500ms")
```

Add assertions in the same test:

```go
if config.OptimizerTriggerURL != "http://s3-image-optimizer:8080/optimize" {
	t.Errorf("Expected optimizer trigger URL to be loaded, got '%s'", config.OptimizerTriggerURL)
}
if config.OptimizerTriggerTimeout != 1500*time.Millisecond {
	t.Errorf("Expected optimizer trigger timeout 1500ms, got %v", config.OptimizerTriggerTimeout)
}
```

Add invalid env tests:

```go
func TestLoadFromEnv_InvalidOptimizerTriggerURL(t *testing.T) {
	clearEnvVars()
	os.Setenv("OPTIMIZER_TRIGGER_URL", "://bad-url")

	_, err := LoadFromEnv()
	if err == nil {
		t.Error("Expected error for invalid optimizer trigger URL, got nil")
	}
}

func TestLoadFromEnv_InvalidOptimizerTriggerTimeout(t *testing.T) {
	clearEnvVars()
	os.Setenv("OPTIMIZER_TRIGGER_TIMEOUT", "soon")

	_, err := LoadFromEnv()
	if err == nil {
		t.Error("Expected error for invalid optimizer trigger timeout, got nil")
	}
}

func TestValidate_OptimizerTriggerTimeoutMustBePositive(t *testing.T) {
	config := DefaultConfig()
	config.OptimizerTriggerURL = "http://s3-image-optimizer:8080/optimize"
	config.OptimizerTriggerTimeout = 0

	err := config.Validate()
	if err == nil {
		t.Error("Expected error for non-positive optimizer trigger timeout, got nil")
	}
}
```

Add cleanup keys in the test helper:

```go
"OPTIMIZER_TRIGGER_URL",
"OPTIMIZER_TRIGGER_TIMEOUT",
```

- [ ] **Step 2: Run config tests and verify they fail before implementation**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
go test ./internal/config
```

Expected before implementation: FAIL with `config.OptimizerTriggerURL undefined`.

- [ ] **Step 3: Implement s3-static trigger config**

In `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/config/config.go`, add imports:

```go
import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"
)
```

Add fields to `Config`:

```go
OptimizerTriggerURL     string        `env:"OPTIMIZER_TRIGGER_URL"`
OptimizerTriggerTimeout time.Duration `env:"OPTIMIZER_TRIGGER_TIMEOUT"`
```

Add defaults:

```go
OptimizerTriggerURL:     "",
OptimizerTriggerTimeout: 2 * time.Second,
```

Load string env:

```go
if optimizerTriggerURL, ok := os.LookupEnv("OPTIMIZER_TRIGGER_URL"); ok {
	config.OptimizerTriggerURL = optimizerTriggerURL
}
```

Load duration env:

```go
if optimizerTriggerTimeoutStr := os.Getenv("OPTIMIZER_TRIGGER_TIMEOUT"); optimizerTriggerTimeoutStr != "" {
	duration, err := time.ParseDuration(optimizerTriggerTimeoutStr)
	if err != nil {
		return nil, fmt.Errorf("invalid OPTIMIZER_TRIGGER_TIMEOUT format: %w", err)
	}
	config.OptimizerTriggerTimeout = duration
}
```

Validate:

```go
if c.OptimizerTriggerURL != "" {
	parsed, err := url.Parse(c.OptimizerTriggerURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("OPTIMIZER_TRIGGER_URL must be a valid absolute URL, got: %s", c.OptimizerTriggerURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("OPTIMIZER_TRIGGER_URL scheme must be http or https, got: %s", parsed.Scheme)
	}
	if c.OptimizerTriggerTimeout <= 0 {
		return fmt.Errorf("OPTIMIZER_TRIGGER_TIMEOUT must be positive, got: %v", c.OptimizerTriggerTimeout)
	}
}
```

- [ ] **Step 4: Create HTTP optimizer trigger client**

Create `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimizer_trigger.go`:

```go
package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type OptimizerTrigger interface {
	Trigger(ctx context.Context, key string) error
}

type HTTPOptimizerTrigger struct {
	endpoint string
	client   *http.Client
}

func NewHTTPOptimizerTrigger(endpoint string, timeout time.Duration) *HTTPOptimizerTrigger {
	return &HTTPOptimizerTrigger{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (t *HTTPOptimizerTrigger) Trigger(ctx context.Context, key string) error {
	if t == nil || t.endpoint == "" {
		return nil
	}

	endpointURL, err := url.Parse(t.endpoint)
	if err != nil {
		return fmt.Errorf("parse optimizer trigger URL: %w", err)
	}
	values := endpointURL.Query()
	values.Set("key", key)
	endpointURL.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL.String(), nil)
	if err != nil {
		return fmt.Errorf("create optimizer trigger request: %w", err)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("post optimizer trigger: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("optimizer trigger returned %s", resp.Status)
	}
	return nil
}
```

- [ ] **Step 5: Wire trigger construction in main**

In `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/cmd/s3-static/main.go`, create trigger before handler creation:

```go
var optimizerTrigger handler.OptimizerTrigger
if cfg.OptimizerTriggerURL != "" {
	optimizerTrigger = handler.NewHTTPOptimizerTrigger(cfg.OptimizerTriggerURL, cfg.OptimizerTriggerTimeout)
}
```

Replace handler construction:

```go
fileHandler := handler.NewFileHandlerWithOptimizedStorageAndTrigger(storageInstance, optimizedStorage, optimizerTrigger, cfg, logger)
```

- [ ] **Step 6: Document s3-static trigger config**

In `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/README.md`, add config bullets near the optimized image section:

```md
- `OPTIMIZER_TRIGGER_URL` - Optional internal URL that receives `POST /optimize?key=...` when an optimized image is missing, stale, or built with a different profile. Empty disables on-demand triggers.
- `OPTIMIZER_TRIGGER_TIMEOUT` - Timeout for the non-blocking optimizer trigger HTTP request. Default: `2s`.
```

Add behavior note:

```md
When `OPTIMIZER_TRIGGER_URL` is configured, a trusted optimized-bucket miss does not change the public response. `s3-static` serves the source object immediately and triggers optimization in the background. A subsequent request can serve the optimized object after the sidecar writes it with matching `source-etag` and `optimization-profile` metadata.
```

- [ ] **Step 7: Run config tests**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
gofmt -w internal/config/config.go internal/config/config_test.go internal/handler/optimizer_trigger.go cmd/s3-static/main.go
go test ./internal/config
```

Expected: PASS for `s3-static/internal/config`.

- [ ] **Step 8: Commit s3-static config and trigger client**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
git add internal/config/config.go internal/config/config_test.go internal/handler/optimizer_trigger.go cmd/s3-static/main.go README.md
git commit -m "feat: add optimizer trigger client"
```

Expected: commit created with config, trigger client, main wiring, and docs.

---

## Task 4: Trigger Optimization from s3-static Fallback

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/handler.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/handler_test.go`

- [ ] **Step 1: Add trigger-aware constructor and handler field**

In `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/handler.go`, update `FileHandler`:

```go
type FileHandler struct {
	storage          interfaces.Storage
	optimizedStorage interfaces.Storage
	optimizerTrigger  OptimizerTrigger
	config           *config.Config
	logger           *config.Logger
}
```

Replace `NewFileHandlerWithOptimizedStorage` with:

```go
func NewFileHandlerWithOptimizedStorage(storage interfaces.Storage, optimized interfaces.Storage, cfg *config.Config, logger *config.Logger) *FileHandler {
	return NewFileHandlerWithOptimizedStorageAndTrigger(storage, optimized, nil, cfg, logger)
}

func NewFileHandlerWithOptimizedStorageAndTrigger(storage interfaces.Storage, optimized interfaces.Storage, trigger OptimizerTrigger, cfg *config.Config, logger *config.Logger) *FileHandler {
	return &FileHandler{
		storage:          storage,
		optimizedStorage: optimized,
		optimizerTrigger:  trigger,
		config:           cfg,
		logger:           logger,
	}
}
```

- [ ] **Step 2: Add handler tests for fallback trigger behavior**

In `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/handler_test.go`, add `errors` and `sync` to the import list.

Add this mock:

```go
type recordingOptimizerTrigger struct {
	mu     sync.Mutex
	calls  []string
	err    error
	called chan string
}

func newRecordingOptimizerTrigger(err error) *recordingOptimizerTrigger {
	return &recordingOptimizerTrigger{
		err:    err,
		called: make(chan string, 4),
	}
}

func (t *recordingOptimizerTrigger) Trigger(ctx context.Context, key string) error {
	t.mu.Lock()
	t.calls = append(t.calls, key)
	t.mu.Unlock()
	select {
	case t.called <- key:
	default:
	}
	return t.err
}

func (t *recordingOptimizerTrigger) waitForCall(tb testing.TB) string {
	tb.Helper()
	select {
	case key := <-t.called:
		return key
	case <-time.After(time.Second):
		tb.Fatal("timed out waiting for optimizer trigger")
		return ""
	}
}

func (t *recordingOptimizerTrigger) callsSnapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	calls := make([]string, len(t.calls))
	copy(calls, t.calls)
	return calls
}
```

Add this test:

```go
func TestFileHandler_OptimizedImageMissTriggersOptimization(t *testing.T) {
	cfg := optimizedTestConfig()
	logger := config.NewLogger("error")
	source := newMockStorage()
	optimizedBase := newMockStorage()
	optimized := &openFileMockStorage{mockStorage: optimizedBase}
	trigger := newRecordingOptimizerTrigger(nil)
	handler := NewFileHandlerWithOptimizedStorageAndTrigger(source, optimized, trigger, cfg, logger)

	modTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	source.addFileWithMetadata("photo.jpg", []byte("source image"), modTime, "source-etag", "image/jpeg", nil)

	req := httptest.NewRequest(http.MethodGet, "/photo.jpg", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}
	if w.Body.String() != "source image" {
		t.Fatalf("Expected source body, got %q", w.Body.String())
	}
	if got := w.Header().Get(optimizedStatusHeader); got != "miss" {
		t.Fatalf("Expected optimized miss header, got %q", got)
	}
	if got := trigger.waitForCall(t); got != "photo.jpg" {
		t.Fatalf("Expected one trigger for photo.jpg, got %q", got)
	}
}
```

Add this test:

```go
func TestFileHandler_OptimizedImageTriggerFailureStillServesSource(t *testing.T) {
	cfg := optimizedTestConfig()
	logger := config.NewLogger("error")
	source := newMockStorage()
	optimizedBase := newMockStorage()
	optimized := &openFileMockStorage{mockStorage: optimizedBase}
	trigger := newRecordingOptimizerTrigger(errors.New("optimizer down"))
	handler := NewFileHandlerWithOptimizedStorageAndTrigger(source, optimized, trigger, cfg, logger)

	modTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	source.addFileWithMetadata("photo.jpg", []byte("source image"), modTime, "source-etag", "image/jpeg", nil)

	req := httptest.NewRequest(http.MethodGet, "/photo.jpg", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}
	if w.Body.String() != "source image" {
		t.Fatalf("Expected source body, got %q", w.Body.String())
	}
	if got := trigger.waitForCall(t); got != "photo.jpg" {
		t.Fatalf("Expected trigger call despite trigger failure, got %q", got)
	}
}
```

Add this table test:

```go
func TestFileHandler_OptimizedImageDoesNotTriggerWhenOptimizedServedOrSkipped(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		rangeHdr  string
		body      []byte
		ctype     string
		addOpt    bool
		wantState string
	}{
		{name: "hit", method: http.MethodGet, path: "/photo.jpg", body: []byte("source image"), ctype: "image/jpeg", addOpt: true, wantState: "hit"},
		{name: "range", method: http.MethodGet, path: "/photo.jpg", rangeHdr: "bytes=0-1", body: []byte("source image"), ctype: "image/jpeg", wantState: ""},
		{name: "head", method: http.MethodHead, path: "/photo.jpg", body: []byte("source image"), ctype: "image/jpeg", wantState: ""},
		{name: "metadata", method: http.MethodGet, path: "/photo.jpg?metadata=1", body: []byte("source image"), ctype: "image/jpeg", wantState: ""},
		{name: "non-image", method: http.MethodGet, path: "/document.pdf", body: []byte("source document"), ctype: "application/pdf", wantState: ""},
		{name: "below threshold", method: http.MethodGet, path: "/small.jpg", body: []byte("small"), ctype: "image/jpeg", wantState: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := optimizedTestConfig()
			cfg.OptimizedMinBytes = 10
			logger := config.NewLogger("error")
			source := newMockStorage()
			optimizedBase := newMockStorage()
			optimized := &openFileMockStorage{mockStorage: optimizedBase}
			trigger := newRecordingOptimizerTrigger(nil)
			handler := NewFileHandlerWithOptimizedStorageAndTrigger(source, optimized, trigger, cfg, logger)

			modTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
			key := strings.TrimPrefix(strings.Split(tt.path, "?")[0], "/")
			source.addFileWithMetadata(key, tt.body, modTime, "source-etag", tt.ctype, nil)
			if tt.addOpt {
				optimizedBase.addFileWithMetadata(key, []byte("optimized image"), modTime.Add(time.Minute), "optimized-etag", tt.ctype, map[string]string{
					optimizedSourceETagMetadata: "source-etag",
					optimizedProfileMetadata:    cfg.OptimizationProfile,
				})
			}

			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.rangeHdr != "" {
				req.Header.Set("Range", tt.rangeHdr)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if got := w.Header().Get(optimizedStatusHeader); got != tt.wantState {
				t.Fatalf("Expected optimized state %q, got %q", tt.wantState, got)
			}
			select {
			case got := <-trigger.called:
				t.Fatalf("Expected no trigger calls, got %q", got)
			default:
			}
		})
	}
}
```

- [ ] **Step 3: Run handler tests and verify they fail before implementation**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
go test ./internal/handler
```

Expected before implementation: FAIL with `undefined: NewFileHandlerWithOptimizedStorageAndTrigger` or missing trigger calls.

- [ ] **Step 4: Implement non-blocking trigger on fallback**

In `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/handler.go`, after this line:

```go
w.Header().Set(optimizedStatusHeader, status)
```

and before the source fallback path, add:

```go
if optimizedFile == nil {
	h.triggerOptimization(sourceInfo, status)
}
```

Add this helper near `openTrustedOptimized`:

```go
func (h *FileHandler) triggerOptimization(source *interfaces.FileInfo, status string) {
	if h.optimizerTrigger == nil || source == nil {
		return
	}
	switch status {
	case "miss", "stale", "profile-mismatch":
	default:
		return
	}

	key := strings.TrimPrefix(source.Path, "/")
	go func() {
		if err := h.optimizerTrigger.Trigger(context.Background(), key); err != nil {
			h.logger.Warn("Optimizer trigger failed", "path", source.Path, "status", status, "error", err)
		} else {
			h.logger.Debug("Optimizer trigger queued", "path", source.Path, "status", status)
		}
	}()
}
```

If the repository logger does not accept variadic key-value arguments for `Warn`, use the existing map style instead:

```go
h.logger.Warn("Optimizer trigger failed", map[string]interface{}{
	"path":   source.Path,
	"status": status,
	"error":  err.Error(),
})
```

- [ ] **Step 5: Run handler tests**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
gofmt -w internal/handler/handler.go internal/handler/handler_test.go
go test ./internal/handler
```

Expected: PASS for `s3-static/internal/handler`.

- [ ] **Step 6: Run full s3-static tests**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
go test ./...
```

Expected: all s3-static packages pass. The root package may take about 70-90 seconds because existing integration-style tests are slow.

- [ ] **Step 7: Commit s3-static fallback trigger**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "feat: trigger image optimization on fallback"
```

Expected: commit created with handler and handler tests.

---

## Task 5: Update my-services Compose Wiring

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services/nodes/node-local/minio/compose.yaml`

- [ ] **Step 1: Update s3static environment**

In `/Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services/nodes/node-local/minio/compose.yaml`, add these entries under `s3static.environment`:

```yaml
      - OPTIMIZER_TRIGGER_URL=http://s3-image-optimizer:8080/optimize
      - OPTIMIZER_TRIGGER_TIMEOUT=2s
```

- [ ] **Step 2: Update s3-image-optimizer environment**

In the same file, replace the scan-oriented settings under `s3-image-optimizer.environment` with:

```yaml
      - SCAN_ENABLED=false
      - SCAN_INTERVAL=24h
      - TRIGGER_QUEUE_SIZE=64
      - SCAN_RETRY_ATTEMPTS=3
      - SCAN_RETRY_INITIAL_DELAY=5s
      - SCAN_RETRY_MAX_DELAY=1m
      - RUN_ONCE=false
```

Keep these existing resource controls:

```yaml
    deploy:
      resources:
        limits:
          memory: 1G
        reservations:
          memory: 512M
```

Keep `GOMEMLIMIT=900MiB`.

- [ ] **Step 3: Validate Compose rendering**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services/nodes/node-local/minio
docker compose --env-file deploy.env config >/tmp/node-local-minio.compose.yaml
rg -n "OPTIMIZER_TRIGGER_URL|SCAN_ENABLED|TRIGGER_QUEUE_SIZE|s3-image-optimizer" /tmp/node-local-minio.compose.yaml
```

Expected: rendered Compose contains:

```text
OPTIMIZER_TRIGGER_URL=http://s3-image-optimizer:8080/optimize
SCAN_ENABLED=false
TRIGGER_QUEUE_SIZE=64
s3-image-optimizer
```

- [ ] **Step 4: Commit my-services Compose wiring**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services
git add nodes/node-local/minio/compose.yaml
git commit -m "feat: wire on-demand s3 image optimization"
```

Expected: commit created with only `nodes/node-local/minio/compose.yaml`.

---

## Task 6: Integration Verification and Publish

**Files:**
- Verify only unless a command exposes a concrete failure.

- [ ] **Step 1: Run final optimizer verification**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
go test ./...
git diff --check
git status -sb
```

Expected: tests pass, `git diff --check` exits 0, status is clean or ahead of origin only by the new commits.

- [ ] **Step 2: Run final s3-static verification**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
go test ./...
git diff --check
git status -sb
```

Expected: tests pass, `git diff --check` exits 0, status is clean or ahead of origin only by the new commits.

- [ ] **Step 3: Run final my-services verification**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services
docker compose --env-file nodes/node-local/minio/deploy.env -f nodes/node-local/minio/compose.yaml config >/tmp/node-local-minio.compose.yaml
git diff --check
git status -sb
```

Expected: Compose renders, `git diff --check` exits 0, status is clean or ahead of origin only by the new commit.

- [ ] **Step 4: Push source repositories**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
git push origin main

cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
git push origin main

cd /Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services
git pull --rebase --autostash origin main
git push origin main
```

Expected: all pushes succeed. If `my-services` rebase conflicts in `deploy.env`, preserve the remote digest metadata and keep the new Compose env settings.

- [ ] **Step 5: Check GitHub Actions dispatch chain**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
gh run list --limit 3 --json databaseId,displayTitle,status,conclusion,url,workflowName,createdAt

cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
gh run list --limit 3 --json databaseId,displayTitle,status,conclusion,url,workflowName,createdAt

cd /Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services
gh run list --limit 5 --json databaseId,displayTitle,status,conclusion,url,workflowName,createdAt
```

Expected: source image build workflows start for optimizer and s3-static. The my-services `Update Image Ref` workflow should run after source dispatch, and `GitOps Sync` should run after my-services image-ref commits.

- [ ] **Step 6: Verify live behavior after deploy completes**

Run on the Docker host to choose one existing large JPEG or PNG key:

```bash
KNOWN_KEY="$(
  docker run --rm --network gitops_external_xiaoken --entrypoint /bin/sh minio/mc:RELEASE.2024-09-16T17-43-14Z \
    -c 'mc alias set local http://minio:9000 zhaochunqi FbzEgJswBP3iK2 >/dev/null && mc find local/logseq-assets --larger 512KiB' \
  | grep -Ei '\\.(jpe?g|png)$' \
  | sed 's#^local/logseq-assets/##' \
  | head -n 1
)"
test -n "$KNOWN_KEY"
ENCODED_KEY="$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1]))' "$KNOWN_KEY")"
echo "$KNOWN_KEY"
```

If the command prints no key, upload or identify one source JPEG or PNG larger than 512 KiB in `logseq-assets` before running the live curl verification.

Run from any machine that can reach the public asset endpoint:

```bash
curl -I "https://assets.logseq.zhaochunqi.com/logseq-assets/${ENCODED_KEY}"
```

Expected on first request when optimized object is missing:

```text
HTTP/2 200
x-s3-static-optimized: miss
```

Then check optimizer logs on the Docker host:

```bash
docker logs --tail=100 gitops-minio-s3-image-optimizer-1
```

Expected logs include:

```text
on-demand optimize started key=
on-demand optimize completed key=
```

The key text after `key=` must match the value printed by `echo "$KNOWN_KEY"`.

Run the same curl again after optimizer completion:

```bash
curl -I "https://assets.logseq.zhaochunqi.com/logseq-assets/${ENCODED_KEY}"
```

Expected:

```text
HTTP/2 200
x-s3-static-optimized: hit
```

---

## Self-Review Notes

- Spec coverage: the plan disables default scans, adds optimizer on-demand queueing, makes s3-static trigger optimization without changing public URLs, and wires the sidecar deployment.
- Type consistency: `OptimizerTrigger`, `NewHTTPOptimizerTrigger`, `NewFileHandlerWithOptimizedStorageAndTrigger`, `ProcessKey`, `SCAN_ENABLED`, `TRIGGER_QUEUE_SIZE`, `OPTIMIZER_TRIGGER_URL`, and `OPTIMIZER_TRIGGER_TIMEOUT` are introduced before use.
- Verification: each repo has package-level tests, full tests, format/check commands, commit steps, push steps, and deployment observation steps.
