# S3 Image Optimizer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a standalone worker that reads images from a source S3-compatible bucket, writes optimized same-key copies to a separate bucket, and attaches metadata that `s3-static` can trust.

**Architecture:** The worker is a non-serving process: it scans source objects, skips unsupported/current/small objects, optimizes JPEG/PNG images, and writes optimized objects to another bucket. Freshness is determined only by source ETag plus optimization profile metadata; public URL behavior remains owned by `s3-static`.

**Tech Stack:** Go 1.25, AWS SDK for Go v2, MinIO/S3-compatible storage, standard `image/jpeg` and `image/png`, `golang.org/x/image/draw`, Docker, GitHub Actions/GHCR.

---

## Implementation Status

- [x] Task 1: Bootstrap Go repo.
- [x] Task 2: Add config.
- [x] Task 3: Add image optimizer.
- [x] Task 4: Add S3 storage adapter.
- [x] Task 5: Add worker orchestration.
- [x] Task 6: Wire entrypoint and health endpoint.
- [x] Task 7: Add Docker, compose, CI, and docs.

## Boundary

This repo owns:
- Source bucket scanning.
- JPEG/PNG decode, resize, and re-encode.
- Optimized bucket writes.
- Metadata contract used by `s3-static`.
- Worker container image.

This repo does not own:
- Public asset serving.
- Domain/path routing.
- `s3-static` handler behavior.
- `my-services` deployment wiring.

## Metadata Contract

For a source object:

```text
bucket: logseq-assets
key: notes/photo.jpg
etag: abc123
```

the worker writes:

```text
bucket: logseq-assets-optimized
key: notes/photo.jpg
x-amz-meta-source-etag: abc123
x-amz-meta-optimization-profile: v1-jpeg82-png-best-w1920
```

The optimized bucket key is exactly the source key. If the source object changes and its ETag changes, `s3-static` rejects the stale optimized object until this worker rewrites it.

## File Structure

Create:
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/go.mod` - module definition.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/go.sum` - module checksums.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/cmd/s3-image-optimizer/main.go` - process entrypoint, health server, scan loop, signal shutdown.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/config/config.go` - environment config and validation.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/config/config_test.go` - config tests.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/imageopt/optimizer.go` - same-format JPEG/PNG optimization.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/imageopt/optimizer_test.go` - generated-image tests.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/storage/s3.go` - S3 list/head/get/put adapter.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/storage/s3_test.go` - MinIO-backed storage tests.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker.go` - scan and per-object orchestration.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker_test.go` - fake-store worker tests.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/Dockerfile` - runtime image.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/compose.yaml` - local MinIO smoke stack.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/.github/workflows/docker-build.yml` - GHCR image workflow.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/README.md` - usage and metadata contract.
- `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/justfile` - local commands.

## Task 1: Bootstrap Go Repo

**Files:**
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/go.mod`
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/cmd/s3-image-optimizer/main.go`
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/justfile`
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/README.md`

- [ ] **Step 1: Initialize module**

Run:

```bash
cd /Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer
go mod init github.com/xiaolutech/s3-image-optimizer
go get github.com/aws/aws-sdk-go-v2/config
go get github.com/aws/aws-sdk-go-v2/credentials
go get github.com/aws/aws-sdk-go-v2/service/s3
go get golang.org/x/image/draw
go get github.com/testcontainers/testcontainers-go
```

Expected: `go.mod` and `go.sum` exist.

- [ ] **Step 2: Add minimal main**

Create `cmd/s3-image-optimizer/main.go`:

```go
package main

import "fmt"

func main() {
	fmt.Println("s3-image-optimizer")
}
```

- [ ] **Step 3: Add justfile**

Create `justfile`:

```make
default:
    @just --list

fmt:
    gofmt -w cmd internal

test:
    go test ./...

vet:
    go vet ./...

build:
    go build -o s3-image-optimizer ./cmd/s3-image-optimizer

validate: fmt test vet build
```

- [ ] **Step 4: Add README skeleton**

Create `README.md`:

```markdown
# s3-image-optimizer

Standalone worker that scans an S3-compatible source bucket, writes optimized image copies to a separate bucket, and tags each optimized object with source metadata that `s3-static` can verify.

