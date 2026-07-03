package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/xiaolutech/s3-image-optimizer/internal/imageopt"
	"github.com/xiaolutech/s3-image-optimizer/internal/storage"
)

const (
	sourceKeyMetadata         = "source-key"
	sourceETagMetadata        = "source-etag"
	profileMetadata           = "optimization-profile"
	sourceContentTypeMetadata = "source-content-type"
	variantFormatMetadata     = "variant-format"
	avifVariantFormat         = "avif"
	webpVariantFormat         = "webp"

	headObjectTimeout    = 45 * time.Second
	getObjectTimeout     = 120 * time.Second
	putObjectTimeout     = 120 * time.Second
	skipMarkerPutTimeout = 45 * time.Second
)

type Store interface {
	HeadObject(ctx context.Context, bucket, key string) (*storage.ObjectInfo, error)
	GetObject(ctx context.Context, bucket, key string) ([]byte, *storage.ObjectInfo, error)
	PutObject(ctx context.Context, bucket, key string, body []byte, opts storage.PutOptions) error
	ListObjects(ctx context.Context, bucket, prefix string, visit func(storage.ObjectInfo) error) error
	ListObjectsPage(ctx context.Context, bucket, prefix, startAfter string, maxKeys int32) (storage.ListPage, error)
}

type Config struct {
	SourceBucket          string
	OptimizedBucket       string
	OptimizationProfile   string
	MaxWidth              int
	JPEGQuality           int
	WebPQuality           int
	AVIFEnabled           bool
	AVIFTargetBytes       int64
	AVIFQualityMin        int
	AVIFQualityMax        int
	AVIFSpeed             int
	MinBytes              int64
	ProcessDelay          time.Duration
	ScanBatchSize         int
	ScanRetryAttempts     int
	ScanRetryInitialDelay time.Duration
	ScanRetryMaxDelay     time.Duration
}

type Worker struct {
	store       Store
	cfg         Config
	cursorMu    sync.Mutex
	scanLastKey string
}

type SkipMarker struct {
	SourceKey  string `json:"source_key"`
	SourceETag string `json:"source_etag"`
	Profile    string `json:"profile"`
	Reason     string `json:"reason"`
}

type ScanRoundResult struct {
	Processed int
	LastKey   string
	HasMore   bool
}

func New(store Store, cfg Config) *Worker {
	return &Worker{store: store, cfg: cfg}
}

func (w *Worker) RunOnce(ctx context.Context) error {
	attempts := w.cfg.ScanRetryAttempts
	if attempts <= 0 {
		attempts = 1
	}
	delay := w.cfg.ScanRetryInitialDelay

	for attempt := 1; attempt <= attempts; attempt++ {
		err := w.runOnce(ctx)
		if err == nil {
			return nil
		}
		var processErr processObjectError
		if errors.As(err, &processErr) {
			return processErr.err
		}
		if attempt == attempts {
			return err
		}

		log.Printf("scan attempt failed attempt=%d/%d retry_in=%s err=%v", attempt, attempts, delay, err)
		if err := wait(ctx, delay); err != nil {
			return err
		}
		delay = w.nextRetryDelay(delay)
	}
	return nil
}

func (w *Worker) runOnce(ctx context.Context) error {
	var processErr error
	return w.store.ListObjects(ctx, w.cfg.SourceBucket, "", func(info storage.ObjectInfo) error {
		processErr = w.ProcessObject(ctx, info)
		if processErr != nil {
			return processObjectError{err: processErr}
		}
		return nil
	})
}

func (w *Worker) RunScanRound(ctx context.Context) (ScanRoundResult, error) {
	batchSize := w.cfg.ScanBatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	startAfter := w.getScanLastKey()
	result := ScanRoundResult{}
	for result.Processed < batchSize {
		page, err := w.store.ListObjectsPage(ctx, w.cfg.SourceBucket, "", startAfter, int32(batchSize))
		if err != nil {
			return ScanRoundResult{}, err
		}
		if len(page.Objects) == 0 {
			result.HasMore = page.HasMore
			break
		}

		result.HasMore = page.HasMore
		for i, info := range page.Objects {
			counted, err := w.processObject(ctx, info)
			if err != nil {
				return result, processObjectError{err: err}
			}
			if counted {
				result.Processed++
			}
			result.LastKey = info.Key
			if result.Processed >= batchSize {
				result.HasMore = page.HasMore || i < len(page.Objects)-1
				break
			}
		}
		if !result.HasMore || result.Processed >= batchSize {
			break
		}
		startAfter = result.LastKey
	}

	if result.LastKey != "" && result.HasMore {
		w.setScanLastKey(result.LastKey)
	} else {
		w.setScanLastKey("")
	}
	return result, nil
}

