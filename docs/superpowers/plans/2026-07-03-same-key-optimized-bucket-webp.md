# Same-Key Optimized Bucket WebP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the source bucket read-only while writing exactly one optimized object per source image into the optimized bucket at the same key, with WebP bytes and `Content-Type: image/webp`.

**Architecture:** `s3-image-optimizer` continues to scan and read only `SOURCE_BUCKET`, but its optimized output key becomes the source key instead of a `.webp` sidecar key. `s3-static` continues to own public serving and trust checks, but resolves optimized variants by opening the same key in `OPTIMIZED_BUCKET`; `Accept` negotiation and metadata checks remain strict. `my-services` rolls both services to a new profile so stale sidecar objects are ignored and same-key WebP objects are generated.

**Tech Stack:** Go, AWS SDK v2, MinIO/S3-compatible object storage, Docker Compose, GitHub Actions image-ref deployment.

---

## File Structure

- Modify `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker.go`
  - Change optimized object key selection from extension replacement to same-key.
  - Keep source bucket operations read-only: `ListObjects`, `HeadObject`, and `GetObject` only.
  - Keep writes limited to `OPTIMIZED_BUCKET`.
- Modify `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker_test.go`
  - Update the optimized object contract vector and worker expectations to same-key.
  - Add an explicit regression test that `PutObject` is never called with `SOURCE_BUCKET`.
- Modify `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/README.md`
  - Document the same-key optimized bucket contract.
- Modify `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimized_variant.go`
  - Change optimized resolver key selection from `photo.webp`/`photo.avif` to source key.
- Modify `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimized_variant_test.go`
  - Update resolver contract tests to same-key.
- Modify `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/README.md`
  - Document that optimized bucket keys mirror source keys exactly while content type may differ.
- Modify `/Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services/nodes/node-local/minio/compose.yaml`
  - Update `OPTIMIZATION_PROFILE` for both services to a new same-key profile, for example `v7-webp-q82-same-key`.

## Contract

Source bucket remains the fact source and is read-only to the optimizer:

```text
bucket: logseq-assets
key: 20220530/photo.jpeg
content-type: image/jpeg
```

Optimized bucket keeps the same key and stores the optimized encoding:

```text
bucket: logseq-assets-optimized
key: 20220530/photo.jpeg
content-type: image/webp
x-amz-meta-source-key: 20220530/photo.jpeg
x-amz-meta-source-etag: <source etag>
x-amz-meta-optimization-profile: v7-webp-q82-same-key
x-amz-meta-source-content-type: image/jpeg
x-amz-meta-variant-format: webp
```

Public URL remains the source path. On a supported `GET` request with `Accept: image/webp`, `s3-static` serves the optimized object and returns:

```text
Content-Type: image/webp
Vary: Accept
X-S3-Static-Optimized: hit; format=webp
```

`HEAD`, `Range`, metadata requests, unsupported `Accept`, unsupported source content types, missing/stale optimized objects, and metadata mismatches continue to fall back to the source object.

---

### Task 1: Update Optimizer Key Contract

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker_test.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker.go`

- [ ] **Step 1: Write the failing same-key contract tests**

Edit `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker_test.go`.

Change `TestWorkerProcessesMissingOptimizedObject` so it expects the optimized object at the source key:

```go
written := store.objects[objKey("optimized", "notes/photo.jpg")]
if len(written.body) == 0 {
	t.Fatal("expected optimized object to be written at the source key")
}
if written.info.ContentType != "image/webp" {
	t.Fatalf("expected webp content type, got %q", written.info.ContentType)
}
if written.info.Metadata["source-key"] != "notes/photo.jpg" {
	t.Fatalf("expected source-key metadata, got %#v", written.info.Metadata)
}
```

Change `TestWorkerSkipsCurrentOptimizedObject` so the existing optimized object is keyed by `notes/photo.jpg`:

```go
store.objects[objKey("optimized", "notes/photo.jpg")] = fakeObject{info: storage.ObjectInfo{
	Key:         "notes/photo.jpg",
	Size:        100,
	ETag:        "optimized-etag",
	ContentType: "image/webp",
	Metadata: map[string]string{
		"source-etag":          "source-etag",
		"optimization-profile": "v6-webp-q82-original",
		"source-key":           source.Key,
		"source-content-type":  source.ContentType,
		"variant-format":       "webp",
	},
}}
```

Replace `TestOptimizedObjectContractVector` expectations with:

```go
const sourceKey = "notes/photo.png"
const expectedAVIFKey = "notes/photo.png"
const expectedWebPKey = "notes/photo.png"
```

Add this regression test near the other worker process tests:

```go
func TestWorkerNeverWritesToSourceBucket(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	source := storage.ObjectInfo{
		Key:         "notes/photo.jpg",
		Size:        int64(len(body)),
		ETag:        "source-etag",
		ContentType: "image/jpeg",
	}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: body}

	w := New(store, testWorkerConfig())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	for _, key := range store.putKeys {
		if strings.HasPrefix(key, "source/") {
			t.Fatalf("optimizer wrote to source bucket: %#v", store.putKeys)
		}
	}
	if _, ok := store.objects[objKey("optimized", source.Key)]; !ok {
		t.Fatalf("expected optimized object at same key %q", source.Key)
	}
}
```

- [ ] **Step 2: Run optimizer worker tests and verify they fail**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
go test ./internal/worker
```

