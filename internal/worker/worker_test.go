package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/xiaolutech/s3-image-optimizer/internal/storage"
)

func TestWorkerProcessesMissingOptimizedObject(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{
		Key:         "notes/photo.jpg",
		Size:        int64(len(largeJPEG(t))),
		ETag:        "source-etag",
		ContentType: "image/jpeg",
	}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: largeJPEG(t)}

	w := New(store, testWorkerConfig())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	written := store.objects[objKey("optimized", "notes/photo.jpg.webp")]
	if len(written.body) == 0 {
		t.Fatal("expected optimized object to be written")
	}
	if written.info.ContentType != "image/webp" {
		t.Fatalf("expected webp content type, got %q", written.info.ContentType)
	}
	if written.info.Metadata["source-etag"] != "source-etag" {
		t.Fatalf("expected source-etag metadata, got %#v", written.info.Metadata)
	}
	if written.info.Metadata["optimization-profile"] != "v6-webp-q82-original" {
		t.Fatalf("expected profile metadata, got %#v", written.info.Metadata)
	}
	if written.info.Metadata["variant-format"] != "webp" {
		t.Fatalf("expected webp variant metadata, got %#v", written.info.Metadata)
	}
	if store.getCalls != 1 {
		t.Fatalf("expected one source get, got %d", store.getCalls)
	}
}

func TestWorkerSkipsCurrentOptimizedObject(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{Key: "notes/photo.jpg", Size: int64(len(largeJPEG(t))), ETag: "source-etag", ContentType: "image/jpeg"}
	store.objects[objKey("optimized", "notes/photo.jpg.webp")] = fakeObject{info: storage.ObjectInfo{
		Key:         "notes/photo.jpg.webp",
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

	w := New(store, testWorkerConfig())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	if store.getCalls != 0 {
		t.Fatalf("expected no source get, got %d", store.getCalls)
	}
	if len(store.putKeys) != 0 {
		t.Fatalf("expected no puts, got %#v", store.putKeys)
	}
}

func TestOptimizedObjectContractVector(t *testing.T) {
	const sourceKey = "notes/photo.png"
	const expectedAVIFKey = "notes/photo.png.avif"
	const expectedWebPKey = "notes/photo.png.webp"

	if got := optimizedVariantKey(sourceKey, avifVariantFormat); got != expectedAVIFKey {
		t.Fatalf("unexpected AVIF optimized key:\n got: %s\nwant: %s", got, expectedAVIFKey)
	}
	if got := optimizedVariantKey(sourceKey, webpVariantFormat); got != expectedWebPKey {
		t.Fatalf("unexpected WebP optimized key:\n got: %s\nwant: %s", got, expectedWebPKey)
	}

	expectedMetadataKeys := []string{
		"source-key",
		"source-etag",
		"optimization-profile",
		"source-content-type",
		"variant-format",
	}
	actualMetadataKeys := []string{
		sourceKeyMetadata,
		sourceETagMetadata,
		profileMetadata,
		sourceContentTypeMetadata,
		variantFormatMetadata,
	}
	for i := range expectedMetadataKeys {
		if actualMetadataKeys[i] != expectedMetadataKeys[i] {
			t.Fatalf("metadata key %d = %q, want %q", i, actualMetadataKeys[i], expectedMetadataKeys[i])
		}
	}
	if avifVariantFormat != "avif" {
		t.Fatalf("variant format = %q, want avif", avifVariantFormat)
	}
	if webpVariantFormat != "webp" {
		t.Fatalf("variant format = %q, want webp", webpVariantFormat)
	}
}

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

	cfg := testAVIFWorkerConfig()
	w := New(store, cfg)
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	key := optimizedVariantKey(source.Key, avifVariantFormat)
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
	if written.info.Metadata["source-content-type"] != source.ContentType {
		t.Fatalf("expected source-content-type metadata, got %#v", written.info.Metadata)
	}
	if written.info.Metadata["variant-format"] != "avif" {
		t.Fatalf("expected variant-format metadata, got %#v", written.info.Metadata)
	}
	if _, ok := store.objects[objKey("optimized", "notes/photo.jpg.webp")]; ok {
		t.Fatalf("did not expect webp optimized object when AVIF is enabled")
	}
}

func TestWorkerSkipsCurrentAVIFOptimizedObject(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{Key: "notes/photo.jpg", Size: int64(len(largeJPEG(t))), ETag: "source-etag", ContentType: "image/jpeg"}
	cfg := testAVIFWorkerConfig()
	key := optimizedVariantKey(source.Key, avifVariantFormat)
	store.objects[objKey("optimized", key)] = fakeObject{info: storage.ObjectInfo{
		Key:         key,
		Size:        100,
		ETag:        "optimized-etag",
		ContentType: "image/avif",
		Metadata: map[string]string{
			"source-key":           source.Key,
			"source-etag":          source.ETag,
			"optimization-profile": cfg.OptimizationProfile,
			"source-content-type":  source.ContentType,
			"variant-format":       "avif",
		},
	}}

	w := New(store, cfg)
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}
	if store.getCalls != 0 {
		t.Fatalf("expected no source get, got %d", store.getCalls)
	}
	if len(store.putKeys) != 0 {
		t.Fatalf("expected no puts, got %#v", store.putKeys)
	}
}

