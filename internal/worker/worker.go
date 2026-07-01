package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"

	"github.com/xiaolutech/s3-image-optimizer/internal/imageopt"
	"github.com/xiaolutech/s3-image-optimizer/internal/storage"
)

const (
	sourceETagMetadata = "source-etag"
	profileMetadata    = "optimization-profile"
)

type Store interface {
	HeadObject(ctx context.Context, bucket, key string) (*storage.ObjectInfo, error)
	GetObject(ctx context.Context, bucket, key string) ([]byte, *storage.ObjectInfo, error)
	PutObject(ctx context.Context, bucket, key string, body []byte, opts storage.PutOptions) error
	ListObjects(ctx context.Context, bucket, prefix string, visit func(storage.ObjectInfo) error) error
}

type Config struct {
	SourceBucket        string
	OptimizedBucket     string
	OptimizationProfile string
	MaxWidth            int
	JPEGQuality         int
	MinBytes            int64
}

type Worker struct {
	store Store
	cfg   Config
}

type SkipMarker struct {
	SourceKey  string `json:"source_key"`
	SourceETag string `json:"source_etag"`
	Profile    string `json:"profile"`
	Reason     string `json:"reason"`
}

func New(store Store, cfg Config) *Worker {
	return &Worker{store: store, cfg: cfg}
}

func (w *Worker) RunOnce(ctx context.Context) error {
	return w.store.ListObjects(ctx, w.cfg.SourceBucket, "", func(info storage.ObjectInfo) error {
		return w.ProcessObject(ctx, info)
	})
}

func (w *Worker) ProcessObject(ctx context.Context, source storage.ObjectInfo) error {
	if source.Size < w.cfg.MinBytes {
		log.Printf("skip small object key=%s size=%d", source.Key, source.Size)
		return nil
	}

	optimized, err := w.store.HeadObject(ctx, w.cfg.OptimizedBucket, source.Key)
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("head optimized object %s: %w", source.Key, err)
	}
	if err == nil && isCurrentOptimized(optimized, source, w.cfg.OptimizationProfile) {
		log.Printf("skip current optimized object key=%s", source.Key)
		return nil
	}

	body, sourceInfo, err := w.store.GetObject(ctx, w.cfg.SourceBucket, source.Key)
	if err != nil {
		return fmt.Errorf("get source object %s: %w", source.Key, err)
	}
	if sourceInfo != nil {
		source = *sourceInfo
	}

	result, err := imageopt.Optimize(body, source.ContentType, imageopt.Options{
		MaxWidth:    w.cfg.MaxWidth,
		JPEGQuality: w.cfg.JPEGQuality,
		MinSavings:  0.05,
	})
	if err != nil {
		return fmt.Errorf("optimize %s: %w", source.Key, err)
	}
	if result.Skipped {
		return w.writeSkipMarker(ctx, source, result.Reason)
	}

	if err := w.store.PutObject(ctx, w.cfg.OptimizedBucket, source.Key, result.Body, storage.PutOptions{
		ContentType: result.ContentType,
		Metadata: map[string]string{
			sourceETagMetadata: source.ETag,
			profileMetadata:    w.cfg.OptimizationProfile,
		},
	}); err != nil {
		return fmt.Errorf("put optimized object %s: %w", source.Key, err)
	}
	log.Printf("optimized object key=%s source_etag=%s", source.Key, source.ETag)
	return nil
}

func isCurrentOptimized(optimized *storage.ObjectInfo, source storage.ObjectInfo, profile string) bool {
	if optimized == nil {
		return false
	}
	return optimized.Metadata[sourceETagMetadata] == source.ETag &&
		optimized.Metadata[profileMetadata] == profile
}

func (w *Worker) writeSkipMarker(ctx context.Context, source storage.ObjectInfo, reason string) error {
	marker := SkipMarker{
		SourceKey:  source.Key,
		SourceETag: source.ETag,
		Profile:    w.cfg.OptimizationProfile,
		Reason:     reason,
	}
	body, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("marshal skip marker: %w", err)
	}
	key := skipMarkerKey(source.Key)
	if err := w.store.PutObject(ctx, w.cfg.OptimizedBucket, key, body, storage.PutOptions{
		ContentType: "application/json",
		Metadata: map[string]string{
			sourceETagMetadata: source.ETag,
			profileMetadata:    w.cfg.OptimizationProfile,
		},
	}); err != nil {
		return fmt.Errorf("put skip marker %s: %w", key, err)
	}
	log.Printf("wrote skip marker key=%s reason=%s", source.Key, reason)
	return nil
}

func skipMarkerKey(sourceKey string) string {
	return ".s3-image-optimizer/skips/" + url.PathEscape(sourceKey) + ".json"
}

type notFoundError interface {
	NotFound() bool
}

func isNotFound(err error) bool {
	var marker notFoundError
	if errors.As(err, &marker) && marker.NotFound() {
		return true
	}
	return storage.IsNotFound(err)
}