Expected: FAIL. The failures should mention missing optimized objects at `notes/photo.jpg` or unexpected optimized key values, because the implementation still writes `notes/photo.webp`.

- [ ] **Step 3: Implement same-key optimized output**

Edit `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker.go`.

Remove unused imports `path` and `strings` from the import block.

Replace `optimizedVariantKey` with:

```go
func optimizedVariantKey(sourceKey, format string) string {
	return sourceKey
}
```

Keep the `format` parameter even though it is unused. The parameter preserves the caller shape and makes the function contract read as "this key is for a specific encoded variant, but same-key storage is used."

- [ ] **Step 4: Run optimizer tests and fix remaining key expectations**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
go test ./internal/worker
```

Expected: PASS after all tests that previously referenced `notes/photo.webp`, `notes/photo.avif`, `a.webp`, `b.webp`, or `c.webp` are updated to same-key expectations such as `notes/photo.jpg`, `a.jpg`, `b.jpg`, and `c.jpg`.

When updating assertions, keep content type and metadata expectations unchanged. For example, a WebP optimized object at key `a.jpg` must still have:

```go
ContentType: "image/webp",
Metadata: map[string]string{
	"source-key":           "a.jpg",
	"source-etag":          "a",
	"optimization-profile": "v6-webp-q82-original",
	"source-content-type":  "image/jpeg",
	"variant-format":       "webp",
},
```

- [ ] **Step 5: Run full optimizer test suite**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit optimizer contract change**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
git add internal/worker/worker.go internal/worker/worker_test.go
git commit -m "feat: write optimized images at source keys"
```

---

### Task 2: Update s3-static Same-Key Resolver

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimized_variant_test.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimized_variant.go`

- [ ] **Step 1: Write failing resolver contract tests**

Edit `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimized_variant_test.go`.

Change `TestOptimizedObjectContractVector` expectations to:

```go
const sourceKey = "notes/photo.png"
const expectedAVIFKey = "notes/photo.png"
const expectedWebPKey = "notes/photo.png"
```

In `TestOptimizedVariantResolverReturnsTrustedWebPByDefault`, keep:

```go
key := optimizedVariantKey(source.Path, optimizedVariantWebP)
```

and add this assertion after key assignment:

```go
if key != source.Path {
	t.Fatalf("expected optimized WebP key to mirror source key, got %q", key)
}
```

In `TestOptimizedVariantResolverReturnsTrustedAVIFWhenEnabled`, add:

```go
if optimizedVariantKey(source.Path, optimizedVariantAVIF) != source.Path {
	t.Fatalf("expected optimized AVIF key to mirror source key")
}
```

- [ ] **Step 2: Run resolver tests and verify they fail**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
go test ./internal/handler -run 'TestOptimizedVariantResolver|TestOptimizedObjectContractVector'
```

Expected: FAIL. The failures should show that `optimizedVariantKey("photo.png", "webp")` still returns `photo.webp`.

- [ ] **Step 3: Implement same-key resolver**

Edit `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimized_variant.go`.

Remove unused imports `path` and `strings` only if no other code in the file uses them. Keep `strings` if `acceptsMediaType` or `canResolveSource` still uses it.

Replace `optimizedVariantKey` with:

```go
func optimizedVariantKey(sourceKey, format string) string {
	return sourceKey
}
```

Do not weaken `isTrustedVariant`. It must still require:

```go
optimized.ContentType == variant.ContentType
optimized.Metadata[optimizedSourceKeyMetadata] == source.Path
optimized.Metadata[optimizedSourceETagMetadata] == source.ETag
optimized.Metadata[optimizedProfileMetadata] == profile
optimized.Metadata[optimizedSourceContentTypeMetadata] == source.ContentType
optimized.Metadata[optimizedVariantFormatMetadata] == variant.Format
```

- [ ] **Step 4: Run handler tests**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
go test ./internal/handler
```

Expected: PASS.

- [ ] **Step 5: Run full s3-static tests**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit resolver change**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
git add internal/handler/optimized_variant.go internal/handler/optimized_variant_test.go
git commit -m "feat: resolve optimized images at source keys"
```

---

### Task 3: Update Documentation

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/README.md`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/README.md`

- [ ] **Step 1: Update optimizer README contract**

Edit `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/README.md`.

Replace the "Contract With s3-static" key mapping section with:

```markdown
By default, this worker writes WebP objects to the optimized bucket using the
same object key as the source bucket:

```text
notes/photo.jpg -> notes/photo.jpg
```

The optimized object body is WebP even though the key extension is inherited
from the source object. The object `Content-Type` is the source of truth:

```text
bucket: logseq-assets-optimized
key: notes/photo.jpg
content-type: image/webp
x-amz-meta-source-key: notes/photo.jpg
x-amz-meta-source-etag: abc123
x-amz-meta-optimization-profile: v7-webp-q82-same-key
x-amz-meta-source-content-type: image/jpeg
x-amz-meta-variant-format: webp
```

The source bucket is read-only to this worker. The worker lists, heads, and
reads source objects, then writes only to `OPTIMIZED_BUCKET`.
```

- [ ] **Step 2: Update s3-static README contract**

Edit `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/README.md`.

Update the optimized bucket section so its example uses the same source key:

```markdown
`s3-static` can optionally serve optimized WebP objects from a second bucket.
The optimized bucket must mirror the source key exactly, while the optimized
object's `Content-Type` records the actual encoded format.

```text
source bucket:    logseq-assets/notes/photo.jpg          image/jpeg
optimized bucket: logseq-assets-optimized/notes/photo.jpg image/webp
```

The response for a trusted optimized hit includes `Content-Type: image/webp`,
`Vary: Accept`, and `X-S3-Static-Optimized: hit; format=webp`.
```

- [ ] **Step 3: Run documentation-adjacent checks**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
rg -n "photo\\.webp|photo\\.avif|sidecar|replace the file extension" README.md internal/worker
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
rg -n "photo\\.webp|photo\\.avif|sidecar|replace the file extension" README.md internal/handler
```

Expected: no remaining live-contract text claiming the default optimized key is `photo.webp` or `photo.avif`. Historical docs under `docs/superpowers/` may still mention older plans and do not need to be rewritten.

- [ ] **Step 4: Commit documentation**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
git add README.md docs/superpowers/plans/2026-07-03-same-key-optimized-bucket-webp.md
git commit -m "docs: plan same-key optimized webp contract"

cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
git add README.md
git commit -m "docs: describe same-key optimized image serving"
```

---

### Task 4: Update Deployment Profile

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services/nodes/node-local/minio/compose.yaml`

- [ ] **Step 1: Update the profile in compose**

Edit `/Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services/nodes/node-local/minio/compose.yaml`.

Change both services to the new same-key profile:

```yaml
- OPTIMIZATION_PROFILE=v7-webp-q82-same-key
```

Keep these existing settings:

```yaml
- OPTIMIZED_IMAGE_ENABLED=true
- OPTIMIZED_BUCKET_NAME=logseq-assets-optimized
- SOURCE_BUCKET=logseq-assets
- OPTIMIZED_BUCKET=logseq-assets-optimized
- MAX_WIDTH=0
- WEBP_QUALITY=82
- AVIF_ENABLED=false
```

- [ ] **Step 2: Validate compose rendering**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services
docker compose --env-file nodes/node-local/minio/deploy.env -f nodes/node-local/minio/compose.yaml config >/tmp/minio-same-key-webp.compose.yaml
rg -n "OPTIMIZATION_PROFILE|OPTIMIZED_BUCKET_NAME|SOURCE_BUCKET|OPTIMIZED_BUCKET|WEBP_QUALITY|AVIF_ENABLED" /tmp/minio-same-key-webp.compose.yaml
```