func TestWorkerRewritesStaleAVIFOptimizedObject(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	source := storage.ObjectInfo{Key: "notes/photo.jpg", Size: int64(len(body)), ETag: "new-etag", ContentType: "image/jpeg"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: body}
	cfg := testAVIFWorkerConfig()
	key := optimizedVariantKey(source.Key, avifVariantFormat)
	store.objects[objKey("optimized", key)] = fakeObject{info: storage.ObjectInfo{
		Key:         key,
		Size:        100,
		ETag:        "optimized-etag",
		ContentType: "image/avif",
		Metadata: map[string]string{
			"source-key":           source.Key,
			"source-etag":          "old-etag",
			"optimization-profile": cfg.OptimizationProfile,
			"source-content-type":  source.ContentType,
			"variant-format":       "avif",
		},
	}}

	w := New(store, cfg)
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	written := store.objects[objKey("optimized", key)]
	if written.info.Metadata["source-etag"] != source.ETag {
		t.Fatalf("expected rewritten metadata, got %#v", written.info.Metadata)
	}
	if store.getCalls != 1 {
		t.Fatalf("expected one source get, got %d", store.getCalls)
	}
}

func TestWorkerRewritesAVIFProfileMismatchAtMirroredKey(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	source := storage.ObjectInfo{Key: "notes/photo.jpg", Size: int64(len(body)), ETag: "source-etag", ContentType: "image/jpeg"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: body}
	cfg := testAVIFWorkerConfig()
	key := optimizedVariantKey(source.Key, avifVariantFormat)
	store.objects[objKey("optimized", key)] = fakeObject{info: storage.ObjectInfo{
		Key:         key,
		Size:        100,
		ETag:        "optimized-etag",
		ContentType: "image/avif",
		Metadata: map[string]string{
			"source-key":           source.Key,
			"source-etag":          source.ETag,
			"optimization-profile": "v3-avif-old",
			"source-content-type":  source.ContentType,
			"variant-format":       "avif",
		},
	}}

	w := New(store, cfg)
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	written := store.objects[objKey("optimized", key)]
	if written.info.Metadata["optimization-profile"] != cfg.OptimizationProfile {
		t.Fatalf("expected current profile metadata, got %#v", written.info.Metadata)
	}
	if store.getCalls != 1 {
		t.Fatalf("expected one source get, got %d", store.getCalls)
	}
}

