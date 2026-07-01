package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
)

const optimizeRequestBodyLimit = 4 * 1024

type keyProcessor interface {
	ProcessKey(ctx context.Context, key string) error
}

type triggerQueue struct {
	processor keyProcessor
	keys      chan string
	mu        sync.Mutex
	pending   map[string]struct{}
}

func startTriggerQueue(ctx context.Context, processor keyProcessor, size int) *triggerQueue {
	q := &triggerQueue{
		processor: processor,
		keys:      make(chan string, size),
		pending:   make(map[string]struct{}),
	}
	go q.run(ctx)
	return q
}

func (q *triggerQueue) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case key := <-q.keys:
			log.Printf("on-demand optimize started key=%s", key)
			if err := q.processor.ProcessKey(ctx, key); err != nil {
				log.Printf("on-demand optimize failed key=%s err=%v", key, err)
			} else {
				log.Printf("on-demand optimize completed key=%s", key)
			}
			q.finish(key)
		}
	}
}

func (q *triggerQueue) enqueue(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("key is required")
	}

	q.mu.Lock()
	if _, ok := q.pending[key]; ok {
		q.mu.Unlock()
		return "already_queued", nil
	}
	if len(q.pending) >= cap(q.keys) {
		q.mu.Unlock()
		return "", errors.New("trigger queue is full")
	}
	q.pending[key] = struct{}{}
	q.mu.Unlock()

	select {
	case q.keys <- key:
		return "queued", nil
	default:
		q.finish(key)
		return "", errors.New("trigger queue is full")
	}
}

func (q *triggerQueue) finish(key string) {
	q.mu.Lock()
	delete(q.pending, key)
	q.mu.Unlock()
}

type optimizeRequest struct {
	Key string `json:"key"`
}

func optimizeHandler(triggers *triggerQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		key := r.URL.Query().Get("key")
		if key == "" && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, optimizeRequestBodyLimit)
			var req optimizeRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid JSON body: %v", err)})
				return
			}
			key = req.Key
		}

		status, err := triggers.enqueue(key)
		if err != nil {
			code := http.StatusBadRequest
			if strings.Contains(err.Error(), "queue is full") {
				code = http.StatusTooManyRequests
			}
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": status, "key": strings.TrimSpace(key)})
	}
}

func writeJSON(w http.ResponseWriter, status int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
