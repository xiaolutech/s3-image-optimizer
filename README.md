# s3-image-optimizer

Standalone worker that scans an S3-compatible source bucket, writes optimized image copies to a separate bucket, and tags each optimized object with source metadata that `s3-static` can verify.

Public URL serving is intentionally out of scope. `s3-static` remains the public gateway and keeps the original domain and path.

## Contract With s3-static

This worker writes optimized objects to the same key in the optimized bucket. Every optimized object includes:

- `x-amz-meta-source-etag`
- `x-amz-meta-optimization-profile`

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
x-amz-meta-optimization-profile: v2-jpeg82-png-best-original-width
```

`s3-static` uses those metadata values to decide whether the optimized object is safe to serve. If the source ETag changes or the profile changes, `s3-static` falls back to the source object until this worker rewrites the optimized copy.

## Behavior

- Scans `SOURCE_BUCKET`.
- Supports JPEG and PNG.
- Keeps original image dimensions by default.
- Resizes images wider than `MAX_WIDTH` only when `MAX_WIDTH` is greater than `0`.
- Re-encodes JPEG with `JPEG_QUALITY`.
- Re-encodes PNG with best compression.
- Writes optimized objects to `OPTIMIZED_BUCKET` using the same key.
- Skips objects smaller than `MIN_BYTES`.
- Skips current optimized objects when metadata already matches.
- Writes skip markers to `.s3-image-optimizer/skips/<escaped-source-key>.json` for unsupported images or insufficient savings.

## Configuration

- `PORT` - Health endpoint port. Default: `8080`.
- `S3_ENDPOINT` - S3-compatible endpoint, for example `minio:9000`.
- `S3_REGION` - S3 region. Default: `us-east-1`.
- `S3_ACCESS_KEY_ID` - S3 access key.
- `S3_SECRET_ACCESS_KEY` - S3 secret key.
- `S3_USE_SSL` - Use HTTPS for S3. Default: `true`.
- `SOURCE_BUCKET` - Bucket containing original objects.
- `OPTIMIZED_BUCKET` - Bucket receiving optimized same-key objects.
- `OPTIMIZATION_PROFILE` - Metadata profile value. Default: `v2-jpeg82-png-best-original-width`.
- `MAX_WIDTH` - Maximum output image width. Set to `0` to preserve original dimensions. Default: `0`.
- `JPEG_QUALITY` - JPEG output quality, 1 through 100. Default: `82`.
- `MIN_BYTES` - Minimum source object size before optimization. Default: `524288`.
- `SCAN_INTERVAL` - Interval for continuous scanning. Default: `10m`.
- `RUN_ONCE` - Run one scan and exit. Default: `false`.

## Local Development

```bash
go test ./...
go vet ./...
go build ./cmd/s3-image-optimizer
```

With `just`:

```bash
just validate
```

## Docker

Build locally:

```bash
docker build -t s3-image-optimizer:local .
```

Validate the local compose stack:

```bash
docker compose config >/tmp/s3-image-optimizer.compose.yaml
```

Run MinIO plus the worker:

```bash
docker compose up --build
```

The local stack creates:

- `source-assets`
- `source-assets-optimized`

## Image

The GitHub Actions workflow publishes:

```text
ghcr.io/xiaolutech/s3-image-optimizer
```