func TestWorkerRewritesAVIFOptimizedObjectMissingRequiredMetadata(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	source := storage.ObjectInfo{Key: "notes/photo.jpg", Size: int64(len(body)), ETag: "source-etag", ContentType: "image/jpeg"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: body}
	cfg := testAVIFWorkerConfig()
	key := optimizedVariantKey(source.Key, avifVariantFormat)
	store.objects[objKey("optimized", key)] = fakeObject{info: storage.ObjectInfo{
		Key:         key,
		Size:        100,
		ETag:        "optimized-etag",
		ContentType: "image/avif",
		Metadata: map[string]string{
			"source-key":           source.Key,
			"source-etag":          source.ETag,
			"optimization-profile": cfg.OptimizationProfile,
			"variant-format":       "avif",
		},
	}}

	w := New(store, cfg)
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	written := store.objects[objKey("optimized", key)]
	if written.info.Metadata["source-content-type"] != source.ContentType {
		t.Fatalf("expected missing metadata to be repaired, got %#v", written.info.Metadata)
	}
	if store.getCalls != 1 {
		t.Fatalf("expected one source get, got %d", store.getCalls)
	}
}

func TestWorkerRewritesAVIFOptimizedObjectWithWrongContentType(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	source := storage.ObjectInfo{Key: "notes/photo.jpg", Size: int64(len(body)), ETag: "source-etag", ContentType: "image/jpeg"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: body}
	cfg := testAVIFWorkerConfig()
	key := optimizedVariantKey(source.Key, avifVariantFormat)
	store.objects[objKey("optimized", key)] = fakeObject{info: storage.ObjectInfo{
		Key:         key,
		Size:        100,
		ETag:        "optimized-etag",
		ContentType: "application/octet-stream",
		Metadata: map[string]string{
			"source-key":           source.Key,
			"source-etag":          source.ETag,
			"optimization-profile": cfg.OptimizationProfile,
			"source-content-type":  source.ContentType,
			"variant-format":       "avif",
		},
	}}

	w := New(store, cfg)
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	written := store.objects[objKey("optimized", key)]
	if written.info.ContentType != "image/avif" {
		t.Fatalf("expected wrong content type to be repaired, got %q", written.info.ContentType)
	}
	if store.getCalls != 1 {
		t.Fatalf("expected one source get, got %d", store.getCalls)
	}
}

func TestWorkerRewritesStaleOptimizedObject(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	source := storage.ObjectInfo{Key: "notes/photo.jpg", Size: int64(len(body)), ETag: "new-etag", ContentType: "image/jpeg"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: body}
	key := optimizedVariantKey(source.Key, webpVariantFormat)
	store.objects[objKey("optimized", key)] = fakeObject{info: storage.ObjectInfo{
		Key:         key,
		Size:        100,
		ETag:        "optimized-etag",
		ContentType: "image/webp",
		Metadata: map[string]string{
			"source-etag":          "old-etag",
			"optimization-profile": "v6-webp-q82-original",
			"source-key":           source.Key,
			"source-content-type":  source.ContentType,
			"variant-format":       "webp",
		},
	}}

	w := New(store, testWorkerConfig())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	written := store.objects[objKey("optimized", key)]
	if written.info.Metadata["source-etag"] != "new-etag" {
		t.Fatalf("expected rewritten metadata, got %#v", written.info.Metadata)
	}
	if store.getCalls != 1 {
		t.Fatalf("expected one source get, got %d", store.getCalls)
	}
}

func TestWorkerRewritesOldOptimizationProfile(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	source := storage.ObjectInfo{Key: "notes/photo.jpg", Size: int64(len(body)), ETag: "source-etag", ContentType: "image/jpeg"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: body}
	key := optimizedVariantKey(source.Key, webpVariantFormat)
	store.objects[objKey("optimized", key)] = fakeObject{info: storage.ObjectInfo{
		Key:         key,
		Size:        100,
		ETag:        "optimized-etag",
		ContentType: "image/webp",
		Metadata: map[string]string{
			"source-etag":          "source-etag",
			"optimization-profile": "v1-jpeg82-png-best-w1920",
			"source-key":           source.Key,
			"source-content-type":  source.ContentType,
			"variant-format":       "webp",
		},
	}}

	w := New(store, testWorkerConfig())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	written := store.objects[objKey("optimized", key)]
	if written.info.Metadata["optimization-profile"] != "v6-webp-q82-original" {
		t.Fatalf("expected rewritten profile metadata, got %#v", written.info.Metadata)
	}
	if store.getCalls != 1 {
		t.Fatalf("expected one source get, got %d", store.getCalls)
	}
}