Public URL serving is intentionally out of scope. `s3-static` remains the public gateway.
```

- [ ] **Step 5: Verify bootstrap**

Run:

```bash
go test ./...
go build ./cmd/s3-image-optimizer
```

Expected: both commands pass.

- [ ] **Step 6: Commit**

Run:

```bash
git add go.mod go.sum cmd/s3-image-optimizer/main.go justfile README.md docs/superpowers/plans/2026-07-01-s3-image-optimizer.md
git commit -m "chore: bootstrap s3 image optimizer"
```

## Task 2: Add Config

**Files:**
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/config/config.go`
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/config/config_test.go`

- [ ] **Step 1: Write failing config tests**

Create tests that assert:
- defaults: profile `v1-jpeg82-png-best-w1920`, max width `1920`, JPEG quality `82`, min bytes `524288`, scan interval `10m`.
- env loading for `S3_ENDPOINT`, `S3_REGION`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `S3_USE_SSL`, `SOURCE_BUCKET`, `OPTIMIZED_BUCKET`, `OPTIMIZATION_PROFILE`, `MAX_WIDTH`, `JPEG_QUALITY`, `MIN_BYTES`, `SCAN_INTERVAL`, `RUN_ONCE`.
- validation requires endpoint, credentials, source bucket, optimized bucket, non-empty profile, positive max width, JPEG quality `1..100`, non-negative min bytes, positive scan interval.

Run:

```bash
go test ./internal/config -count=1
```

Expected before implementation: FAIL with undefined `DefaultConfig` / `Load`.

- [ ] **Step 2: Implement config**

Create:

```go
type Config struct {
	Port string
	S3Endpoint string
	S3Region string
	S3AccessKeyID string
	S3SecretAccessKey string
	S3UseSSL bool
	SourceBucket string
	OptimizedBucket string
	OptimizationProfile string
	MaxWidth int
	JPEGQuality int
	MinBytes int64
	ScanInterval time.Duration
	RunOnce bool
}
```

Functions:
- `DefaultConfig() *Config`
- `Load() (*Config, error)`
- `(c *Config) Validate() error`

- [ ] **Step 3: Verify and commit**

Run:

```bash
go test ./internal/config -count=1
git add internal/config/config.go internal/config/config_test.go docs/superpowers/plans/2026-07-01-s3-image-optimizer.md
git commit -m "feat: add optimizer configuration"
```

Expected: config tests pass.

## Task 3: Add Same-Format Image Optimizer

**Files:**
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/imageopt/optimizer.go`
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/imageopt/optimizer_test.go`

- [ ] **Step 1: Write failing image tests**

Tests:
- generated `3000x1200` JPEG with quality 95 optimizes to JPEG and width <= `1920`.
- generated `2400x1000` PNG optimizes to PNG and width <= `1920`.
- `image/gif` returns `Skipped=true`, `Reason="unsupported_content_type"`.
- small image with output not at least 5 percent smaller returns `Skipped=true`, `Reason="insufficient_savings"`.

Run:

```bash
go test ./internal/imageopt -count=1
```

Expected before implementation: FAIL with undefined `Optimize`.

- [ ] **Step 2: Implement optimizer**

Create:

```go
type Options struct {
	MaxWidth int
	JPEGQuality int
	MinSavings float64
}

type Result struct {
	Body []byte
	ContentType string
	Width int
	Height int
	Skipped bool
	Reason string
}

func Optimize(body []byte, contentType string, opts Options) (Result, error)
```

Rules:
- Support `image/jpeg` and `image/png`.
- Decode image dimensions.
- If width > max width, resize preserving aspect ratio with `draw.CatmullRom`.
- JPEG output uses configured quality.
- PNG output uses `png.BestCompression`.
- If `len(output) >= len(input) * (1 - MinSavings)`, skip with `insufficient_savings`.

- [ ] **Step 3: Verify and commit**

Run:

```bash
go test ./internal/imageopt -count=1
git add internal/imageopt/optimizer.go internal/imageopt/optimizer_test.go docs/superpowers/plans/2026-07-01-s3-image-optimizer.md
git commit -m "feat: add same-format image optimizer"
```

Expected: image tests pass.

## Task 4: Add S3 Storage Adapter

**Files:**
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/storage/s3.go`
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/storage/s3_test.go`

- [ ] **Step 1: Write MinIO-backed tests**

Tests start MinIO with testcontainers, create source/optimized buckets, and verify:
- `ListObjects` visits a source key.
- `HeadObject` returns key, size, ETag, content type, and lower-case metadata.
- `GetObject` returns bytes and object info.
- `PutObject` writes metadata that can be read by `HeadObject`.
- `IsNotFound` is true for missing keys.

Run:

```bash
go test ./internal/storage -count=1
```

Expected before implementation: FAIL with undefined `New`.

- [ ] **Step 2: Implement adapter**

Create:

```go
type ObjectInfo struct {
	Key string
	Size int64
	ETag string
	ContentType string
	Metadata map[string]string
}

type PutOptions struct {
	ContentType string
	Metadata map[string]string
}

