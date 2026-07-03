package imageopt

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestOptimizeJPEGResizesAndKeepsFormat(t *testing.T) {
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
	if result.ContentType != "image/jpeg" {
		t.Fatalf("expected image/jpeg, got %q", result.ContentType)
	}
	if result.Width != 1920 {
		t.Fatalf("expected width 1920, got %d", result.Width)
	}
	if result.Height != 768 {
		t.Fatalf("expected height 768, got %d", result.Height)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(result.Body))
	if err != nil {
		t.Fatalf("decode optimized jpeg: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("expected jpeg format, got %s", format)
	}
	if cfg.Width != 1920 || cfg.Height != 768 {
		t.Fatalf("unexpected decoded dimensions %dx%d", cfg.Width, cfg.Height)
	}
}

func TestOptimizePNGResizesAndKeepsFormat(t *testing.T) {
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
	if result.ContentType != "image/png" {
		t.Fatalf("expected image/png, got %q", result.ContentType)
	}
	if result.Width != 1920 {
		t.Fatalf("expected width 1920, got %d", result.Width)
	}
	if result.Height != 800 {
		t.Fatalf("expected height 800, got %d", result.Height)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(result.Body))
	if err != nil {
		t.Fatalf("decode optimized png: %v", err)
	}
	if format != "png" {
		t.Fatalf("expected png format, got %s", format)
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
		t.Fatalf("decode optimized jpeg: %v", err)
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