func TestWorkerSkipsSmallSourceWithoutRead(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{Key: "small.jpg", Size: 10, ETag: "small-etag", ContentType: "image/jpeg"}

	w := New(store, testWorkerConfig())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	if store.getCalls != 0 {
		t.Fatalf("expected no source get, got %d", store.getCalls)
	}
	if len(store.putKeys) != 0 {
		t.Fatalf("expected no puts, got %#v", store.putKeys)
	}
}

func TestWorkerWritesSkipMarkerForUnsupportedSource(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{Key: "notes/anim.gif", Size: 1024, ETag: "gif-etag", ContentType: "image/gif"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: []byte("gif")}

	w := New(store, testWorkerConfig())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	marker := decodeSkipMarker(t, store.objects[objKey("optimized", skipMarkerKey(source.Key))].body)
	if marker.SourceKey != source.Key {
		t.Fatalf("expected source key %q, got %q", source.Key, marker.SourceKey)
	}
	if marker.SourceETag != "gif-etag" {
		t.Fatalf("expected source etag gif-etag, got %q", marker.SourceETag)
	}
	if marker.Reason != "unsupported_content_type" {
		t.Fatalf("expected unsupported_content_type, got %q", marker.Reason)
	}
}

func TestWorkerWritesSkipMarkerForUndecodableSupportedSource(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{
		Key:         "notes/bad.jpg",
		Size:        1024,
		ETag:        "bad-jpeg-etag",
		ContentType: "image/jpeg",
	}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: []byte("not actually a jpeg")}

	w := New(store, testWorkerConfig())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	marker := decodeSkipMarker(t, store.objects[objKey("optimized", skipMarkerKey(source.Key))].body)
	if marker.SourceKey != source.Key {
		t.Fatalf("expected source key %q, got %q", source.Key, marker.SourceKey)
	}
	if marker.SourceETag != "bad-jpeg-etag" {
		t.Fatalf("expected source etag bad-jpeg-etag, got %q", marker.SourceETag)
	}
	if marker.Reason != "decode_image_failed" {
		t.Fatalf("expected decode_image_failed, got %q", marker.Reason)
	}
}

func TestWorkerRunScanRoundWritesSkipMarkerForWebPUnsupportedDimensions(t *testing.T) {
	store := newFakeStore()
	body := tallJPEG(t)
	source := storage.ObjectInfo{
		Key:         "notes/tall.jpg",
		Size:        int64(len(body)),
		ETag:        "tall-jpeg-etag",
		ContentType: "image/jpeg",
	}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: body}

	cfg := testWorkerConfig()
	cfg.MinBytes = 0
	w := New(store, cfg)

	result, err := w.RunScanRound(context.Background())
	if err != nil {
		t.Fatalf("RunScanRound failed: %v", err)
	}
	if result.Processed != 1 {
		t.Fatalf("expected one counted object, got %d", result.Processed)
	}

	marker := decodeSkipMarker(t, store.objects[objKey("optimized", skipMarkerKey(source.Key))].body)
	if marker.SourceKey != source.Key {
		t.Fatalf("expected source key %q, got %q", source.Key, marker.SourceKey)
	}
	if marker.SourceETag != source.ETag {
		t.Fatalf("expected source etag %q, got %q", source.ETag, marker.SourceETag)
	}
	if marker.Reason != "unsupported_dimensions" {
		t.Fatalf("expected unsupported_dimensions, got %q", marker.Reason)
	}
	if _, ok := store.objects[objKey("optimized", "notes/tall.webp")]; ok {
		t.Fatal("did not expect optimized WebP object for unsupported dimensions")
	}
}

