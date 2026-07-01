package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Port != "8080" {
		t.Fatalf("expected port 8080, got %q", cfg.Port)
	}
	if cfg.S3Region != "us-east-1" {
		t.Fatalf("expected default region us-east-1, got %q", cfg.S3Region)
	}
	if !cfg.S3UseSSL {
		t.Fatal("expected S3UseSSL true by default")
	}
	if cfg.SourceBucket != "" {
		t.Fatalf("expected empty source bucket, got %q", cfg.SourceBucket)
	}
	if cfg.OptimizedBucket != "" {
		t.Fatalf("expected empty optimized bucket, got %q", cfg.OptimizedBucket)
	}
	if cfg.OptimizationProfile != "v2-jpeg82-png-best-original-width" {
		t.Fatalf("unexpected profile %q", cfg.OptimizationProfile)
	}
	if cfg.MaxWidth != 0 {
		t.Fatalf("expected max width 0, got %d", cfg.MaxWidth)
	}
	if cfg.JPEGQuality != 82 {
		t.Fatalf("expected jpeg quality 82, got %d", cfg.JPEGQuality)
	}
	if cfg.MinBytes != 512*1024 {
		t.Fatalf("expected min bytes 524288, got %d", cfg.MinBytes)
	}
	if cfg.ScanInterval != 10*time.Minute {
		t.Fatalf("expected scan interval 10m, got %v", cfg.ScanInterval)
	}
	if cfg.RunOnce {
		t.Fatal("expected RunOnce false by default")
	}
}

func TestLoadFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("S3_ENDPOINT", "minio:9000")
	t.Setenv("S3_REGION", "us-west-2")
	t.Setenv("S3_ACCESS_KEY_ID", "key")
	t.Setenv("S3_SECRET_ACCESS_KEY", "secret")
	t.Setenv("S3_USE_SSL", "false")
	t.Setenv("SOURCE_BUCKET", "logseq-assets")
	t.Setenv("OPTIMIZED_BUCKET", "logseq-assets-optimized")
	t.Setenv("OPTIMIZATION_PROFILE", "v2-jpeg76-w2560")
	t.Setenv("MAX_WIDTH", "2560")
	t.Setenv("JPEG_QUALITY", "76")
	t.Setenv("MIN_BYTES", "262144")
	t.Setenv("SCAN_INTERVAL", "5m")
	t.Setenv("RUN_ONCE", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Port != "9090" {
		t.Fatalf("expected port 9090, got %q", cfg.Port)
	}
	if cfg.S3Endpoint != "minio:9000" || cfg.S3Region != "us-west-2" {
		t.Fatalf("unexpected s3 config: %#v", cfg)
	}
	if cfg.S3UseSSL {
		t.Fatal("expected S3UseSSL false")
	}
	if cfg.SourceBucket != "logseq-assets" || cfg.OptimizedBucket != "logseq-assets-optimized" {
		t.Fatalf("unexpected buckets: %#v", cfg)
	}
	if cfg.OptimizationProfile != "v2-jpeg76-w2560" {
		t.Fatalf("unexpected profile %q", cfg.OptimizationProfile)
	}
	if cfg.MaxWidth != 2560 || cfg.JPEGQuality != 76 || cfg.MinBytes != 262144 {
		t.Fatalf("unexpected optimization config: %#v", cfg)
	}
	if cfg.ScanInterval != 5*time.Minute {
		t.Fatalf("expected scan interval 5m, got %v", cfg.ScanInterval)
	}
	if !cfg.RunOnce {
		t.Fatal("expected RunOnce true")
	}
}

func TestValidateRequiresCoreFields(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Config)
		wantError string
	}{
		{
			name:      "missing endpoint",
			mutate:    func(cfg *Config) { cfg.S3Endpoint = "" },
			wantError: "S3_ENDPOINT",
		},
		{
			name:      "missing access key",
			mutate:    func(cfg *Config) { cfg.S3AccessKeyID = "" },
			wantError: "S3_ACCESS_KEY_ID",
		},
		{
			name:      "missing secret key",
			mutate:    func(cfg *Config) { cfg.S3SecretAccessKey = "" },
			wantError: "S3_SECRET_ACCESS_KEY",
		},
		{
			name:      "missing source bucket",
			mutate:    func(cfg *Config) { cfg.SourceBucket = "" },
			wantError: "SOURCE_BUCKET",
		},
		{
			name:      "missing optimized bucket",
			mutate:    func(cfg *Config) { cfg.OptimizedBucket = "" },
			wantError: "OPTIMIZED_BUCKET",
		},
		{
			name:      "missing profile",
			mutate:    func(cfg *Config) { cfg.OptimizationProfile = "" },
			wantError: "OPTIMIZATION_PROFILE",
		},
		{
			name:      "invalid max width",
			mutate:    func(cfg *Config) { cfg.MaxWidth = -1 },
			wantError: "MAX_WIDTH",
		},
		{
			name:      "invalid jpeg quality low",
			mutate:    func(cfg *Config) { cfg.JPEGQuality = 0 },
			wantError: "JPEG_QUALITY",
		},
		{
			name:      "invalid jpeg quality high",
			mutate:    func(cfg *Config) { cfg.JPEGQuality = 101 },
			wantError: "JPEG_QUALITY",
		},
		{
			name:      "negative min bytes",
			mutate:    func(cfg *Config) { cfg.MinBytes = -1 },
			wantError: "MIN_BYTES",
		},
		{
			name:      "invalid scan interval",
			mutate:    func(cfg *Config) { cfg.ScanInterval = 0 },
			wantError: "SCAN_INTERVAL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestLoadRejectsInvalidEnv(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{name: "invalid bool", key: "S3_USE_SSL", val: "not-bool"},
		{name: "invalid max width", key: "MAX_WIDTH", val: "wide"},
		{name: "invalid jpeg quality", key: "JPEG_QUALITY", val: "high"},
		{name: "invalid min bytes", key: "MIN_BYTES", val: "many"},
		{name: "invalid scan interval", key: "SCAN_INTERVAL", val: "soon"},
		{name: "invalid run once", key: "RUN_ONCE", val: "sometimes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			setValidEnv(t)
			t.Setenv(tt.key, tt.val)

			_, err := Load()
			if err == nil {
				t.Fatal("expected load error")
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("expected error containing %q, got %v", tt.key, err)
			}
		})
	}
}

func validConfig() *Config {
	cfg := DefaultConfig()
	cfg.S3Endpoint = "minio:9000"
	cfg.S3AccessKeyID = "key"
	cfg.S3SecretAccessKey = "secret"
	cfg.SourceBucket = "source"
	cfg.OptimizedBucket = "optimized"
	return cfg
}

func setValidEnv(t *testing.T) {
	t.Helper()
	t.Setenv("S3_ENDPOINT", "minio:9000")
	t.Setenv("S3_ACCESS_KEY_ID", "key")
	t.Setenv("S3_SECRET_ACCESS_KEY", "secret")
	t.Setenv("SOURCE_BUCKET", "source")
	t.Setenv("OPTIMIZED_BUCKET", "optimized")
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PORT",
		"S3_ENDPOINT",
		"S3_REGION",
		"S3_ACCESS_KEY_ID",
		"S3_SECRET_ACCESS_KEY",
		"S3_USE_SSL",
		"SOURCE_BUCKET",
		"OPTIMIZED_BUCKET",
		"OPTIMIZATION_PROFILE",
		"MAX_WIDTH",
		"JPEG_QUALITY",
		"MIN_BYTES",
		"SCAN_INTERVAL",
		"RUN_ONCE",
	} {
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}
}