type processObjectError struct {
	err error
}

func (e processObjectError) Error() string {
	return e.err.Error()
}

func (e processObjectError) Unwrap() error {
	return e.err
}

func (w *Worker) ProcessObject(ctx context.Context, source storage.ObjectInfo) error {
	_, err := w.processObject(ctx, source)
	return err
}

func (w *Worker) processObject(ctx context.Context, source storage.ObjectInfo) (bool, error) {
	if source.Size < w.cfg.MinBytes {
		log.Printf("skip small object key=%s size=%d", source.Key, source.Size)
		return true, nil
	}

	if err := w.waitForRequestDelay(ctx); err != nil {
		return false, err
	}

	optimizedKey := optimizedVariantKey(source.Key, w.outputVariantFormat())
	headCtx, headCancel := context.WithTimeout(ctx, headObjectTimeout)
	defer headCancel()
	optimized, err := w.store.HeadObject(headCtx, w.cfg.OptimizedBucket, optimizedKey)
	if err != nil && !isNotFound(err) {
		return false, fmt.Errorf("head optimized object %s: %w", optimizedKey, err)
	}
	optimizedFound := err == nil
	if optimizedFound && w.isCurrentOptimizedForSource(optimized, source) {
		log.Printf("skip current optimized object key=%s optimized_key=%s", source.Key, optimizedKey)
		return false, nil
	}

	if err := w.waitForRequestDelay(ctx); err != nil {
		return false, err
	}
	skipCtx, skipCancel := context.WithTimeout(ctx, headObjectTimeout)
	defer skipCancel()
	skipMarker, err := w.store.HeadObject(skipCtx, w.cfg.OptimizedBucket, skipMarkerKey(source.Key))
	if err != nil && !isNotFound(err) {
		return false, fmt.Errorf("head skip marker %s: %w", source.Key, err)
	}
	if err == nil && isCurrentOptimized(skipMarker, source, w.cfg.OptimizationProfile) {
		log.Printf("skip current skip marker key=%s", source.Key)
		return false, nil
	}

	if source.ContentType == "" {
		if err := w.waitForRequestDelay(ctx); err != nil {
			return false, err
		}
		sourceCtx, sourceCancel := context.WithTimeout(ctx, headObjectTimeout)
		defer sourceCancel()
		sourceInfo, err := w.store.HeadObject(sourceCtx, w.cfg.SourceBucket, source.Key)
		if err != nil {
			return false, fmt.Errorf("head source object %s: %w", source.Key, err)
		}
		source = *sourceInfo
	}
	if optimizedFound && w.isCurrentOptimizedForSource(optimized, source) {
		log.Printf("skip current optimized object key=%s optimized_key=%s", source.Key, optimizedKey)
		return false, nil
	}
	if !imageopt.IsSupportedContentType(source.ContentType) {
		return true, w.writeSkipMarker(ctx, source, "unsupported_content_type")
	}

	if err := w.waitForRequestDelay(ctx); err != nil {
		return false, err
	}
	getCtx, getCancel := context.WithTimeout(ctx, getObjectTimeout)
	defer getCancel()
	body, sourceInfo, err := w.store.GetObject(getCtx, w.cfg.SourceBucket, source.Key)
	if err != nil {
		return false, fmt.Errorf("get source object %s: %w", source.Key, err)
	}
	if sourceInfo != nil {
		source = *sourceInfo
	}

	result, err := imageopt.Optimize(body, source.ContentType, imageopt.Options{
		MaxWidth:        w.cfg.MaxWidth,
		JPEGQuality:     w.cfg.JPEGQuality,
		WebPQuality:     w.cfg.WebPQuality,
		MinSavings:      0.05,
		AVIFEnabled:     w.cfg.AVIFEnabled,
		AVIFTargetBytes: w.cfg.AVIFTargetBytes,
		AVIFQualityMin:  w.cfg.AVIFQualityMin,
		AVIFQualityMax:  w.cfg.AVIFQualityMax,
		AVIFSpeed:       w.cfg.AVIFSpeed,
	})
	if err != nil {
		return false, fmt.Errorf("optimize %s: %w", source.Key, err)
	}
	if result.Skipped {
		return true, w.writeSkipMarker(ctx, source, result.Reason)
	}

	if err := w.waitForRequestDelay(ctx); err != nil {
		return false, err
	}
	putCtx, putCancel := context.WithTimeout(ctx, putObjectTimeout)
	defer putCancel()
	metadata := map[string]string{
		sourceETagMetadata:        source.ETag,
		profileMetadata:           w.cfg.OptimizationProfile,
		sourceKeyMetadata:         source.Key,
		sourceContentTypeMetadata: source.ContentType,
		variantFormatMetadata:     w.outputVariantFormat(),
	}
	putKey := optimizedVariantKey(source.Key, w.outputVariantFormat())
	if err := w.store.PutObject(putCtx, w.cfg.OptimizedBucket, putKey, result.Body, storage.PutOptions{
		ContentType: result.ContentType,
		Metadata:    metadata,
	}); err != nil {
		return false, fmt.Errorf("put optimized object %s: %w", putKey, err)
	}
	log.Printf("optimized object key=%s optimized_key=%s source_etag=%s", source.Key, putKey, source.ETag)
	return true, nil
}