Expected: rendered config contains `v7-webp-q82-same-key` for both `s3static` and `s3-image-optimizer`, `AVIF_ENABLED: "false"`, and `WEBP_QUALITY: "82"`.

- [ ] **Step 3: Commit deployment config**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services
git add nodes/node-local/minio/compose.yaml
git commit -m "config: use same-key webp optimized images"
```

---

### Task 5: Build, Push, Deploy, and Verify Runtime

**Files:**
- No source edits in this task.

- [ ] **Step 1: Push all three repos**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
git push origin main

cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
git push origin main

cd /Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services
git push origin main
```

Expected: pushes succeed. GitHub Actions should build new images for both application repos and then update `my-services` image refs.

- [ ] **Step 2: Watch image build workflows**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
gh run list --limit 3

cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static
gh run list --limit 3
```

Expected: the latest workflow for each pushed commit completes with `success`.

- [ ] **Step 3: Pull image-ref commits in my-services**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services
git pull --ff-only
git log -3 --oneline
```

Expected: top commits include generated image-ref updates for `s3-image-optimizer` and `s3-static`.

- [ ] **Step 4: Watch my-services GitOps**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services
gh run list --limit 5
```

Then watch the newest run for the image-ref or config commit:

```bash
gh run watch <run-id> --exit-status
```

Expected: GitOps completes with `success`.

- [ ] **Step 5: Verify optimized object writes at same key**

Run:

```bash
mc stat local-minio/logseq-assets-optimized/20220530/IMG_20220530_082425_803_1.jpg
```

Expected:

```text
Content-Type                   : image/webp
X-Amz-Meta-Source-Key          : 20220530/IMG_20220530_082425_803_1.jpg
X-Amz-Meta-Optimization-Profile: v7-webp-q82-same-key
X-Amz-Meta-Variant-Format      : webp
```

- [ ] **Step 6: Verify old sidecar is no longer used**

Run:

```bash
mc stat local-minio/logseq-assets-optimized/20220530/IMG_20220530_082425_803_1.webp
```

Expected: the object may still exist from the previous rollout, but new `s3-static` must not require it. Do not delete it in this task.

- [ ] **Step 7: Verify public GET serves WebP from same-key optimized object**

Run:

```bash
curl -sS -D - -o /dev/null \
  -H 'Accept: image/webp,image/*,*/*' \
  'https://assets.logseq.zhaochunqi.com/logseq-assets/20220530/IMG_20220530_082425_803_1.jpg' \
  | rg -i '^(HTTP/|content-type|content-length|vary|x-s3-static-optimized|etag|last-modified):'
```

Expected:

```text
content-type: image/webp
vary: Accept
x-s3-static-optimized: hit; format=webp
```

- [ ] **Step 8: Verify source bucket remains unchanged**

Run:

```bash
mc stat local-minio/logseq-assets/20220530/IMG_20220530_082425_803_1.jpg
```

Expected:

```text
Content-Type                   : image/jpeg
```

The source bucket object must not have WebP metadata such as `X-Amz-Meta-Variant-Format: webp`.

---

### Task 6: Optional Cleanup Plan for Old Sidecars

**Files:**
- No source edits in this task.

- [ ] **Step 1: Inventory old optimized sidecar objects**

Run:

```bash
mc find local-minio/logseq-assets-optimized --name "*.webp" | head -n 50
mc find local-minio/logseq-assets-optimized --name "*.avif" | head -n 50
```

Expected: this lists old sidecar objects from previous rollouts. This task only inventories them.

- [ ] **Step 2: Do not delete sidecars during the same rollout**

Record this operational note in the deployment issue or handoff:

```text
Old .webp/.avif sidecar objects are no longer part of the serving contract after v7-webp-q82-same-key, but they should be deleted only in a separate cleanup after same-key hits have been observed in production.
```

Expected: no deletion commands are run in this implementation plan.

---

## Self-Review

- Spec coverage: The plan preserves source bucket as read-only, keeps the optimized bucket, writes one optimized object per source key, serves WebP by `Content-Type`, and keeps strict metadata trust.
- Placeholder scan: No task uses unresolved placeholder markers, "similar to", or unspecified error handling.
- Type consistency: The same helper name `optimizedVariantKey(sourceKey, format string)` is used in both repos; metadata names remain `source-key`, `source-etag`, `optimization-profile`, `source-content-type`, and `variant-format`.
