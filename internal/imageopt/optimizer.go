package imageopt

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strings"

	avif "github.com/gen2brain/avif"
	"golang.org/x/image/draw"
)

const (
	ContentTypeJPEG = "image/jpeg"
	ContentTypePNG  = "image/png"
	ContentTypeAVIF = "image/avif"
)

type Options struct {
	MaxWidth        int
	JPEGQuality     int
	MinSavings      float64
	AVIFEnabled     bool
	AVIFTargetBytes int64
	AVIFQualityMin  int
	AVIFQualityMax  int
	AVIFSpeed       int
}

type Result struct {
	Body        []byte
	ContentType string
	Width       int
	Height      int
	Skipped     bool
	Reason      string
}

func IsSupportedContentType(contentType string) bool {
	mediaType := normalizeContentType(contentType)
	return mediaType == ContentTypeJPEG || mediaType == ContentTypePNG
}

func Optimize(body []byte, contentType string, opts Options) (Result, error) {
	mediaType := normalizeContentType(contentType)
	if !IsSupportedContentType(mediaType) {
		return skipped("unsupported_content_type"), nil
	}
	if opts.MaxWidth < 0 {
		return Result{}, fmt.Errorf("max width cannot be negative")
	}
	if opts.JPEGQuality < 1 || opts.JPEGQuality > 100 {
		return Result{}, fmt.Errorf("jpeg quality must be between 1 and 100")
	}
	if opts.MinSavings < 0 || opts.MinSavings >= 1 {
		return Result{}, fmt.Errorf("min savings must be >= 0 and < 1")
	}
	if opts.AVIFEnabled {
		if err := validateAVIFOptions(opts); err != nil {
			return Result{}, err
		}
	}

	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return skipped("decode_image_failed"), nil
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if opts.MaxWidth > 0 && width > opts.MaxWidth {
		height = scaledHeight(width, height, opts.MaxWidth)
		width = opts.MaxWidth
		img = resize(img, width, height)
	}

	outputContentType := mediaType
	var encoded []byte
	if opts.AVIFEnabled {
		outputContentType = ContentTypeAVIF
		targetBytes, enforceTarget := avifSearchTarget(opts)
		encoded, err = encodeAVIF(img, opts, targetBytes, enforceTarget)
	} else {
		encoded, err = encode(img, mediaType, opts)
	}
	if err != nil {
		return Result{}, err
	}
	if hasInsufficientSavings(len(encoded), len(body), opts) {
		result := skipped("insufficient_savings")
		result.Width = width
		result.Height = height
		result.ContentType = outputContentType
		return result, nil
	}

	return Result{
		Body:        encoded,
		ContentType: outputContentType,
		Width:       width,
		Height:      height,
	}, nil
}

func validateAVIFOptions(opts Options) error {
	if opts.AVIFTargetBytes < 0 {
		return fmt.Errorf("avif target bytes cannot be negative")
	}
	if opts.AVIFQualityMin < 0 || opts.AVIFQualityMin > 100 {
		return fmt.Errorf("avif quality min must be between 0 and 100")
	}
	if opts.AVIFQualityMax < 0 || opts.AVIFQualityMax > 100 {
		return fmt.Errorf("avif quality max must be between 0 and 100")
	}
	if opts.AVIFQualityMin > opts.AVIFQualityMax {
		return fmt.Errorf("avif quality min cannot exceed avif quality max")
	}
	if opts.AVIFSpeed < 0 || opts.AVIFSpeed > 10 {
		return fmt.Errorf("avif speed must be between 0 and 10")
	}
	return nil
}

func hasInsufficientSavings(encodedBytes, originalBytes int, opts Options) bool {
	return float64(encodedBytes) >= float64(originalBytes)*(1-opts.MinSavings)
}

func skipped(reason string) Result {
	return Result{Skipped: true, Reason: reason}
}

func normalizeContentType(contentType string) string {
	mediaType := strings.ToLower(strings.TrimSpace(contentType))
	if idx := strings.Index(mediaType, ";"); idx >= 0 {
		mediaType = mediaType[:idx]
	}
	if mediaType == "image/jpg" {
		return ContentTypeJPEG
	}
	return strings.TrimSpace(mediaType)
}

func scaledHeight(width, height, maxWidth int) int {
	scaled := int(float64(height) * (float64(maxWidth) / float64(width)))
	if scaled < 1 {
		return 1
	}
	return scaled
}

func resize(src image.Image, width, height int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

func encode(img image.Image, contentType string, opts Options) ([]byte, error) {
	var buf bytes.Buffer
	switch contentType {
	case ContentTypeJPEG:
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: opts.JPEGQuality}); err != nil {
			return nil, fmt.Errorf("encode jpeg: %w", err)
		}
	case ContentTypePNG:
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		if err := encoder.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("encode png: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported content type %q", contentType)
	}
	return buf.Bytes(), nil
}

func avifSearchTarget(opts Options) (int64, bool) {
	if opts.AVIFTargetBytes == 0 {
		return 0, false
	}
	return opts.AVIFTargetBytes, true
}

func encodeAVIF(img image.Image, opts Options, targetBytes int64, enforceTarget bool) ([]byte, error) {
	minQuality := opts.AVIFQualityMin
	maxQuality := opts.AVIFQualityMax
	speed := opts.AVIFSpeed

	if !enforceTarget {
		return encodeAVIFAtQuality(img, maxQuality, speed)
	}

	encodedMin, err := encodeAVIFAtQuality(img, minQuality, speed)
	if err != nil {
		return nil, err
	}
	if int64(len(encodedMin)) > targetBytes {
		return encodedMin, nil
	}

	bestPassing := encodedMin
	low := minQuality + 1
	high := maxQuality
	for low <= high {
		quality := low + (high-low)/2
		encoded, err := encodeAVIFAtQuality(img, quality, speed)
		if err != nil {
			return nil, err
		}
		if int64(len(encoded)) <= targetBytes {
			bestPassing = encoded
			low = quality + 1
			continue
		}
		high = quality - 1
	}
	return bestPassing, nil
}

func encodeAVIFAtQuality(img image.Image, quality, speed int) ([]byte, error) {
	encodeQuality := quality
	if encodeQuality == 0 {
		encodeQuality = 1
	}

	var buf bytes.Buffer
	if err := avif.Encode(&buf, img, avif.Options{
		Quality:      encodeQuality,
		QualityAlpha: encodeQuality,
		Speed:        speed,
	}); err != nil {
		return nil, fmt.Errorf("encode avif: %w", err)
	}
	return buf.Bytes(), nil
}