func TestWorkerSkipsCurrentSkipMarkerWithoutReadingSource(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{Key: "notes/clip.webm", Size: 10 * 1024 * 1024, ETag: "webm-etag"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: storage.ObjectInfo{
		Key:         source.Key,
		Size:        source.Size,
		ETag:        source.ETag,
		ContentType: "video/webm",
	}, body: []byte("large video")}
	store.objects[objKey("optimized", skipMarkerKey(source.Key))] = fakeObject{info: storage.ObjectInfo{
		Key:         skipMarkerKey(source.Key),
		Size:        100,
		ETag:        "marker-etag",
		ContentType: "application/json",
		Metadata: map[string]string{
			"source-etag":          source.ETag,
			"optimization-profile": "v6-webp-q82-original",
		},
	}}

	w := New(store, testWorkerConfig())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	if store.getCalls != 0 {
		t.Fatalf("expected no source get, got %d", store.getCalls)
	}
	if len(store.putKeys) != 0 {
		t.Fatalf("expected no puts, got %#v", store.putKeys)
	}
}

func TestWorkerSkipsCurrentSkipMarkerWithoutSourceHeadWhenAVIFEnabled(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{Key: "notes/clip.webm", Size: 10 * 1024 * 1024, ETag: "webm-etag"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: storage.ObjectInfo{
		Key:         source.Key,
		Size:        source.Size,
		ETag:        source.ETag,
		ContentType: "video/webm",
	}, body: []byte("large video")}
	cfg := testAVIFWorkerConfig()
	store.objects[objKey("optimized", skipMarkerKey(source.Key))] = fakeObject{info: storage.ObjectInfo{
		Key:         skipMarkerKey(source.Key),
		Size:        100,
		ETag:        "marker-etag",
		ContentType: "application/json",
		Metadata: map[string]string{
			"source-etag":          source.ETag,
			"optimization-profile": cfg.OptimizationProfile,
		},
	}}

	w := New(store, cfg)
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	if store.getCalls != 0 {
		t.Fatalf("expected no source get, got %d", store.getCalls)
	}
	if store.sourceHeadCalls != 0 {
		t.Fatalf("expected no source head, got %d", store.sourceHeadCalls)
	}
	if len(store.putKeys) != 0 {
		t.Fatalf("expected no puts, got %#v", store.putKeys)
	}
}

func TestWorkerWritesUnsupportedSkipMarkerFromSourceHeadWithoutReadingBody(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{Key: "notes/clip.webm", Size: 10 * 1024 * 1024, ETag: "webm-etag"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: storage.ObjectInfo{
		Key:         source.Key,
		Size:        source.Size,
		ETag:        source.ETag,
		ContentType: "video/webm",
	}, body: []byte("large video")}

	w := New(store, testWorkerConfig())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	if store.getCalls != 0 {
		t.Fatalf("expected no source get, got %d", store.getCalls)
	}
	marker := decodeSkipMarker(t, store.objects[objKey("optimized", skipMarkerKey(source.Key))].body)
	if marker.Reason != "unsupported_content_type" {
		t.Fatalf("expected unsupported_content_type, got %q", marker.Reason)
	}
}

func TestWorkerRunOnceListsSourceBucket(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	store.objects[objKey("source", "b.jpg")] = fakeObject{info: storage.ObjectInfo{Key: "b.jpg", Size: int64(len(body)), ETag: "b", ContentType: "image/jpeg"}, body: body}
	store.objects[objKey("source", "a.jpg")] = fakeObject{info: storage.ObjectInfo{Key: "a.jpg", Size: int64(len(body)), ETag: "a", ContentType: "image/jpeg"}, body: body}

	w := New(store, testWorkerConfig())
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}

	if store.listBucket != "source" {
		t.Fatalf("expected list bucket source, got %q", store.listBucket)
	}
	if _, ok := store.objects[objKey("optimized", optimizedVariantKey("a.jpg", webpVariantFormat))]; !ok {
		t.Fatal("expected a.jpg.webp optimized object")
	}
	if _, ok := store.objects[objKey("optimized", optimizedVariantKey("b.jpg", webpVariantFormat))]; !ok {
		t.Fatal("expected b.jpg.webp optimized object")
	}
}

func TestWorkerRunOnceRetriesTransientListErrors(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	store.objects[objKey("source", "photo.jpg")] = fakeObject{info: storage.ObjectInfo{Key: "photo.jpg", Size: int64(len(body)), ETag: "photo", ContentType: "image/jpeg"}, body: body}
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
	if _, ok := store.objects[objKey("optimized", optimizedVariantKey("photo.jpg", webpVariantFormat))]; !ok {
		t.Fatal("expected photo.jpg.webp optimized object")
	}
}

