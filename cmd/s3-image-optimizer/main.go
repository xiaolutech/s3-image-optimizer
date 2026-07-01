package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xiaolutech/s3-image-optimizer/internal/config"
	"github.com/xiaolutech/s3-image-optimizer/internal/storage"
	"github.com/xiaolutech/s3-image-optimizer/internal/worker"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := runHealthcheck(); err != nil {
			log.Fatalf("healthcheck: %v", err)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store, err := storage.New(cfg)
	if err != nil {
		log.Fatalf("create storage: %v", err)
	}

	w := worker.New(store, worker.Config{
		SourceBucket:        cfg.SourceBucket,
		OptimizedBucket:     cfg.OptimizedBucket,
		OptimizationProfile: cfg.OptimizationProfile,
		MaxWidth:            cfg.MaxWidth,
		JPEGQuality:         cfg.JPEGQuality,
		MinBytes:            cfg.MinBytes,
		ProcessDelay:        cfg.ProcessDelay,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server := startHealthServer(cfg.Port)
	defer shutdownHealthServer(server)

	if cfg.RunOnce {
		if err := w.RunOnce(ctx); err != nil {
			log.Fatalf("run once: %v", err)
		}
		return
	}

	runLoop(ctx, w, cfg.ScanInterval)
}

func startHealthServer(port string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%s", port),
		Handler: mux,
	}
	go func() {
		log.Printf("health server listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("health server: %v", err)
		}
	}()
	return server
}

func runHealthcheck() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/health", port))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}

func shutdownHealthServer(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("health server shutdown: %v", err)
	}
}

func runLoop(ctx context.Context, w *worker.Worker, interval time.Duration) {
	runScan(ctx, w)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("shutdown requested")
			return
		case <-ticker.C:
			runScan(ctx, w)
		}
	}
}

func runScan(ctx context.Context, w *worker.Worker) {
	start := time.Now()
	log.Printf("scan started")
	if err := w.RunOnce(ctx); err != nil {
		log.Printf("scan failed: %v", err)
		return
	}
	log.Printf("scan completed duration=%s", time.Since(start))
}
