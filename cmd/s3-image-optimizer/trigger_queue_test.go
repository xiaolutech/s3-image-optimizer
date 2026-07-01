package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeKeyProcessor struct {
	mu      sync.Mutex
	keys    []string
	started chan string
	block   chan struct{}
}

func newFakeKeyProcessor() *fakeKeyProcessor {
	return &fakeKeyProcessor{
		started: make(chan string, 8),
		block:   make(chan struct{}),
	}
}

func (p *fakeKeyProcessor) ProcessKey(ctx context.Context, key string) error {
	p.mu.Lock()
	p.keys = append(p.keys, key)
	p.mu.Unlock()
	p.started <- key
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.block:
		return nil
	}
}

func TestOptimizeHandlerQueuesQueryKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := newFakeKeyProcessor()
	defer close(processor.block)
	queue := startTriggerQueue(ctx, processor, 4)

	req := httptest.NewRequest(http.MethodPost, "/optimize?key=notes/photo.jpg", nil)
	rec := httptest.NewRecorder()
	optimizeHandler(queue)(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "queued" || body["key"] != "notes/photo.jpg" {
		t.Fatalf("unexpected body %#v", body)
	}

	select {
	case got := <-processor.started:
		if got != "notes/photo.jpg" {
			t.Fatalf("expected queued key notes/photo.jpg, got %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for processor")
	}
}

func TestOptimizeHandlerQueuesJSONKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := newFakeKeyProcessor()
	defer close(processor.block)
	queue := startTriggerQueue(ctx, processor, 4)

	req := httptest.NewRequest(http.MethodPost, "/optimize", strings.NewReader(`{"key":"json/photo.jpg"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	optimizeHandler(queue)(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case got := <-processor.started:
		if got != "json/photo.jpg" {
			t.Fatalf("expected queued key json/photo.jpg, got %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for processor")
	}
}

func TestTriggerQueueDeduplicatesPendingKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := newFakeKeyProcessor()
	queue := startTriggerQueue(ctx, processor, 4)

	status, err := queue.enqueue("same.jpg")
	if err != nil || status != "queued" {
		t.Fatalf("expected first enqueue queued, status=%s err=%v", status, err)
	}
	status, err = queue.enqueue("same.jpg")
	if err != nil || status != "already_queued" {
		t.Fatalf("expected duplicate already_queued, status=%s err=%v", status, err)
	}

	close(processor.block)
	select {
	case <-processor.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for processor")
	}
}

func TestTriggerQueueRejectsFullQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := newFakeKeyProcessor()
	queue := startTriggerQueue(ctx, processor, 1)

	status, err := queue.enqueue("first.jpg")
	if err != nil || status != "queued" {
		t.Fatalf("expected first enqueue queued, status=%s err=%v", status, err)
	}
	status, err = queue.enqueue("second.jpg")
	if err == nil || status != "" {
		t.Fatalf("expected full queue error, status=%s err=%v", status, err)
	}
	close(processor.block)
}

func TestOptimizeHandlerRejectsInvalidRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := newFakeKeyProcessor()
	defer close(processor.block)
	queue := startTriggerQueue(ctx, processor, 4)

	tests := []struct {
		name string
		req  *http.Request
		code int
	}{
		{name: "method", req: httptest.NewRequest(http.MethodGet, "/optimize?key=x.jpg", nil), code: http.StatusMethodNotAllowed},
		{name: "missing key", req: httptest.NewRequest(http.MethodPost, "/optimize", nil), code: http.StatusBadRequest},
		{name: "invalid json", req: httptest.NewRequest(http.MethodPost, "/optimize", strings.NewReader(`{`)), code: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			optimizeHandler(queue)(rec, tt.req)
			if rec.Code != tt.code {
				t.Fatalf("expected status %d, got %d body=%s", tt.code, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestOptimizeHandlerRejectsOversizedJSONBody(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := newFakeKeyProcessor()
	defer close(processor.block)
	queue := startTriggerQueue(ctx, processor, 4)

	req := httptest.NewRequest(http.MethodPost, "/optimize", strings.NewReader(`{"key":"`+strings.Repeat("a", optimizeRequestBodyLimit)+`"}`))
	rec := httptest.NewRecorder()
	optimizeHandler(queue)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "request body too large") {
		t.Fatalf("expected body size error, got %s", rec.Body.String())
	}
}