func (w *Worker) waitForRequestDelay(ctx context.Context) error {
	return wait(ctx, w.cfg.ProcessDelay)
}

func (w *Worker) nextRetryDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	next := delay * 2
	if next < delay {
		next = delay
	}
	if w.cfg.ScanRetryMaxDelay > 0 && next > w.cfg.ScanRetryMaxDelay {
		return w.cfg.ScanRetryMaxDelay
	}
	return next
}

func (w *Worker) getScanLastKey() string {
	w.cursorMu.Lock()
	defer w.cursorMu.Unlock()
	return w.scanLastKey
}

func (w *Worker) setScanLastKey(key string) {
	w.cursorMu.Lock()
	defer w.cursorMu.Unlock()
	w.scanLastKey = key
}

func wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isCurrentOptimized(optimized *storage.ObjectInfo, source storage.ObjectInfo, profile string) bool {
	if optimized == nil {
		return false
	}
	return optimized.Metadata[sourceETagMetadata] == source.ETag &&
		optimized.Metadata[profileMetadata] == profile
}

func (w *Worker) isCurrentOptimizedForSource(optimized *storage.ObjectInfo, source storage.ObjectInfo) bool {
	if !isCurrentOptimized(optimized, source, w.cfg.OptimizationProfile) {
		return false
	}
	return optimized.ContentType == w.outputContentType() &&
		optimized.Metadata[sourceKeyMetadata] == source.Key &&
		optimized.Metadata[sourceContentTypeMetadata] == source.ContentType &&
		optimized.Metadata[variantFormatMetadata] == w.outputVariantFormat()
}

func (w *Worker) outputVariantFormat() string {
	if w.cfg.AVIFEnabled {
		return avifVariantFormat
	}
	return webpVariantFormat
}

func (w *Worker) outputContentType() string {
	if w.cfg.AVIFEnabled {
		return imageopt.ContentTypeAVIF
	}
	return imageopt.ContentTypeWEBP
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
	if err := w.waitForRequestDelay(ctx); err != nil {
		return err
	}
	markerCtx, markerCancel := context.WithTimeout(ctx, skipMarkerPutTimeout)
	defer markerCancel()
	err = w.store.PutObject(markerCtx, w.cfg.OptimizedBucket, key, body, storage.PutOptions{
		ContentType: "application/json",
		Metadata: map[string]string{
			sourceETagMetadata: source.ETag,
			profileMetadata:    w.cfg.OptimizationProfile,
		},
	})
	if err != nil {
		return fmt.Errorf("put skip marker %s: %w", key, err)
	}
	log.Printf("wrote skip marker key=%s reason=%s", source.Key, reason)
	return nil
}

func skipMarkerKey(sourceKey string) string {
	sum := sha256.Sum256([]byte(sourceKey))
	return ".s3-image-optimizer/skips/" + hex.EncodeToString(sum[:]) + ".json"
}

func optimizedVariantKey(sourceKey, format string) string {
	ext := path.Ext(sourceKey)
	if ext == "" {
		return sourceKey + "." + format
	}
	return strings.TrimSuffix(sourceKey, ext) + "." + format
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
