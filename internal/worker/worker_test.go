package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"sort"
	"strings"
	"testing"

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

	written := store.objects[objKey("optimized", source.Key)]
	if len(written.body) == 0 {
		t.Fatal("expected optimized object to be written")
	}
	if written.info.ContentType != "image/jpeg" {
		t.Fatalf("expected jpeg content type, got %q", written.info.ContentType)
	}
	if written.info.Metadata["source-etag"] != "source-etag" {
		t.Fatalf("expected source-etag metadata, got %#v", written.info.Metadata)
	}
	if written.info.Metadata["optimization-profile"] != "v2-jpeg82-png-best-original-width" {
		t.Fatalf("expected profile metadata, got %#v", written.info.Metadata)
	}
	if store.getCalls != 1 {
		t.Fatalf("expected one source get, got %d", store.getCalls)
	}
}

func TestWorkerSkipsCurrentOptimizedObject(t *testing.T) {
	store := newFakeStore()
	source := storage.ObjectInfo{Key: "notes/photo.jpg", Size: int64(len(largeJPEG(t))), ETag: "source-etag", ContentType: "image/jpeg"}
	store.objects[objKey("optimized", source.Key)] = fakeObject{info: storage.ObjectInfo{
		Key:         source.Key,
		Size:        100,
		ETag:        "optimized-etag",
		ContentType: "image/jpeg",
		Metadata: map[string]string{
			"source-etag":          "source-etag",
			"optimization-profile": "v2-jpeg82-png-best-original-width",
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

func TestWorkerRewritesStaleOptimizedObject(t *testing.T) {
	store := newFakeStore()
	body := largeJPEG(t)
	source := storage.ObjectInfo{Key: "notes/photo.jpg", Size: int64(len(body)), ETag: "new-etag", ContentType: "image/jpeg"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: body}
	store.objects[objKey("optimized", source.Key)] = fakeObject{info: storage.ObjectInfo{
		Key:         source.Key,
		Size:        100,
		ETag:        "optimized-etag",
		ContentType: "image/jpeg",
		Metadata: map[string]string{
			"source-etag":          "old-etag",
			"optimization-profile": "v1-jpeg82-png-best-w1920",
		},
	}}

	w := New(store, testWorkerConfig())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	written := store.objects[objKey("optimized", source.Key)]
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
	store.objects[objKey("optimized", source.Key)] = fakeObject{info: storage.ObjectInfo{
		Key:         source.Key,
		Size:        100,
		ETag:        "optimized-etag",
		ContentType: "image/jpeg",
		Metadata: map[string]string{
			"source-etag":          "source-etag",
			"optimization-profile": "v1-jpeg82-png-best-w1920",
		},
	}}

	w := New(store, testWorkerConfig())
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	written := store.objects[objKey("optimized", source.Key)]
	if written.info.Metadata["optimization-profile"] != "v2-jpeg82-png-best-original-width" {
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

	marker := decodeSkipMarker(t, store.objects[objKey("optimized", ".s3-image-optimizer/skips/notes%2Fanim.gif.json")].body)
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

func TestWorkerWritesSkipMarkerForInsufficientSavings(t *testing.T) {
	store := newFakeStore()
	body := smallJPEG(t)
	source := storage.ObjectInfo{Key: "notes/tiny.jpg", Size: int64(len(body)), ETag: "tiny-etag", ContentType: "image/jpeg"}
	store.objects[objKey("source", source.Key)] = fakeObject{info: source, body: body}
	cfg := testWorkerConfig()
	cfg.MinBytes = 0

	w := New(store, cfg)
	if err := w.ProcessObject(context.Background(), source); err != nil {
		t.Fatalf("ProcessObject failed: %v", err)
	}

	marker := decodeSkipMarker(t, store.objects[objKey("optimized", ".s3-image-optimizer/skips/notes%2Ftiny.jpg.json")].body)
	if marker.Reason != "insufficient_savings" {
		t.Fatalf("expected insufficient_savings, got %q", marker.Reason)
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
	if _, ok := store.objects[objKey("optimized", "a.jpg")]; !ok {
		t.Fatal("expected a.jpg optimized object")
	}
	if _, ok := store.objects[objKey("optimized", "b.jpg")]; !ok {
		t.Fatal("expected b.jpg optimized object")
	}
}

type fakeObject struct {
	info storage.ObjectInfo
	body []byte
}

type fakeStore struct {
	objects    map[string]fakeObject
	getCalls   int
	putKeys    []string
	listBucket string
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: make(map[string]fakeObject)}
}

func (s *fakeStore) HeadObject(ctx context.Context, bucket, key string) (*storage.ObjectInfo, error) {
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
	s.listBucket = bucket
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
		OptimizationProfile: "v2-jpeg82-png-best-original-width",
		MaxWidth:            0,
		JPEGQuality:         82,
		MinBytes:            512,
	}
}

func largeJPEG(t *testing.T) []byte {
	t.Helper()
	return encodeJPEG(t, noisyImage(3000, 1200), 95)
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
