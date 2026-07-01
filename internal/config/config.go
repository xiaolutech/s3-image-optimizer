package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port string

	S3Endpoint        string
	S3Region          string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3UseSSL          bool

	SourceBucket    string
	OptimizedBucket string

	OptimizationProfile string
	MaxWidth            int
	JPEGQuality         int
	MinBytes            int64

	ScanInterval     time.Duration
	ScanEnabled      bool
	RunOnce          bool
	ProcessDelay     time.Duration
	TriggerQueueSize int

	ScanRetryAttempts     int
	ScanRetryInitialDelay time.Duration
	ScanRetryMaxDelay     time.Duration
}

func DefaultConfig() *Config {
	return &Config{
		Port:                  "8080",
		S3Region:              "us-east-1",
		S3UseSSL:              true,
		OptimizationProfile:   "v2-jpeg82-png-best-original-width",
		MaxWidth:              0,
		JPEGQuality:           82,
		MinBytes:              512 * 1024,
		ScanInterval:          24 * time.Hour,
		ScanEnabled:           false,
		ProcessDelay:          0,
		TriggerQueueSize:      256,
		ScanRetryAttempts:     8,
		ScanRetryInitialDelay: 5 * time.Second,
		ScanRetryMaxDelay:     2 * time.Minute,
	}
}

func Load() (*Config, error) {
	cfg := DefaultConfig()
	cfg.Port = getenv("PORT", cfg.Port)
	cfg.S3Endpoint = getenv("S3_ENDPOINT", cfg.S3Endpoint)
	cfg.S3Region = getenv("S3_REGION", cfg.S3Region)
	cfg.S3AccessKeyID = getenv("S3_ACCESS_KEY_ID", cfg.S3AccessKeyID)
	cfg.S3SecretAccessKey = getenv("S3_SECRET_ACCESS_KEY", cfg.S3SecretAccessKey)
	cfg.SourceBucket = getenv("SOURCE_BUCKET", cfg.SourceBucket)
	cfg.OptimizedBucket = getenv("OPTIMIZED_BUCKET", cfg.OptimizedBucket)
	cfg.OptimizationProfile = getenv("OPTIMIZATION_PROFILE", cfg.OptimizationProfile)

	var err error
	if cfg.S3UseSSL, err = getenvBool("S3_USE_SSL", cfg.S3UseSSL); err != nil {
		return nil, err
	}
	if cfg.MaxWidth, err = getenvInt("MAX_WIDTH", cfg.MaxWidth); err != nil {
		return nil, err
	}
	if cfg.JPEGQuality, err = getenvInt("JPEG_QUALITY", cfg.JPEGQuality); err != nil {
		return nil, err
	}
	if cfg.MinBytes, err = getenvInt64("MIN_BYTES", cfg.MinBytes); err != nil {
		return nil, err
	}
	if cfg.ScanInterval, err = getenvDuration("SCAN_INTERVAL", cfg.ScanInterval); err != nil {
		return nil, err
	}
	if cfg.ScanEnabled, err = getenvBool("SCAN_ENABLED", cfg.ScanEnabled); err != nil {
		return nil, err
	}
	if cfg.ProcessDelay, err = getenvDuration("PROCESS_DELAY", cfg.ProcessDelay); err != nil {
		return nil, err
	}
	if cfg.TriggerQueueSize, err = getenvInt("TRIGGER_QUEUE_SIZE", cfg.TriggerQueueSize); err != nil {
		return nil, err
	}
	if cfg.ScanRetryAttempts, err = getenvInt("SCAN_RETRY_ATTEMPTS", cfg.ScanRetryAttempts); err != nil {
		return nil, err
	}
	if cfg.ScanRetryInitialDelay, err = getenvDuration("SCAN_RETRY_INITIAL_DELAY", cfg.ScanRetryInitialDelay); err != nil {
		return nil, err
	}
	if cfg.ScanRetryMaxDelay, err = getenvDuration("SCAN_RETRY_MAX_DELAY", cfg.ScanRetryMaxDelay); err != nil {
		return nil, err
	}
	if cfg.RunOnce, err = getenvBool("RUN_ONCE", cfg.RunOnce); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.S3Endpoint == "" {
		return fmt.Errorf("S3_ENDPOINT is required")
	}
	if c.S3Region == "" {
		return fmt.Errorf("S3_REGION is required")
	}
	if c.S3AccessKeyID == "" {
		return fmt.Errorf("S3_ACCESS_KEY_ID is required")
	}
	if c.S3SecretAccessKey == "" {
		return fmt.Errorf("S3_SECRET_ACCESS_KEY is required")
	}
	if c.SourceBucket == "" {
		return fmt.Errorf("SOURCE_BUCKET is required")
	}
	if c.OptimizedBucket == "" {
		return fmt.Errorf("OPTIMIZED_BUCKET is required")
	}
	if c.OptimizationProfile == "" {
		return fmt.Errorf("OPTIMIZATION_PROFILE is required")
	}
	if c.MaxWidth < 0 {
		return fmt.Errorf("MAX_WIDTH cannot be negative")
	}
	if c.JPEGQuality < 1 || c.JPEGQuality > 100 {
		return fmt.Errorf("JPEG_QUALITY must be between 1 and 100")
	}
	if c.MinBytes < 0 {
		return fmt.Errorf("MIN_BYTES cannot be negative")
	}
	if c.ScanInterval <= 0 {
		return fmt.Errorf("SCAN_INTERVAL must be positive")
	}
	if c.ProcessDelay < 0 {
		return fmt.Errorf("PROCESS_DELAY cannot be negative")
	}
	if c.TriggerQueueSize < 1 {
		return fmt.Errorf("TRIGGER_QUEUE_SIZE must be at least 1")
	}
	if c.ScanRetryAttempts < 1 {
		return fmt.Errorf("SCAN_RETRY_ATTEMPTS must be at least 1")
	}
	if c.ScanRetryInitialDelay < 0 {
		return fmt.Errorf("SCAN_RETRY_INITIAL_DELAY cannot be negative")
	}
	if c.ScanRetryMaxDelay < 0 {
		return fmt.Errorf("SCAN_RETRY_MAX_DELAY cannot be negative")
	}
	if c.ScanRetryMaxDelay > 0 && c.ScanRetryInitialDelay > c.ScanRetryMaxDelay {
		return fmt.Errorf("SCAN_RETRY_INITIAL_DELAY cannot exceed SCAN_RETRY_MAX_DELAY")
	}
	return nil
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getenvBool(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid %s: %w", key, err)
	}
	return parsed, nil
}

func getenvInt(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return parsed, nil
}

func getenvInt64(key string, fallback int64) (int64, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return parsed, nil
}

func getenvDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return parsed, nil
}