func TestWorkerRunOnceDoesNotRetryProcessObjectErrors(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	store.objects[objKey("source", "photo.jpg")] = fakeObject{info: storage.ObjectInfo{Key: "photo.jpg", Size: int64(len(body)), ETag: "photo", ContentType: "image/jpeg"}, body: body}
	store.headErrors[objKey("optimized", optimizedVariantKey("photo.jpg", webpVariantFormat))] = errors.New("optimized head failed")

	cfg := testWorkerConfig()
	cfg.ScanRetryAttempts = 3
	cfg.ScanRetryInitialDelay = time.Nanosecond
	cfg.ScanRetryMaxDelay = time.Nanosecond

	w := New(store, cfg)
	err := w.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected RunOnce to return process object error")
	}
	if !strings.Contains(err.Error(), "optimized head failed") {
		t.Fatalf("expected optimized head error, got %v", err)
	}
	if store.listCalls != 1 {
		t.Fatalf("expected no retry for process object error, got %d list calls", store.listCalls)
	}
}

func TestWorkerRunScanRoundProcessesBatchAndAdvancesInMemoryCursor(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	for _, key := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		store.objects[objKey("source", key)] = fakeObject{info: storage.ObjectInfo{
			Key:         key,
			Size:        int64(len(body)),
			ETag:        key + "-etag",
			ContentType: "image/jpeg",
		}, body: body}
	}
	cfg := testWorkerConfig()
	cfg.ScanBatchSize = 2

	w := New(store, cfg)
	first, err := w.RunScanRound(context.Background())
	if err != nil {
		t.Fatalf("first RunScanRound failed: %v", err)
	}
	if !first.HasMore {
		t.Fatal("expected first scan round to report more objects")
	}
	if first.LastKey != "b.jpg" {
		t.Fatalf("expected first last key b.jpg, got %q", first.LastKey)
	}
	if _, ok := store.objects[objKey("optimized", optimizedVariantKey("a.jpg", webpVariantFormat))]; !ok {
		t.Fatal("expected a.jpg.webp optimized object")
	}
	if _, ok := store.objects[objKey("optimized", optimizedVariantKey("b.jpg", webpVariantFormat))]; !ok {
		t.Fatal("expected b.jpg.webp optimized object")
	}
	if _, ok := store.objects[objKey("optimized", optimizedVariantKey("c.jpg", webpVariantFormat))]; ok {
		t.Fatal("did not expect c.jpg.webp to be processed in first batch")
	}

	second, err := w.RunScanRound(context.Background())
	if err != nil {
		t.Fatalf("second RunScanRound failed: %v", err)
	}
	if second.HasMore {
		t.Fatal("expected second scan round to reach bucket end")
	}
	if second.LastKey != "c.jpg" {
		t.Fatalf("expected second last key c.jpg, got %q", second.LastKey)
	}
	if _, ok := store.objects[objKey("optimized", optimizedVariantKey("c.jpg", webpVariantFormat))]; !ok {
		t.Fatal("expected c.jpg.webp optimized object")
	}
	if store.listStartAfterCalls[0] != "" {
		t.Fatalf("expected first list to start at bucket beginning, got %q", store.listStartAfterCalls[0])
	}
	if store.listStartAfterCalls[1] != "b.jpg" {
		t.Fatalf("expected second list to start after b.jpg, got %q", store.listStartAfterCalls[1])
	}
}

