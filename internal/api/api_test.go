package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rinat1313/analizator_zakupok/internal/analyzer"
	"github.com/rinat1313/analizator_zakupok/internal/api"
	"github.com/rinat1313/analizator_zakupok/internal/config"
	"github.com/rinat1313/analizator_zakupok/internal/lmstudio"
	"github.com/rinat1313/analizator_zakupok/internal/store"
)

func TestHealthAndChecklists(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		HTTPAddr:        ":0",
		TendersRoot:     root,
		ChecklistsDir:   filepath.Join("..", "..", "configs", "checklists"),
		DefaultList:     "default",
		LMStudioBaseURL: "http://127.0.0.1:1/v1",
		ChunkSize:       1000,
		ChunkOverlap:    50,
		Concurrency:     1,
	}
	st, err := store.New(root, "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	llm := lmstudio.New(lmstudio.Options{BaseURL: cfg.LMStudioBaseURL, Model: "x"})
	svc := analyzer.New(cfg, llm, st)
	srv := api.New(cfg, svc, st, llm)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("health code=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/checklists", nil)
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("checklists code=%d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["checklists"]; !ok {
		t.Fatalf("body=%v", body)
	}
}

func TestAnalyzeValidation(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{
		TendersRoot:     root,
		ChecklistsDir:   filepath.Join("..", "..", "configs", "checklists"),
		DefaultList:     "quick",
		LMStudioBaseURL: "http://127.0.0.1:1/v1",
		ChunkSize:       1000,
		Concurrency:     1,
	}
	st, _ := store.New(root, "")
	defer st.Close()
	llm := lmstudio.New(lmstudio.Options{BaseURL: cfg.LMStudioBaseURL, Model: "x"})
	svc := analyzer.New(cfg, llm, st)
	srv := api.New(cfg, svc, st, llm)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analyze", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", rr.Code, rr.Body.String())
	}

	// Get missing analysis
	req = httptest.NewRequest(http.MethodGet, "/api/v1/analysis/missing", nil)
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}

	_ = os.MkdirAll(filepath.Join(root, "x", store.DirAnalysis), 0o755)
}
