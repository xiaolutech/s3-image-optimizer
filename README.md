# s3-image-sidecar

Standalone worker that scans an S3-compatible source bucket, writes optimized image copies to a separate bucket, and tags each optimized object with source metadata that `s3-static` can verify.

Public URL serving is intentionally out of scope. `s3-static` remains the public gateway and keeps the original domain and path.

## Contract With s3-static

By default, this worker writes WebP sidecar objects that mirror the source bucket path and append the output format suffix:

```text
notes/photo.jpg -> notes/photo.jpg.webp
```

When `AVIF_ENABLED=true`, it writes AVIF sidecar objects with the same mirrored key rule:

```text
notes/photo.jpg -> notes/photo.jpg.avif
```

Every optimized object includes:

- `x-amz-meta-source-key`
- `x-amz-meta-source-etag`
- `x-amz-meta-optimization-profile`
- `x-amz-meta-source-content-type`
- `x-amz-meta-variant-format: webp` or `avif`
- `x-amz-meta-config-signature` (hash of the optimization settings used to produce the object)

For a source object:

```text
bucket: logseq-assets
key: notes/photo.jpg
etag: abc123
```

the worker writes:

```text
bucket: logseq-assets-optimized
key: notes/photo.jpg.webp
x-amz-meta-source-key: notes/photo.jpg
x-amz-meta-source-etag: abc123
x-amz-meta-optimization-profile: v7-webp-q82-w2560
x-amz-meta-source-content-type: image/jpeg
x-amz-meta-variant-format: webp
x-amz-meta-config-signature: <hash>
```

`s3-static` uses those metadata values to decide whether the optimized object is safe to serve. If the source ETag changes or the profile changes, `s3-static` falls back to the source object until this worker rewrites the optimized copy.

## Behavior

- Stays idle by default when both `SCAN_ENABLED=false` and `RUN_ONCE=false`.
- Runs bounded resident scan rounds when `SCAN_ENABLED=true`.
- Runs one manifest-diff pass and exits when `RUN_ONCE=true`.
- Each resident scan round starts by listing the source bucket once, building a folder fingerprint (`key + etag + size` over all objects), and comparing it against the saved manifest.
- When the fingerprint is unchanged, the whole folder is skipped and no object is touched.
- When it changed, only the added or modified keys are processed across bounded scan rounds; the optimized copies of untouched keys are not re-read.
- When the optimization configuration changed (`MIN_BYTES`, quality, `MAX_WIDTH`, AVIF settings, or profile), every object is re-scheduled so affected outputs are regenerated without bumping the profile.
- Saves the current manifest to `.s3-image-sidecar/manifest/<profile>.json` in `OPTIMIZED_BUCKET` at the end of each pass so change detection survives restarts.
- Supports JPEG and PNG source objects.
- Resizes images wider than 2560 pixels by default.
- Resizes images wider than `MAX_WIDTH` only when `MAX_WIDTH` is greater than `0`.
- Encodes supported source images to WebP when `AVIF_ENABLED=false`.
- Encodes supported source images to AVIF when `AVIF_ENABLED=true`.
- Writes optimized objects to `OPTIMIZED_BUCKET`.
- Skips objects smaller than `MIN_BYTES`.
- Skips unsupported or insufficiently-compressed images without writing an optimized object.

Because only changed keys are re-optimized, an optimized object that is deleted or corrupted without a matching source change will not be repaired by either the resident loop or `RUN_ONCE`. To force a full re-encode, change an optimization setting (which bumps the manifest's config signature and re-schedules every object) or delete the manifest at `.s3-image-sidecar/manifest/<profile>.json`.

## Configuration

- `PORT` - Health endpoint port. Default: `8080`.
- `S3_ENDPOINT` - S3-compatible endpoint, for example `minio:9000`.
- `S3_REGION` - S3 region. Default: `us-east-1`.
- `S3_ACCESS_KEY_ID` - S3 access key.
- `S3_SECRET_ACCESS_KEY` - S3 secret key.
- `S3_USE_SSL` - Use HTTPS for S3. Default: `true`.
- `SOURCE_BUCKET` - Bucket containing original objects.
- `OPTIMIZED_BUCKET` - Bucket receiving optimized objects.
- `OPTIMIZATION_PROFILE` - Metadata profile value. Default: `v7-webp-q82-w2560`.
- `MAX_WIDTH` - Maximum output image width. Set to `0` to preserve original dimensions. Default: `2560`.
- `JPEG_QUALITY` - JPEG output quality, 1 through 100. Default: `82`.
- `WEBP_QUALITY` - WebP output quality, 1 through 100. Default: `82`.
- `AVIF_ENABLED` - Encode supported source images to AVIF optimized objects instead of WebP. Default: `false`.
- `AVIF_TARGET_BYTES` - Target AVIF output size. Set to `0` to disable target-size search. Default: `1048576`.
- `AVIF_QUALITY_MIN` - Lowest AVIF quality considered during search. Default: `35`.
- `AVIF_QUALITY_MAX` - Highest AVIF quality considered during search. Default: `75`.
- `AVIF_SPEED` - AVIF encoder speed, 0 through 10; higher is faster with lower compression efficiency. Default: `6`.
- `MIN_BYTES` - Minimum source object size before optimization. Default: `524288`.
- `SCAN_ENABLED` - Enable resident bounded scan rounds. Default: `false`.
- `SCAN_INTERVAL` - Delay between resident scan rounds while a bucket pass still has more changed objects; sets the pace between batches when `SCAN_ENABLED=true`. Default: `24h`.
- `SCAN_FULL_PASS_INTERVAL` - Delay after a resident scan round reaches the end of the bucket before starting over from the beginning. Default: `24h`.
- `SCAN_BATCH_SIZE` - Maximum changed source objects processed per resident scan round. Only keys selected by the manifest diff consume this window. This is an object count, not a byte limit. Default: `200`.
- `SCAN_RETRY_ATTEMPTS` - Whole-scan retry attempts after a failed scan, including the first attempt. Set to `1` to disable scan retries. Default: `8`.
- `SCAN_RETRY_INITIAL_DELAY` - Initial whole-scan retry delay. Default: `5s`.
- `SCAN_RETRY_MAX_DELAY` - Maximum whole-scan retry delay after exponential backoff. Default: `2m`.
- `RUN_ONCE` - Run a single manifest-diff pass and exit. Default: `false`.

## Local Development

```bash
go test ./...
go vet ./...
go build ./cmd/s3-image-sidecar
```

With `just`:

```bash
just validate
```

## Docker

Build locally:

```bash
DOCKER_BUILDKIT=1 docker build -t s3-image-sidecar:local .
```

Validate the local compose stack:

```bash
docker compose config >/tmp/s3-image-sidecar.compose.yaml
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
ghcr.io/xiaolutech/s3-image-sidecar
```