func TestWorkerRunScanRoundCountsCurrentObjectsTowardBatchWindow(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	for _, key := range []string{"a.jpg", "b.jpg", "c.jpg", "d.jpg"} {
		store.objects[objKey("source", key)] = fakeObject{info: storage.ObjectInfo{
			Key:         key,
			Size:        int64(len(body)),
			ETag:        key + "-etag",
			ContentType: "image/jpeg",
		}, body: body}
	}
	store.objects[objKey("optimized", optimizedVariantKey("a.jpg", webpVariantFormat))] = fakeObject{info: storage.ObjectInfo{
		Key:         optimizedVariantKey("a.jpg", webpVariantFormat),
		Size:        100,
		ETag:        "a.jpg-optimized-etag",
		ContentType: "image/webp",
		Metadata: map[string]string{
			"source-etag":          "a.jpg-etag",
			"optimization-profile": "v6-webp-q82-original",
			"source-key":           "a.jpg",
			"source-content-type":  "image/jpeg",
			"variant-format":       "webp",
		},
	}}
	store.objects[objKey("optimized", skipMarkerKey("b.jpg"))] = fakeObject{info: storage.ObjectInfo{
		Key:         skipMarkerKey("b.jpg"),
		Size:        100,
		ETag:        "b.jpg-skip-etag",
		ContentType: "application/json",
		Metadata: map[string]string{
			"source-etag":          "b.jpg-etag",
			"optimization-profile": "v6-webp-q82-original",
		},
	}}
	cfg := testWorkerConfig()
	cfg.ScanBatchSize = 2

	w := New(store, cfg)
	result, err := w.RunScanRound(context.Background())
	if err != nil {
		t.Fatalf("RunScanRound failed: %v", err)
	}
	if result.Processed != 0 {
		t.Fatalf("expected no processed objects, got %d", result.Processed)
	}
	if result.LastKey != "b.jpg" {
		t.Fatalf("expected last key b.jpg, got %q", result.LastKey)
	}
	if !result.HasMore {
		t.Fatal("expected scan round to report more objects")
	}
	if _, ok := store.objects[objKey("optimized", optimizedVariantKey("c.jpg", webpVariantFormat))]; ok {
		t.Fatal("did not expect c.jpg.webp to be processed in first batch window")
	}
	if store.getCalls != 0 {
		t.Fatalf("expected no source gets, got %d", store.getCalls)
	}
	if len(store.listStartAfterCalls) != 1 {
		t.Fatalf("expected one paged list call, got %d", len(store.listStartAfterCalls))
	}
	if store.listStartAfterCalls[0] != "" {
		t.Fatalf("expected first list to start at bucket beginning, got %q", store.listStartAfterCalls[0])
	}
}

type fakeObject struct {
	info storage.ObjectInfo
	body []byte
}

type fakeStore struct {
	objects             map[string]fakeObject
	getCalls            int
	sourceHeadCalls     int
	listCalls           int
	putKeys             []string
	listBucket          string
	listErrorsRemaining int
	listErr             error
	headErrors          map[string]error
	listStartAfterCalls []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		objects:    make(map[string]fakeObject),
		headErrors: make(map[string]error),
	}
}

func (s *fakeStore) HeadObject(ctx context.Context, bucket, key string) (*storage.ObjectInfo, error) {
	if bucket == "source" {
		s.sourceHeadCalls++
	}
	if err, ok := s.headErrors[objKey(bucket, key)]; ok {
		return nil, err
	}
	obj, ok := s.objects[objKey(bucket, key)]
	if !ok {
		return nil, errNotFound{}
	}
	info := obj.info
	return &info, nil
}

func (s *fakeStore) GetObject(ctx context.Context, bucket, key string) ([]byte, *storage.ObjectInfo, error) {
	s.getCalls++
	obj, ok := s.objects[objKey(bucket, key)]
	if !ok {
		return nil, nil, errNotFound{}
	}
	info := obj.info
	return append([]byte(nil), obj.body...), &info, nil
}

func (s *fakeStore) PutObject(ctx context.Context, bucket, key string, body []byte, opts storage.PutOptions) error {
	s.putKeys = append(s.putKeys, objKey(bucket, key))
	s.objects[objKey(bucket, key)] = fakeObject{
		info: storage.ObjectInfo{
			Key:         key,
			Size:        int64(len(body)),
			ETag:        "put-etag",
			ContentType: opts.ContentType,
			Metadata:    copyMetadata(opts.Metadata),
		},
		body: append([]byte(nil), body...),
	}
	return nil
}

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
		if !strings.HasPrefix(obj.info.Key, prefix) {
			continue
		}
		keys = append(keys, obj.info.Key)
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

