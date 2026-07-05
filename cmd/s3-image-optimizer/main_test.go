package main

import (
	"errors"
	"testing"
	"time"

	"github.com/xiaolutech/s3-image-optimizer/internal/worker"
)

func TestNextScanDelayUsesIntervalWhenRoundHasMoreObjects(t *testing.T) {
	got := nextScanDelay(worker.ScanRoundResult{HasMore: true}, nil, 10*time.Second, 12*time.Hour)

	if got != 10*time.Second {
		t.Fatalf("expected normal scan interval, got %v", got)
	}
}

func TestNextScanDelayUsesFullPassIntervalWhenRoundReachesBucketEnd(t *testing.T) {
	got := nextScanDelay(worker.ScanRoundResult{HasMore: false}, nil, 10*time.Second, 12*time.Hour)

	if got != 12*time.Hour {
		t.Fatalf("expected full-pass interval, got %v", got)
	}
}

func TestNextScanDelayUsesIntervalAfterScanFailure(t *testing.T) {
	got := nextScanDelay(worker.ScanRoundResult{}, errors.New("list failed"), 10*time.Second, 12*time.Hour)

	if got != 10*time.Second {
		t.Fatalf("expected normal scan interval after failure, got %v", got)
	}
}
