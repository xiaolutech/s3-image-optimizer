package worker

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"sort"
	"strings"
	"testing"
	"time"

	slog "github.com/xiaolutech/s3-image-sidecar/internal/log"
	"github.com/xiaolutech/s3-image-sidecar/internal/storage"
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

	w := New(store, testWorkerConfig(), testLogger())
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
		"config-signature",
	}
	actualMetadataKeys := []string{
		sourceKeyMetadata,
		sourceETagMetadata,
		profileMetadata,
		sourceContentTypeMetadata,
		variantFormatMetadata,
		configSignatureMetadata,
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
	w := New(store, cfg, testLogger())
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

	w := New(store, cfg, testLogger())
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

	w := New(store, cfg, testLogger())
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

	w := New(store, cfg, testLogger())
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

	w := New(store, cfg, testLogger())
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

	w := New(store, testWorkerConfig(), testLogger())
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

	w := New(store, testWorkerConfig(), testLogger())
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

	w := New(store, testWorkerConfig(), testLogger())
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

func TestWorkerSkipsUnsupportedSourceWithoutWritingOptimized(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{Key: "notes/anim.gif", Size: 1024, ETag: "gif-etag", ContentType: "image/gif"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: []byte("gif")}

	w := New(store, testWorkerConfig(), testLogger())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	if store.getCalls != 1 {
		t.Fatalf("expected one source get, got %d", store.getCalls)
	}
	if len(store.putKeys) != 0 {
		t.Fatalf("expected no writes for unsupported source, got %#v", store.putKeys)
	}
}

func TestWorkerSkipsUndecodableSourceWithoutWritingOptimized(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{
		Key:         "notes/bad.jpg",
		Size:        1024,
		ETag:        "bad-jpeg-etag",
		ContentType: "image/jpeg",
	}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: []byte("not actually a jpeg")}

	w := New(store, testWorkerConfig(), testLogger())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	if len(store.putKeys) != 0 {
		t.Fatalf("expected no writes for undecodable source, got %#v", store.putKeys)
	}
}

func TestWorkerRunScanRoundSkipsUnsupportedWebPDimensions(t *testing.T) {
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
	w := New(store, cfg, testLogger())

	result, err := w.RunScanRound(context.Background())
	if err != nil {
		t.Fatalf("RunScanRound failed: %v", err)
	}
	if result.Processed != 1 {
		t.Fatalf("expected one counted object, got %d", result.Processed)
	}

	if _, ok := store.objects[objKey("optimized", "notes/tall.webp")]; ok {
		t.Fatal("did not expect optimized WebP object for unsupported dimensions")
	}
}

func TestWorkerRunOnceListsSourceBucket(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	store.objects[objKey("source", "b.jpg")] = fakeObject{info: storage.ObjectInfo{Key: "b.jpg", Size: int64(len(body)), ETag: "b", ContentType: "image/jpeg"}, body: body}
	store.objects[objKey("source", "a.jpg")] = fakeObject{info: storage.ObjectInfo{Key: "a.jpg", Size: int64(len(body)), ETag: "a", ContentType: "image/jpeg"}, body: body}

	w := New(store, testWorkerConfig(), testLogger())
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

	w := New(store, cfg, testLogger())
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
	store.getErrors[objKey("source", "photo.jpg")] = errors.New("source get failed")

	cfg := testWorkerConfig()
	cfg.ScanRetryAttempts = 3
	cfg.ScanRetryInitialDelay = time.Nanosecond
	cfg.ScanRetryMaxDelay = time.Nanosecond

	w := New(store, cfg, testLogger())
	err := w.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected RunOnce to return process object error")
	}
	if !strings.Contains(err.Error(), "source get failed") {
		t.Fatalf("expected source get error, got %v", err)
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

	w := New(store, cfg, testLogger())
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
	if store.listCalls != 1 {
		t.Fatalf("expected one full list per pass, got %d", store.listCalls)
	}
	if _, ok := store.objects[objKey("optimized", manifestKey(cfg.OptimizationProfile))]; !ok {
		t.Fatal("expected pass manifest to be saved")
	}
}

func TestWorkerRunScanRoundSkipsUnchangedPass(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	for _, key := range []string{"a.jpg", "b.jpg"} {
		store.objects[objKey("source", key)] = fakeObject{info: storage.ObjectInfo{
			Key:         key,
			Size:        int64(len(body)),
			ETag:        key + "-etag",
			ContentType: "image/jpeg",
		}, body: body}
	}
	cfg := testWorkerConfig()

	w := New(store, cfg, testLogger())

	first, err := w.RunScanRound(context.Background())
	if err != nil {
		t.Fatalf("first RunScanRound failed: %v", err)
	}
	if first.HasMore {
		t.Fatal("expected first scan round to complete the pass")
	}
	if first.Processed != 2 {
		t.Fatalf("expected two processed objects, got %d", first.Processed)
	}
	if _, ok := store.objects[objKey("optimized", manifestKey(cfg.OptimizationProfile))]; !ok {
		t.Fatal("expected pass manifest to be saved")
	}

	store.sourceGetCalls = 0
	store.getCalls = 0
	store.putKeys = nil
	second, err := w.RunScanRound(context.Background())
	if err != nil {
		t.Fatalf("second RunScanRound failed: %v", err)
	}
	if second.HasMore {
		t.Fatal("expected unchanged pass to report no more work")
	}
	if second.Processed != 0 {
		t.Fatalf("expected zero processed objects for unchanged pass, got %d", second.Processed)
	}
	if store.getCalls != 1 {
		t.Fatalf("expected only manifest read for unchanged pass, got %d gets", store.getCalls)
	}
	if store.sourceGetCalls != 0 {
		t.Fatalf("expected no source reads for unchanged pass, got %d", store.sourceGetCalls)
	}
	if len(store.putKeys) != 0 {
		t.Fatalf("expected no writes for unchanged pass, got %#v", store.putKeys)
	}
}

