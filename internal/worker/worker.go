package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/xiaolutech/s3-image-optimizer/internal/imageopt"
	"github.com/xiaolutech/s3-image-optimizer/internal/storage"
)

const (
	sourceETagMetadata = "source-etag"
	profileMetadata    = "optimization-profile"

	headObjectTimeout      = 45 * time.Second
	getObjectTimeout      = 120 * time.Second
	putObjectTimeout      = 120 * time.Second
	skipMarkerPutTimeout  = 45 * time.Second
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
	objStart := time.Now()
	log.Printf("process object start key=%s size=%d content_type=%s", source.Key, source.Size, source.ContentType)

	if source.Size < w.cfg.MinBytes {
		log.Printf("skip small object key=%s size=%d", source.Key, source.Size)
		return nil
	}

	headCtx, headCancel := context.WithTimeout(ctx, headObjectTimeout)
	defer headCancel()
	headStart := time.Now()
	optimized, err := w.store.HeadObject(headCtx, w.cfg.OptimizedBucket, source.Key)
	if err != nil {
		log.Printf("head optimized key=%s duration=%s err=%v", source.Key, time.Since(headStart), err)
	} else {
		log.Printf("head optimized key=%s duration=%s etag=%q found=true", source.Key, time.Since(headStart), optimized.ETag)
	}
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("head optimized object %s: %w", source.Key, err)
	}
	if err == nil && isCurrentOptimized(optimized, source, w.cfg.OptimizationProfile) {
		log.Printf("skip current optimized object key=%s", source.Key)
		return nil
	}

	getCtx, getCancel := context.WithTimeout(ctx, getObjectTimeout)
	defer getCancel()
	getStart := time.Now()
	body, sourceInfo, err := w.store.GetObject(getCtx, w.cfg.SourceBucket, source.Key)
	if err != nil {
		log.Printf("get source key=%s duration=%s err=%v", source.Key, time.Since(getStart), err)
	} else {
		log.Printf("get source key=%s duration=%s size=%d", source.Key, time.Since(getStart), len(body))
	}
	if err != nil {
		return fmt.Errorf("get source object %s: %w", source.Key, err)
	}
	if sourceInfo != nil {
		source = *sourceInfo
	}

	optimizeStart := time.Now()
	result, err := imageopt.Optimize(body, source.ContentType, imageopt.Options{
		MaxWidth:    w.cfg.MaxWidth,
		JPEGQuality: w.cfg.JPEGQuality,
		MinSavings:  0.05,
	})
	log.Printf(
		"optimize done key=%s duration=%s skipped=%t reason=%s out_bytes=%d",
		source.Key,
		time.Since(optimizeStart),
		result.Skipped,
		result.Reason,
		len(result.Body),
	)
	if err != nil {
		return fmt.Errorf("optimize %s: %w", source.Key, err)
	}
	if result.Skipped {
		log.Printf("skipping object key=%s due to %s", source.Key, result.Reason)
		return w.writeSkipMarker(ctx, source, result.Reason)
	}

	putCtx, putCancel := context.WithTimeout(ctx, putObjectTimeout)
	defer putCancel()
	putStart := time.Now()
	if err := w.store.PutObject(putCtx, w.cfg.OptimizedBucket, source.Key, result.Body, storage.PutOptions{
		ContentType: result.ContentType,
		Metadata: map[string]string{
			sourceETagMetadata: source.ETag,
			profileMetadata:    w.cfg.OptimizationProfile,
		},
	}); err != nil {
		log.Printf("put optimized key=%s duration=%s err=%v", source.Key, time.Since(putStart), err)
		return fmt.Errorf("put optimized object %s: %w", source.Key, err)
	}
	log.Printf("put optimized key=%s duration=%s", source.Key, time.Since(putStart))
	log.Printf("process object complete key=%s total_duration=%s source_etag=%s", source.Key, time.Since(objStart), source.ETag)
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
	markerCtx, markerCancel := context.WithTimeout(ctx, skipMarkerPutTimeout)
	defer markerCancel()
	markerStart := time.Now()
	err = w.store.PutObject(markerCtx, w.cfg.OptimizedBucket, key, body, storage.PutOptions{
		ContentType: "application/json",
		Metadata: map[string]string{
			sourceETagMetadata: source.ETag,
			profileMetadata:    w.cfg.OptimizationProfile,
		},
	})
	if err != nil {
		log.Printf("put skip marker key=%s duration=%s err=%v", key, time.Since(markerStart), err)
		return fmt.Errorf("put skip marker %s: %w", key, err)
	}
	log.Printf("put skip marker key=%s duration=%s", key, time.Since(markerStart))
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