type Client struct {
	client *s3.Client
}
```

Functions:
- `New(cfg *config.Config) (*Client, error)`
- `HeadObject(ctx context.Context, bucket, key string) (*ObjectInfo, error)`
- `GetObject(ctx context.Context, bucket, key string) ([]byte, *ObjectInfo, error)`
- `PutObject(ctx context.Context, bucket, key string, body []byte, opts PutOptions) error`
- `ListObjects(ctx context.Context, bucket, prefix string, visit func(ObjectInfo) error) error`
- `IsNotFound(err error) bool`

- [ ] **Step 3: Verify and commit**

Run:

```bash
go test ./internal/storage -count=1
git add internal/storage/s3.go internal/storage/s3_test.go docs/superpowers/plans/2026-07-01-s3-image-optimizer.md
git commit -m "feat: add s3 storage adapter"
```

Expected: storage tests pass.

## Task 5: Add Worker Orchestration

**Files:**
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker.go`
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/internal/worker/worker_test.go`

- [ ] **Step 1: Write fake-store worker tests**

Tests:
- missing optimized object causes source get, optimization, and optimized put.
- current optimized object with matching metadata is skipped.
- stale optimized object with mismatched `source-etag` is rewritten.
- unsupported content type writes a skip marker under `.s3-image-optimizer/skips/<escaped-key>.json`.
- source object smaller than `MinBytes` is skipped without source get.
- optimizer `insufficient_savings` writes a skip marker.

Run:

```bash
go test ./internal/worker -count=1
```

Expected before implementation: FAIL with undefined `Worker`.

- [ ] **Step 2: Implement worker**

Create:

```go
type Store interface {
	HeadObject(ctx context.Context, bucket, key string) (*storage.ObjectInfo, error)
	GetObject(ctx context.Context, bucket, key string) ([]byte, *storage.ObjectInfo, error)
	PutObject(ctx context.Context, bucket, key string, body []byte, opts storage.PutOptions) error
	ListObjects(ctx context.Context, bucket, prefix string, visit func(storage.ObjectInfo) error) error
}

type Config struct {
	SourceBucket string
	OptimizedBucket string
	OptimizationProfile string
	MaxWidth int
	JPEGQuality int
	MinBytes int64
}
```

Functions:
- `New(store Store, cfg Config) *Worker`
- `(w *Worker) RunOnce(ctx context.Context) error`
- `(w *Worker) ProcessObject(ctx context.Context, source storage.ObjectInfo) error`

Optimized metadata:

```go
map[string]string{
	"source-etag": source.ETag,
	"optimization-profile": cfg.OptimizationProfile,
}
```

Skip marker JSON:

```json
{"source_key":"notes/photo.jpg","source_etag":"abc123","profile":"v1-jpeg82-png-best-w1920","reason":"insufficient_savings"}
```

- [ ] **Step 3: Verify and commit**

Run:

```bash
go test ./internal/worker -count=1
git add internal/worker/worker.go internal/worker/worker_test.go docs/superpowers/plans/2026-07-01-s3-image-optimizer.md
git commit -m "feat: add bucket optimization worker"
```

Expected: worker tests pass.

## Task 6: Wire Entrypoint And Health Endpoint

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/cmd/s3-image-optimizer/main.go`

- [ ] **Step 1: Replace minimal main**

Main must:
- load config.
- create storage client.
- create worker.
- start `/health` on `PORT`.
- run once and exit when `RUN_ONCE=true`.
- otherwise run immediately and then every `SCAN_INTERVAL`.
- stop on SIGINT/SIGTERM.

- [ ] **Step 2: Verify and commit**

Run:

```bash
go build ./cmd/s3-image-optimizer
go test ./...
git add cmd/s3-image-optimizer/main.go docs/superpowers/plans/2026-07-01-s3-image-optimizer.md
git commit -m "feat: wire optimizer entrypoint"
```

Expected: build and tests pass.

## Task 7: Add Docker, Compose, CI, And Docs

**Files:**
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/Dockerfile`
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/compose.yaml`
- Create: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/.github/workflows/docker-build.yml`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-image-optimizer/README.md`

- [ ] **Step 1: Add Dockerfile**

Use a multi-stage Go 1.25 Alpine build. Runtime image runs as UID 1001, exposes `8080`, and healthchecks `http://localhost:8080/health`.

- [ ] **Step 2: Add compose**

Compose services:
- `minio`
- `minio-setup`, creating `source-assets` and `source-assets-optimized`
- `s3-image-optimizer`, configured with endpoint `minio:9000`, source bucket `source-assets`, optimized bucket `source-assets-optimized`, `RUN_ONCE=false`

- [ ] **Step 3: Add GHCR workflow**

Workflow builds on `main`, PRs, and tags. It publishes `ghcr.io/xiaolutech/s3-image-optimizer` on non-PR events using Docker buildx.

- [ ] **Step 4: Expand README**

Document:
- env vars.
- source/optimized bucket contract.
- relationship with `s3-static`.
- local commands.
- Docker/compose usage.

- [ ] **Step 5: Verify and commit**

Run:

```bash
go test ./...
go vet ./...
docker build -t s3-image-optimizer:local .
docker compose config >/tmp/s3-image-optimizer.compose.yaml
git diff --check
git add Dockerfile compose.yaml .github/workflows/docker-build.yml README.md docs/superpowers/plans/2026-07-01-s3-image-optimizer.md
git commit -m "chore: add optimizer packaging and docs"
```

Expected: all commands pass.

## Final Verification

Run:

```bash
gofmt -w cmd internal
go test ./...
go vet ./...
go build ./cmd/s3-image-optimizer
docker build -t s3-image-optimizer:local .
docker compose config >/tmp/s3-image-optimizer.compose.yaml
git diff --check
```

Expected: all commands pass.

## Self-Review Notes

- Spec coverage: This plan builds the external worker only; `s3-static` serving and `my-services` deployment are out of scope.
- Placeholder scan: Every task names exact files, behavior, commands, and expected outcomes.
- Type consistency: Config names, metadata names, and optimization profile match the `s3-static` contract.
