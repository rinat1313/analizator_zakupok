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
	"github.com/rinat1313/analizator_zakupok/internal/lmstudio"
	"github.com/rinat1313/analizator_zakupok/internal/store"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("config: %v", err)
	}

	st, err := store.New(cfg.TendersRoot, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	llm := lmstudio.New(lmstudio.Options{
		BaseURL:     cfg.LMStudioBaseURL,
		APIKey:      cfg.LMStudioAPIKey,
		Model:       cfg.LMStudioModel,
		Timeout:     cfg.LMStudioTimeout,
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
	})

	svc := analyzer.New(cfg, llm, st)
	srv := api.New(cfg, svc, st, llm)

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           logRequests(srv.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("analizator_zakupok listening on %s (tenders=%s lm=%s)",
			cfg.HTTPAddr, cfg.TendersRoot, cfg.LMStudioBaseURL)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

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
