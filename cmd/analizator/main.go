package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rinat1313/analizator_zakupok/internal/analyzer"
	"github.com/rinat1313/analizator_zakupok/internal/api"
	"github.com/rinat1313/analizator_zakupok/internal/config"
	"github.com/rinat1313/analizator_zakupok/internal/lmpool"
	"github.com/rinat1313/analizator_zakupok/internal/lmstudio"
	"github.com/rinat1313/analizator_zakupok/internal/store"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := os.MkdirAll(cfg.TendersRoot, 0o755); err != nil {
		log.Fatalf("tenders root %s: %v", cfg.TendersRoot, err)
	}

	st, err := store.New(cfg.TendersRoot, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	pool, err := lmpool.Load(os.Getenv("LM_STUDIO_ENDPOINTS_FILE"), lmstudio.Options{
		BaseURL:     cfg.LMStudioBaseURL,
		APIKey:      cfg.LMStudioAPIKey,
		Model:       cfg.LMStudioModel,
		Timeout:     cfg.LMStudioTimeout,
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
	})
	if err != nil {
		log.Fatalf("lmpool: %v", err)
	}

	runCtx, runCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer runCancel()
	pool.StartHealth(runCtx)
	log.Printf("lmpool: mode=single_exclusive max_parallel=%d status=%+v", pool.MaxParallel(), pool.Status())

	svc := analyzer.New(cfg, pool, st)
	srv := api.New(cfg, svc, st, pool)

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           logRequests(srv.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("analizator_zakupok listening on %s (tenders=%s llm=%s model=%s exclusive=1)",
			cfg.HTTPAddr, cfg.TendersRoot, pool.BaseURL(), pool.Model())
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	<-runCtx.Done()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	log.Println("shutdown complete")
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}
