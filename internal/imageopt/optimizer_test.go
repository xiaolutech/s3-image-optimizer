package imageopt

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	_ "github.com/gen2brain/avif"
	_ "golang.org/x/image/webp"
)

func TestOptimizeJPEGResizesAndWritesWebP(t *testing.T) {
	input := encodeJPEG(t, gradientImage(3000, 1200), 95)

	result, err := Optimize(input, "image/jpeg", Options{
		MaxWidth:    1920,
		JPEGQuality: 82,
		MinSavings:  0,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}
	if result.Skipped {
		t.Fatalf("expected optimized JPEG, skipped with %s", result.Reason)
	}
	if result.ContentType != "image/webp" {
		t.Fatalf("expected image/webp, got %q", result.ContentType)
	}
	if result.Width != 1920 {
		t.Fatalf("expected width 1920, got %d", result.Width)
	}
	if result.Height != 768 {
		t.Fatalf("expected height 768, got %d", result.Height)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(result.Body))
	if err != nil {
		t.Fatalf("decode optimized webp: %v", err)
	}
	if format != "webp" {
		t.Fatalf("expected webp format, got %s", format)
	}
	if cfg.Width != 1920 || cfg.Height != 768 {
		t.Fatalf("unexpected decoded dimensions %dx%d", cfg.Width, cfg.Height)
	}
}

func TestOptimizePNGResizesAndWritesWebP(t *testing.T) {
	input := encodePNG(t, noisyImage(2400, 1000))

	result, err := Optimize(input, "image/png", Options{
		MaxWidth:    1920,
		JPEGQuality: 82,
		MinSavings:  0,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}
	if result.Skipped {
		t.Fatalf("expected optimized PNG, skipped with %s", result.Reason)
	}
	if result.ContentType != "image/webp" {
		t.Fatalf("expected image/webp, got %q", result.ContentType)
	}
	if result.Width != 1920 {
		t.Fatalf("expected width 1920, got %d", result.Width)
	}
	if result.Height != 800 {
		t.Fatalf("expected height 800, got %d", result.Height)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(result.Body))
	if err != nil {
		t.Fatalf("decode optimized webp: %v", err)
	}
	if format != "webp" {
		t.Fatalf("expected webp format, got %s", format)
	}
	if cfg.Width != 1920 || cfg.Height != 800 {
		t.Fatalf("unexpected decoded dimensions %dx%d", cfg.Width, cfg.Height)
	}
}

func TestOptimizeWithZeroMaxWidthKeepsOriginalDimensions(t *testing.T) {
	input := encodeJPEG(t, gradientImage(3000, 1200), 95)

	result, err := Optimize(input, "image/jpeg", Options{
		MaxWidth:    0,
		JPEGQuality: 82,
		MinSavings:  0,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}
	if result.Skipped {
		t.Fatalf("expected optimized JPEG, skipped with %s", result.Reason)
	}
	if result.Width != 3000 {
		t.Fatalf("expected width 3000, got %d", result.Width)
	}
	if result.Height != 1200 {
		t.Fatalf("expected height 1200, got %d", result.Height)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(result.Body))
	if err != nil {
		t.Fatalf("decode optimized webp: %v", err)
	}
	if cfg.Width != 3000 || cfg.Height != 1200 {
		t.Fatalf("unexpected decoded dimensions %dx%d", cfg.Width, cfg.Height)
	}
}

func TestOptimizeSkipsUnsupportedContentType(t *testing.T) {
	var buf bytes.Buffer
	if err := gif.Encode(&buf, gradientImage(100, 100), nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}

	result, err := Optimize(buf.Bytes(), "image/gif", Options{
		MaxWidth:    1920,
		JPEGQuality: 82,
		MinSavings:  0.05,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}
	if !result.Skipped {
		t.Fatal("expected unsupported image to be skipped")
	}
	if result.Reason != "unsupported_content_type" {
		t.Fatalf("expected unsupported_content_type, got %q", result.Reason)
	}
}

func TestOptimizeSkipsUndecodableSupportedContent(t *testing.T) {
	result, err := Optimize([]byte("not actually a jpeg"), "image/jpeg", Options{
		MaxWidth:    1920,
		JPEGQuality: 82,
		MinSavings:  0.05,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}
	if !result.Skipped {
		t.Fatal("expected undecodable image to be skipped")
	}
	if result.Reason != "decode_image_failed" {
		t.Fatalf("expected decode_image_failed, got %q", result.Reason)
	}
}

func TestOptimizeWebPSkipsDimensionsOutsideEncoderLimit(t *testing.T) {
	input := encodeJPEG(t, solidImage(1, 16384), 82)

	result, err := Optimize(input, "image/jpeg", Options{
		MaxWidth:    0,
		JPEGQuality: 82,
		WebPQuality: 82,
		MinSavings:  0.05,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}
	if !result.Skipped {
		t.Fatal("expected unsupported WebP dimensions to be skipped")
	}
	if result.Reason != "unsupported_dimensions" {
		t.Fatalf("expected unsupported_dimensions, got %q", result.Reason)
	}
}

func TestOptimizeSkipsInsufficientSavings(t *testing.T) {
	input := encodeJPEG(t, gradientImage(200, 100), 82)

	result, err := Optimize(input, "image/jpeg", Options{
		MaxWidth:    1920,
		JPEGQuality: 82,
		MinSavings:  0.95,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}
	if !result.Skipped {
		t.Fatal("expected image to be skipped for insufficient savings")
	}
	if result.Reason != "insufficient_savings" {
		t.Fatalf("expected insufficient_savings, got %q", result.Reason)
	}
}

func TestOptimizeWithAVIFOutputPreservesDimensions(t *testing.T) {
	input := encodePNG(t, noisyImage(640, 480))

	result, err := Optimize(input, "image/png", Options{
		MaxWidth:        0,
		JPEGQuality:     82,
		MinSavings:      0,
		AVIFEnabled:     true,
		AVIFTargetBytes: 1024 * 1024,
		AVIFQualityMin:  35,
		AVIFQualityMax:  75,
		AVIFSpeed:       8,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}
	if result.Skipped {
		t.Fatalf("expected AVIF output, skipped with %s", result.Reason)
	}
	if result.ContentType != "image/avif" {
		t.Fatalf("expected image/avif, got %q", result.ContentType)
	}
	if result.Width != 640 || result.Height != 480 {
		t.Fatalf("unexpected dimensions %dx%d", result.Width, result.Height)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(result.Body))
	if err != nil {
		t.Fatalf("decode optimized avif: %v", err)
	}
	if format != "avif" {
		t.Fatalf("expected avif format, got %s", format)
	}
	if cfg.Width != 640 || cfg.Height != 480 {
		t.Fatalf("unexpected decoded dimensions %dx%d", cfg.Width, cfg.Height)
	}
}

func TestOptimizeAVIFSkipsInsufficientSavings(t *testing.T) {
	input := encodePNG(t, gradientImage(16, 16))

	result, err := Optimize(input, "image/png", Options{
		MaxWidth:        0,
		JPEGQuality:     82,
		MinSavings:      0.99,
		AVIFEnabled:     true,
		AVIFTargetBytes: 1024,
		AVIFQualityMin:  35,
		AVIFQualityMax:  75,
		AVIFSpeed:       8,
	})
	if err != nil {
		t.Fatalf("Optimize failed: %v", err)
	}
	if !result.Skipped {
		t.Fatal("expected AVIF output to be skipped")
	}
	if result.Reason != "insufficient_savings" {
		t.Fatalf("expected insufficient_savings, got %q", result.Reason)
	}
}

func TestOptimizeAVIFOptionValidation(t *testing.T) {
	input := encodePNG(t, gradientImage(16, 16))

	tests := []struct {
		name      string
		mutate    func(*Options)
		wantError string
	}{
		{
			name:      "negative target bytes",
			mutate:    func(opts *Options) { opts.AVIFTargetBytes = -1 },
			wantError: "avif target bytes",
		},
		{
			name:      "quality min below bounds",
			mutate:    func(opts *Options) { opts.AVIFQualityMin = -1 },
			wantError: "avif quality min",
		},
		{
			name:      "quality min above bounds",
			mutate:    func(opts *Options) { opts.AVIFQualityMin = 101 },
			wantError: "avif quality min",
		},
		{
			name:      "quality max below bounds",
			mutate:    func(opts *Options) { opts.AVIFQualityMax = -1 },
			wantError: "avif quality max",
		},
		{
			name:      "quality max above bounds",
			mutate:    func(opts *Options) { opts.AVIFQualityMax = 101 },
			wantError: "avif quality max",
		},
		{
			name: "quality min greater than max",
			mutate: func(opts *Options) {
				opts.AVIFQualityMin = 76
				opts.AVIFQualityMax = 75
			},
			wantError: "avif quality min",
		},
		{
			name:      "speed below bounds",
			mutate:    func(opts *Options) { opts.AVIFSpeed = -1 },
			wantError: "avif speed",
		},
		{
			name:      "speed above bounds",
			mutate:    func(opts *Options) { opts.AVIFSpeed = 11 },
			wantError: "avif speed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := Options{
				MaxWidth:        0,
				JPEGQuality:     82,
				MinSavings:      0,
				AVIFEnabled:     true,
				AVIFTargetBytes: 1024,
				AVIFQualityMin:  35,
				AVIFQualityMax:  75,
				AVIFSpeed:       8,
			}
			tt.mutate(&opts)

			_, err := Optimize(input, "image/png", opts)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %q", tt.wantError, err.Error())
			}
		})
	}
}

func gradientImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8((x * 255) / width),
				G: uint8((y * 255) / height),
				B: uint8((x + y) % 256),
				A: 255,
			})
		}
	}
	return img
}

func noisyImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var state uint32 = 1
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			state = state*1664525 + 1013904223
			img.Set(x, y, color.RGBA{
				R: uint8(state >> 24),
				G: uint8(state >> 16),
				B: uint8(state >> 8),
				A: 255,
			})
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

func encodeJPEG(t *testing.T, img image.Image, quality int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}