func (s *fakeStore) ListObjectsPage(ctx context.Context, bucket, prefix, startAfter string, maxKeys int32) (storage.ListPage, error) {
	s.listCalls++
	s.listBucket = bucket
	s.listStartAfterCalls = append(s.listStartAfterCalls, startAfter)
	if s.listErrorsRemaining > 0 {
		s.listErrorsRemaining--
		return storage.ListPage{}, s.listErr
	}
	var keys []string
	for fullKey, obj := range s.objects {
		if !strings.HasPrefix(fullKey, bucket+"/") {
			continue
		}
		if !strings.HasPrefix(obj.info.Key, prefix) {
			continue
		}
		if startAfter != "" && obj.info.Key <= startAfter {
			continue
		}
		keys = append(keys, obj.info.Key)
	}
	sort.Strings(keys)
	if maxKeys > 0 && len(keys) > int(maxKeys) {
		keys = keys[:maxKeys]
	}
	objects := make([]storage.ObjectInfo, 0, len(keys))
	for _, key := range keys {
		objects = append(objects, s.objects[objKey(bucket, key)].info)
	}
	return storage.ListPage{
		Objects: objects,
		HasMore: maxKeys > 0 &&
			len(keys) == int(maxKeys) &&
			s.hasKeyAfter(bucket, prefix, keys[len(keys)-1]),
	}, nil
}

func (s *fakeStore) hasKeyAfter(bucket, prefix, key string) bool {
	for fullKey, obj := range s.objects {
		if !strings.HasPrefix(fullKey, bucket+"/") {
			continue
		}
		if !strings.HasPrefix(obj.info.Key, prefix) {
			continue
		}
		if obj.info.Key > key {
			return true
		}
	}
	return false
}

type errNotFound struct{}

func (errNotFound) Error() string { return "not found" }

func (errNotFound) NotFound() bool { return true }

func objKey(bucket, key string) string {
	return bucket + "/" + key
}

func copyMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	copied := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copied[key] = value
	}
	return copied
}

func testWorkerConfig() Config {
	return Config{
		SourceBucket:        "source",
		OptimizedBucket:     "optimized",
		OptimizationProfile: "v6-webp-q82-original",
		MaxWidth:            0,
		JPEGQuality:         82,
		WebPQuality:         82,
		MinBytes:            512,
		ScanBatchSize:       200,
	}
}

func testAVIFWorkerConfig() Config {
	cfg := testWorkerConfig()
	cfg.OptimizationProfile = "v4-avif-target1m-original"
	cfg.AVIFEnabled = true
	cfg.AVIFTargetBytes = 1024 * 1024
	cfg.AVIFQualityMin = 35
	cfg.AVIFQualityMax = 75
	cfg.AVIFSpeed = 10
	return cfg
}

func largeJPEG(t *testing.T) []byte {
	t.Helper()
	return encodeJPEG(t, noisyImage(3000, 1200), 95)
}

func tallJPEG(t *testing.T) []byte {
	t.Helper()
	return encodeJPEG(t, solidImage(1, 16384), 82)
}

func smallJPEG(t *testing.T) []byte {
	t.Helper()
	return encodeJPEG(t, solidImage(200, 100), 82)
}

func encodeJPEG(t *testing.T, img image.Image, quality int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func noisyImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var state uint32 = 1
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			state = state*1664525 + 1013904223
			img.Set(x, y, color.RGBA{R: uint8(state >> 24), G: uint8(state >> 16), B: uint8(state >> 8), A: 255})
		}
	}
	return img
}

func solidImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 128, G: 64, B: 32, A: 255})
		}
	}
	return img
}

func decodeSkipMarker(t *testing.T, body []byte) SkipMarker {
	t.Helper()
	if len(body) == 0 {
		t.Fatal("expected skip marker body")
	}
	var marker SkipMarker
	if err := json.Unmarshal(body, &marker); err != nil {
		t.Fatalf("decode skip marker: %v", err)
	}
	return marker
}
