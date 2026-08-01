package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"time"

	"github.com/xiaolutech/s3-image-sidecar/internal/imageopt"
	slog "github.com/xiaolutech/s3-image-sidecar/internal/log"
	"github.com/xiaolutech/s3-image-sidecar/internal/storage"
)

const (
	sourceKeyMetadata         = "source-key"
	sourceETagMetadata        = "source-etag"
	profileMetadata           = "optimization-profile"
	sourceContentTypeMetadata = "source-content-type"
	variantFormatMetadata     = "variant-format"
	configSignatureMetadata   = "config-signature"
	avifVariantFormat         = "avif"
	webpVariantFormat         = "webp"

	getObjectTimeout = 120 * time.Second
	putObjectTimeout = 120 * time.Second
	manifestTimeout  = 45 * time.Second
)

type Store interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, *storage.ObjectInfo, error)
	PutObject(ctx context.Context, bucket, key string, body []byte, opts storage.PutOptions) error
	ListObjects(ctx context.Context, bucket, prefix string, visit func(storage.ObjectInfo) error) error
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
	ScanBatchSize         int
	ScanRetryAttempts     int
	ScanRetryInitialDelay time.Duration
	ScanRetryMaxDelay     time.Duration
}

type Worker struct {
	store Store
	cfg   Config

	configSignature string

	passStarted    bool
	passManifest   *Manifest
	manifestDirty  bool
	pendingObjects []storage.ObjectInfo

	logger *slog.Logger
}

type Manifest struct {
	Profile         string           `json:"profile"`
	ConfigSignature string           `json:"config_signature"`
	Fingerprint     string           `json:"fingerprint"`
	Objects         []ManifestObject `json:"objects"`
}

type ManifestObject struct {
	Key  string `json:"key"`
	ETag string `json:"etag"`
	Size int64  `json:"size"`
}

type ScanRoundResult struct {
	Processed int
	LastKey   string
	HasMore   bool
}

func (c Config) Signature() string {
	h := sha256.New()
	fmt.Fprintf(h, "profile=%s\n", c.OptimizationProfile)
	fmt.Fprintf(h, "max_width=%d\n", c.MaxWidth)
	fmt.Fprintf(h, "jpeg_quality=%d\n", c.JPEGQuality)
	fmt.Fprintf(h, "webp_quality=%d\n", c.WebPQuality)
	fmt.Fprintf(h, "min_bytes=%d\n", c.MinBytes)
	fmt.Fprintf(h, "avif_enabled=%t\n", c.AVIFEnabled)
	fmt.Fprintf(h, "avif_target_bytes=%d\n", c.AVIFTargetBytes)
	fmt.Fprintf(h, "avif_quality_min=%d\n", c.AVIFQualityMin)
	fmt.Fprintf(h, "avif_quality_max=%d\n", c.AVIFQualityMax)
	fmt.Fprintf(h, "avif_speed=%d\n", c.AVIFSpeed)
	return hex.EncodeToString(h.Sum(nil))
}

func New(store Store, cfg Config, logger *slog.Logger) *Worker {
	return &Worker{store: store, cfg: cfg, configSignature: cfg.Signature(), logger: logger}
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

		w.logger.Warn("scan attempt failed attempt=%d/%d retry_in=%s err=%v", attempt, attempts, delay, err)
		if err := wait(ctx, delay); err != nil {
			return err
		}
		delay = w.nextRetryDelay(delay)
	}
	return nil
}

func (w *Worker) runOnce(ctx context.Context) error {
	if err := w.startPass(ctx); err != nil {
		return err
	}
	for len(w.pendingObjects) > 0 {
		info := w.pendingObjects[0]
		w.pendingObjects = w.pendingObjects[1:]
		if _, err := w.processObject(ctx, info); err != nil {
			return processObjectError{err: err}
		}
	}
	if w.manifestDirty {
		if err := w.saveManifest(ctx, w.passManifest); err != nil {
			return err
		}
	}
	w.logger.Info("run once completed objects=%d fingerprint=%s", len(w.passManifest.Objects), w.passManifest.Fingerprint)
	w.resetPass()
	return nil
}

func (w *Worker) RunScanRound(ctx context.Context) (ScanRoundResult, error) {
	batchSize := w.cfg.ScanBatchSize
	if batchSize <= 0 {
		batchSize = 200
	}

	if !w.passStarted {
		if err := w.startPass(ctx); err != nil {
			return ScanRoundResult{}, err
		}
	}

	result := ScanRoundResult{}
	processed := 0
	for processed < batchSize && len(w.pendingObjects) > 0 {
		info := w.pendingObjects[0]
		w.pendingObjects = w.pendingObjects[1:]
		counted, err := w.processObject(ctx, info)
		if err != nil {
			w.pendingObjects = append([]storage.ObjectInfo{info}, w.pendingObjects...)
			return result, processObjectError{err: err}
		}
		if counted {
			result.Processed++
		}
		result.LastKey = info.Key
		processed++
	}

	if len(w.pendingObjects) == 0 {
		if w.manifestDirty {
			if err := w.saveManifest(ctx, w.passManifest); err != nil {
				w.logger.Warn("save manifest failed: %v", err)
			}
		}
		w.resetPass()
		return result, nil
	}

	result.HasMore = true
	return result, nil
}

