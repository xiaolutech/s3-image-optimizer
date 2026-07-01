package imageopt

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strings"

	"golang.org/x/image/draw"
)

const (
	ContentTypeJPEG = "image/jpeg"
	ContentTypePNG  = "image/png"
)

type Options struct {
	MaxWidth    int
	JPEGQuality int
	MinSavings  float64
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

	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("decode image: %w", err)
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if opts.MaxWidth > 0 && width > opts.MaxWidth {
		height = scaledHeight(width, height, opts.MaxWidth)
		width = opts.MaxWidth
		img = resize(img, width, height)
	}

	encoded, err := encode(img, mediaType, opts)
	if err != nil {
		return Result{}, err
	}
	if float64(len(encoded)) >= float64(len(body))*(1-opts.MinSavings) {
		result := skipped("insufficient_savings")
		result.Width = width
		result.Height = height
		result.ContentType = mediaType
		return result, nil
	}

	return Result{
		Body:        encoded,
		ContentType: mediaType,
		Width:       width,
		Height:      height,
	}, nil
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
