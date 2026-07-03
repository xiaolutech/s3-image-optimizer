# AVIF-only optimized image serving design

## Context

`s3-image-optimizer` currently scans source images and writes one optimized same-key copy to the optimized bucket. The optimized copy is trusted by `s3-static` only when its `source-etag` and `optimization-profile` metadata match the current source object.

That model keeps public URLs stable, but it is too conservative for large photographic PNG files because the optimizer keeps PNG as PNG. A test image in this repository showed that same-format PNG optimization remained around 20 MiB, while lossy modern image formats can get under 1 MiB without resizing.

The approved long-term direction is:

- Generate only AVIF optimized objects.
- Do not generate JPEG, WebP, or PNG optimized variants.
- Do not create a manifest file.
- Preserve the public URL and source bucket object.
- Keep `s3-static` as a thin serving gateway: it may select a trusted AVIF object, but it must not encode, queue, scan, or write optimized objects.

## Goals

- Serve AVIF to clients that explicitly advertise `image/avif` support.
- Fall back to the source object for clients that do not advertise AVIF support.
- Preserve source-object conditional request semantics.
- Keep optimized freshness based on source ETag plus optimization profile.
- Keep the optimized bucket layout deterministic and metadata-driven.
- Avoid manifest reads and multi-variant negotiation.

## Non-goals

- No WebP or JPEG optimized output.
- No compatibility optimized variant for older clients.
- No access-time trigger path in `s3-static`.
- No resizing in the first AVIF-only design; source dimensions are preserved.
- No `Range` support for optimized AVIF in the first version.
- No `HEAD` or `?meta=1` optimized-object behavior in the first version.

## Optimized object layout

For a source object:

```text
source bucket key: notes/photo.png
source etag: abc123
profile: v4-avif-target1m-original
```

`s3-image-optimizer` writes:

```text
optimized bucket key:
.s3-image-optimizer/avif/<sha256(source-key)>/<profile>/image.avif
```

The AVIF object metadata must include:

```text
x-amz-meta-source-key: notes/photo.png
x-amz-meta-source-etag: abc123
x-amz-meta-optimization-profile: v4-avif-target1m-original
x-amz-meta-source-content-type: image/png
x-amz-meta-variant-format: avif
```

The key uses `sha256(source-key)` to avoid object-name issues with arbitrary source paths. The original source key stays in metadata for traceability.

## Optimizer behavior

`s3-image-optimizer` continues to own all image processing. It supports JPEG and PNG source objects and produces only AVIF output.

The encoder should be target-size driven rather than fixed-quality only:

```text
AVIF_ENABLED=true
AVIF_TARGET_BYTES=1048576
AVIF_QUALITY_MIN=35
AVIF_QUALITY_MAX=75
AVIF_EFFORT=<implementation-dependent default>
MIN_SAVINGS=0.05
```

Encoding chooses the highest quality that reaches `AVIF_TARGET_BYTES` when possible. If no candidate reaches the target, the optimizer may keep the smallest candidate only when it still meets `MIN_SAVINGS`; otherwise it writes a skip marker.

The optimizer writes the AVIF object only after successful encode and validation. There is no manifest finalization step. Freshness is still determined by object metadata, so changing the AVIF strategy requires bumping `OPTIMIZATION_PROFILE`.

## s3-static behavior

`s3-static` keeps source-object lookup as the first authority.

For a normal `GET` image request:

1. Validate and locate the source object.
2. Evaluate conditional headers against the source object.
3. Only if the request `Accept` header includes `image/avif`, compute the deterministic AVIF optimized key.
4. Open the AVIF object from the optimized bucket.
5. Trust it only when:
   - `source-key` equals the source path.
   - `source-etag` equals the current source ETag.
   - `optimization-profile` equals the configured profile.
   - `variant-format` is `avif`.
6. If trusted, serve it with:
   - `Content-Type: image/avif`
   - `Vary: Accept`
   - `X-S3-Static-Optimized: hit; format=avif`
7. If the AVIF object is missing, stale, profile-mismatched, or unreadable, fall back to the source object.

If the request does not advertise AVIF support, `s3-static` must not probe the optimized bucket. It serves the source object directly.

`HEAD`, `Range`, and `?meta=1` stay source-object based in the first version. This keeps the current seekable serving and metadata semantics intact.

## Module interfaces

The `s3-static` handler should not learn the AVIF key layout or metadata rules directly. Put that behavior behind a small module interface:

```go
type OptimizedVariantResolver interface {
    Resolve(ctx context.Context, r *http.Request, source *interfaces.FileInfo) (*interfaces.OpenedFile, VariantStatus)
}
```

The implementation owns:

- `Accept` parsing for `image/avif`.
- deterministic AVIF key construction.
- optimized object open/read behavior.
- metadata trust checks.
- status mapping such as `disabled`, `not-accepted`, `miss`, `stale`, `profile-mismatch`, and `hit`.

This keeps the handler flow shallow and preserves the existing thin-gateway rule.

## Failure and fallback

All optimized-path failures are soft failures for public reads. The public response should fall back to the source object unless the source object itself fails.

Suggested status header values:

```text
X-S3-Static-Optimized: hit; format=avif
X-S3-Static-Optimized: miss
X-S3-Static-Optimized: stale
X-S3-Static-Optimized: profile-mismatch
X-S3-Static-Optimized: not-accepted
```

`not-accepted` can be omitted to avoid adding a response header on ordinary source responses. `hit`, `miss`, `stale`, and `profile-mismatch` are useful diagnostics when optimized serving was attempted.

## Testing plan

`s3-image-optimizer` tests:

- JPEG source produces AVIF with `image/avif` content type.
- PNG source produces AVIF with source dimensions preserved.
- AVIF object key uses `sha256(source-key)`.
- AVIF metadata includes source key, source ETag, profile, source content type, and variant format.
- target-size quality search chooses the highest passing quality.
- insufficient savings writes a skip marker.
- profile changes cause stale optimized objects to be regenerated.

`s3-static` tests:

- request with `Accept: image/avif` serves trusted AVIF.
- request without AVIF support does not touch optimized storage.
- missing AVIF falls back to source.
- stale `source-etag` falls back to source.
- profile mismatch falls back to source.
- mismatched `source-key` or `variant-format` falls back to source.
- optimized hit sets `Content-Type: image/avif`, `Vary: Accept`, and optimized status.
- `HEAD`, `Range`, `?meta=1`, small images, and non-image files stay source-only.
- conditional requests are evaluated against source ETag before optimized lookup.

## Rollout

1. Add AVIF encode support to `s3-image-optimizer` behind disabled-by-default config.
2. Write AVIF objects to the new hidden key layout with full metadata.
3. Add the `s3-static` optimized variant resolver behind disabled-by-default config.
4. Enable both services in the local compose stack with a new profile string.
5. Verify a real large PNG path returns AVIF only when `Accept: image/avif` is present.
6. After validation, enable the same profile in deployment.

The rollout must not delete old same-key optimized objects. They become stale by profile mismatch and can be cleaned later by a separate maintenance task.