func (w *Worker) resetPass() {
	w.passStarted = false
	w.passManifest = nil
	w.pendingObjects = nil
	w.manifestDirty = false
}

func (w *Worker) startPass(ctx context.Context) error {
	newManifest := &Manifest{Profile: w.cfg.OptimizationProfile, ConfigSignature: w.configSignature}
	fingerprint := sha256.New()
	err := w.store.ListObjects(ctx, w.cfg.SourceBucket, "", func(info storage.ObjectInfo) error {
		newManifest.Objects = append(newManifest.Objects, ManifestObject{Key: info.Key, ETag: info.ETag, Size: info.Size})
		writeManifestFingerprint(fingerprint, info)
		return nil
	})
	if err != nil {
		return fmt.Errorf("list source objects: %w", err)
	}
	newManifest.Fingerprint = hex.EncodeToString(fingerprint.Sum(nil))

	stored, err := w.loadManifest(ctx)
	if err != nil {
		return err
	}

	w.passManifest = newManifest
	w.passStarted = true
	w.pendingObjects = nil

	configChanged := stored == nil || stored.ConfigSignature != newManifest.ConfigSignature
	fingerprintChanged := stored == nil || stored.Fingerprint != newManifest.Fingerprint

	switch {
	case configChanged:
		w.pendingObjects = diffManifests(nil, newManifest)
		w.manifestDirty = true
		w.logger.Info("optimization config changed fingerprint=%s objects=%d", newManifest.Fingerprint, len(newManifest.Objects))
	case fingerprintChanged:
		w.pendingObjects = diffManifests(stored, newManifest)
		w.manifestDirty = true
		w.logger.Info("manifest changed fingerprint=%s objects=%d changed=%d", newManifest.Fingerprint, len(newManifest.Objects), len(w.pendingObjects))
	default:
		w.manifestDirty = false
		w.logger.Debug("manifest unchanged fingerprint=%s objects=%d", newManifest.Fingerprint, len(newManifest.Objects))
	}
	return nil
}

func (w *Worker) loadManifest(ctx context.Context) (*Manifest, error) {
	getCtx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()
	body, _, err := w.store.GetObject(getCtx, w.cfg.OptimizedBucket, manifestKey(w.cfg.OptimizationProfile))
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

func (w *Worker) saveManifest(ctx context.Context, m *Manifest) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	putCtx, cancel := context.WithTimeout(ctx, manifestTimeout)
	defer cancel()
	if err := w.store.PutObject(putCtx, w.cfg.OptimizedBucket, manifestKey(w.cfg.OptimizationProfile), body, storage.PutOptions{
		ContentType: "application/json",
	}); err != nil {
		return fmt.Errorf("put manifest: %w", err)
	}
	return nil
}

func writeManifestFingerprint(h hash.Hash, info storage.ObjectInfo) {
	h.Write([]byte(info.Key))
	h.Write([]byte{0})
	h.Write([]byte(info.ETag))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(info.Size, 10)))
	h.Write([]byte{0})
}

func diffManifests(prev, next *Manifest) []storage.ObjectInfo {
	if prev == nil || len(prev.Objects) == 0 {
		changed := make([]storage.ObjectInfo, 0, len(next.Objects))
		for _, o := range next.Objects {
			changed = append(changed, storage.ObjectInfo{Key: o.Key, ETag: o.ETag, Size: o.Size})
		}
		return changed
	}
	prevByKey := make(map[string]ManifestObject, len(prev.Objects))
	for _, o := range prev.Objects {
		prevByKey[o.Key] = o
	}
	var changed []storage.ObjectInfo
	for _, o := range next.Objects {
		prevObj, ok := prevByKey[o.Key]
		if !ok || prevObj.ETag != o.ETag || prevObj.Size != o.Size {
			changed = append(changed, storage.ObjectInfo{Key: o.Key, ETag: o.ETag, Size: o.Size})
		}
	}
	return changed
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
		w.logger.Debug("skip small object key=%s size=%d", source.Key, source.Size)
		return true, nil
	}

	getCtx, cancel := context.WithTimeout(ctx, getObjectTimeout)
	defer cancel()
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
		w.logger.Debug("skip object key=%s reason=%s", source.Key, result.Reason)
		return true, nil
	}

	putCtx, putCancel := context.WithTimeout(ctx, putObjectTimeout)
	defer putCancel()
	metadata := map[string]string{
		sourceETagMetadata:        source.ETag,
		profileMetadata:           w.cfg.OptimizationProfile,
		sourceKeyMetadata:         source.Key,
		sourceContentTypeMetadata: source.ContentType,
		variantFormatMetadata:     w.outputVariantFormat(),
		configSignatureMetadata:   w.configSignature,
	}
	putKey := optimizedVariantKey(source.Key, w.outputVariantFormat())
	if err := w.store.PutObject(putCtx, w.cfg.OptimizedBucket, putKey, result.Body, storage.PutOptions{
		ContentType: result.ContentType,
		Metadata:    metadata,
	}); err != nil {
		return false, fmt.Errorf("put optimized object %s: %w", putKey, err)
	}
	w.logger.Info("optimized object key=%s optimized_key=%s source_etag=%s", source.Key, putKey, source.ETag)
	return true, nil
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

func manifestKey(profile string) string {
	return ".s3-image-sidecar/manifest/" + profile + ".json"
}

func optimizedVariantKey(sourceKey, format string) string {
	return sourceKey + "." + format
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
