# Bounded Resident Scan Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep `s3-image-optimizer` resident while preventing startup or periodic full-bucket scans from exhausting Docker Desktop.

**Architecture:** Add a process-local scan cursor and a per-round object-count limit. Each resident scan round lists at most `SCAN_BATCH_SIZE` source objects after the in-memory cursor, processes that page, advances the cursor to the last listed key, and sleeps for `SCAN_INTERVAL`; reaching the bucket end clears the cursor so later rounds wrap from the beginning.

**Tech Stack:** Go, AWS S3 ListObjectsV2, MinIO-compatible S3, Docker Compose.

---

### Task 1: Add Bounded Scan API

**Files:**
- Modify: `internal/storage/s3.go`
- Modify: `internal/worker/worker.go`
- Test: `internal/worker/worker_test.go`

- [ ] **Step 1: Write failing tests**

Add tests that call a new worker scan-round method twice with `ScanBatchSize=2`. The first call should optimize `a.jpg` and `b.jpg`, advance the cursor to `b.jpg`, and report that more objects remain. The second call should optimize `c.jpg`, clear the cursor at bucket end, and report that no more objects remain.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worker -run TestWorkerRunScanRound`

Expected: FAIL because the bounded scan API does not exist yet.

- [ ] **Step 3: Implement minimal API**

Add `ListObjectsPage(ctx, bucket, prefix, startAfter string, maxKeys int32)`, `ScanBatchSize`, and worker in-memory cursor state. Keep `RunOnce()` as full scan behavior for manual one-shot compatibility.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/worker ./internal/storage`

Expected: PASS.

### Task 2: Wire Resident Loop Configuration

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`
- Modify: `cmd/s3-image-optimizer/main.go`
- Modify: `README.md`

- [ ] **Step 1: Write failing config tests**

Add `SCAN_BATCH_SIZE` with default `100`, env loading, and validation that values below `1` fail.

- [ ] **Step 2: Run config tests**

Run: `go test ./internal/config`

Expected: FAIL before implementation.

- [ ] **Step 3: Implement config and loop wiring**

Pass `ScanBatchSize` into worker config. Change resident `runLoop` to call the bounded scan-round method instead of full `RunOnce()`. Leave `RUN_ONCE=true` using full `RunOnce()`.

- [ ] **Step 4: Run package tests**

Run: `go test ./...`

Expected: PASS.

### Task 3: Remove Access-Path Optimizer Trigger

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/cmd/s3-static/main.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/config/config.go`
- Modify: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/handler.go`
- Delete: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimizer_trigger.go`
- Delete: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/optimizer_trigger_test.go`
- Test: `/Users/zhaochunqi/ghq/github.com/xiaolutech/s3-static/internal/handler/handler_test.go`

- [ ] **Step 1: Remove trigger config and constructor**

Delete `OPTIMIZER_TRIGGER_URL` and `OPTIMIZER_TRIGGER_TIMEOUT` config fields and validation. Construct the handler with optimized storage only.

- [ ] **Step 2: Remove trigger calls**

Remove `optimizerTrigger` from `FileHandler` and remove calls after optimized miss/stale/profile mismatch. Keep optimized bucket fallback intact.

- [ ] **Step 3: Run tests**

Run: `go test ./...`

Expected: PASS.

### Task 4: Update Deployment

**Files:**
- Modify: `/Users/zhaochunqi/ghq/github.com/zhaochunqi/my-services/nodes/node-local/minio/compose.yaml`

- [ ] **Step 1: Configure resident bounded scan**

Set `SCAN_ENABLED=true`, `SCAN_INTERVAL=30s`, `SCAN_BATCH_SIZE=100`, and `RUN_ONCE=false` for `s3-image-optimizer`. Remove `OPTIMIZER_TRIGGER_URL` and `OPTIMIZER_TRIGGER_TIMEOUT` from `s3static`.

- [ ] **Step 2: Validate compose**

Run: `docker compose --env-file deploy.env config >/tmp/node-local-minio.compose.yaml`

Expected: command exits 0.

### Task 5: Final Verification

**Files:**
- All modified files

- [ ] **Step 1: Run repository tests**

Run in `s3-image-optimizer`: `go test ./...`

Run in `s3-static`: `go test ./...`

- [ ] **Step 2: Check whitespace**

Run in each modified repo: `git diff --check`

- [ ] **Step 3: Review git state**

Run: `git status -sb` in all three repos and inspect diffs before committing.