func TestWorkerRunScanRoundProcessesOnlyChangedKeys(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	for _, key := range []string{"a.jpg", "b.jpg"} {
		store.objects[objKey("source", key)] = fakeObject{info: storage.ObjectInfo{
			Key:         key,
			Size:        int64(len(body)),
			ETag:        key + "-etag",
			ContentType: "image/jpeg",
		}, body: body}
	}
	cfg := testWorkerConfig()

	w := New(store, cfg, testLogger())
	if _, err := w.RunScanRound(context.Background()); err != nil {
		t.Fatalf("first RunScanRound failed: %v", err)
	}

	store.putKeys = nil
	store.sourceGetCalls = 0
	bSource := store.objects[objKey("source", "b.jpg")]
	bSource.info.ETag = "b-etag-v2"
	store.objects[objKey("source", "b.jpg")] = bSource
	store.objects[objKey("source", "c.jpg")] = fakeObject{info: storage.ObjectInfo{
		Key:         "c.jpg",
		Size:        int64(len(body)),
		ETag:        "c-etag",
		ContentType: "image/jpeg",
	}, body: body}

	result, err := w.RunScanRound(context.Background())
	if err != nil {
		t.Fatalf("second RunScanRound failed: %v", err)
	}
	if result.HasMore {
		t.Fatal("expected second round to complete the pass")
	}
	if result.Processed != 2 {
		t.Fatalf("expected two changed objects processed, got %d", result.Processed)
	}
	if _, ok := store.objects[objKey("optimized", optimizedVariantKey("a.jpg", webpVariantFormat))]; !ok {
		t.Fatal("expected a.jpg optimized object to be preserved")
	}
	if _, ok := store.objects[objKey("optimized", optimizedVariantKey("c.jpg", webpVariantFormat))]; !ok {
		t.Fatal("expected c.jpg.webp optimized object for added key")
	}
	if _, ok := store.objects[objKey("optimized", manifestKey(cfg.OptimizationProfile))]; !ok {
		t.Fatal("expected updated manifest to be saved")
	}
	if store.sourceGetCalls != 2 {
		t.Fatalf("expected source reads only for changed keys, got %d", store.sourceGetCalls)
	}
}

func TestWorkerRunScanRoundReprocessesAllObjectsOnConfigChange(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	for _, key := range []string{"a.jpg", "b.jpg"} {
		store.objects[objKey("source", key)] = fakeObject{info: storage.ObjectInfo{
			Key:         key,
			Size:        int64(len(body)),
			ETag:        key + "-etag",
			ContentType: "image/jpeg",
		}, body: body}
	}

	cfg := testWorkerConfig()
	w := New(store, cfg, testLogger())
	first, err := w.RunScanRound(context.Background())
	if err != nil {
		t.Fatalf("first RunScanRound failed: %v", err)
	}
	if first.Processed != 2 {
		t.Fatalf("expected two processed objects, got %d", first.Processed)
	}

	store.sourceGetCalls = 0
	cfg2 := testWorkerConfig()
	cfg2.JPEGQuality = 60
	w2 := New(store, cfg2, testLogger())
	result, err := w2.RunScanRound(context.Background())
	if err != nil {
		t.Fatalf("second RunScanRound failed: %v", err)
	}
	if result.HasMore {
		t.Fatal("expected second round to complete the pass")
	}
	if result.Processed != 2 {
		t.Fatalf("expected all objects reprocessed after config change, got %d", result.Processed)
	}
	if store.sourceGetCalls != 2 {
		t.Fatalf("expected both sources re-read after config change, got %d", store.sourceGetCalls)
	}
	written := store.objects[objKey("optimized", optimizedVariantKey("a.jpg", webpVariantFormat))]
	if written.info.Metadata[configSignatureMetadata] != cfg2.Signature() {
		t.Fatalf("expected rewritten object to carry new config signature, got %#v", written.info.Metadata)
	}
}

type fakeObject struct {
	info storage.ObjectInfo
	body []byte
}

type fakeStore struct {
	objects             map[string]fakeObject
	getCalls            int
	sourceGetCalls      int
	listCalls           int
	putKeys             []string
	listBucket          string
	listErrorsRemaining int
	listErr             error
	getErrors           map[string]error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		objects:   make(map[string]fakeObject),
		getErrors: make(map[string]error),
	}
}

func (s *fakeStore) GetObject(ctx context.Context, bucket, key string) ([]byte, *storage.ObjectInfo, error) {
	s.getCalls++
	if bucket == "source" {
		s.sourceGetCalls++
	}
	if err, ok := s.getErrors[objKey(bucket, key)]; ok {
		return nil, nil, err
	}
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

func testLogger() *slog.Logger {
	return slog.New("error")
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
