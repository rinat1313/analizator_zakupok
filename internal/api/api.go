package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/rinat1313/analizator_zakupok/internal/analyzer"
	"github.com/rinat1313/analizator_zakupok/internal/checklist"
	"github.com/rinat1313/analizator_zakupok/internal/config"
	"github.com/rinat1313/analizator_zakupok/internal/lmpool"
	"github.com/rinat1313/analizator_zakupok/internal/lmstudio"
	"github.com/rinat1313/analizator_zakupok/internal/store"
)

// Server HTTP API анализатора.
type Server struct {
	cfg   config.Config
	svc   *analyzer.Service
	store *store.Store
	llm   analyzer.LLM
	pool  *lmpool.Pool
	mux   *http.ServeMux
}

func New(cfg config.Config, svc *analyzer.Service, st *store.Store, llm analyzer.LLM) *Server {
	s := &Server{cfg: cfg, svc: svc, store: st, llm: llm, mux: http.NewServeMux()}
	if p, ok := llm.(*lmpool.Pool); ok {
		s.pool = p
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/checklists", s.handleListChecklists)
	s.mux.HandleFunc("POST /api/v1/analyze", s.handleAnalyze)
	s.mux.HandleFunc("GET /api/v1/analyze/progress/{reg}", s.handleAnalyzeProgress)
	s.mux.HandleFunc("GET /api/v1/lm/pool", s.handleLMPool)
	s.mux.HandleFunc("POST /api/v1/lm/smoke", s.handleLMSmoke)
	s.mux.HandleFunc("GET /api/v1/analysis/{reg}", s.handleGetAnalysis)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"status":       "ok",
		"service":      "analizator_zakupok",
		"tenders_root": s.cfg.TendersRoot,
		"lm_base_url":  s.llm.BaseURL(),
		"lm_model":     s.llm.Model(),
		"time":         time.Now().UTC().Format(time.RFC3339),
	}
	if s.pool != nil {
		st := s.pool.Status()
		status["lm_pool"] = st
		status["lm_max_parallel"] = st.MaxParallel
	}
	if r.URL.Query().Get("lm") == "1" || r.URL.Query().Get("deep") == "1" {
		ctx := r.Context()
		if err := s.llm.Ping(ctx); err != nil {
			status["lm_studio"] = "unavailable"
			status["lm_studio_error"] = err.Error()
		} else {
			status["lm_studio"] = "ok"
		}
	} else {
		status["lm_studio"] = "skipped"
		status["lm_studio_hint"] = "use GET /health?lm=1 to probe LM Studio"
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleLMPool(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"total":         1,
			"healthy":       1,
			"busy":          0,
			"available":     1,
			"max_parallel":  1,
			"hosts":         1,
			"healthy_hosts": 1,
			"endpoints": []map[string]any{{
				"name": "default", "base_url": s.llm.BaseURL(), "model": s.llm.Model(), "healthy": true,
			}},
		})
		return
	}
	writeJSON(w, http.StatusOK, s.pool.Status())
}

func (s *Server) handleListChecklists(w http.ResponseWriter, r *http.Request) {
	ids, err := checklist.ListIDs(s.cfg.ChecklistsDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checklists": ids})
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	var req analyzer.Request
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if strings.TrimSpace(req.RegNumber) == "" && strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, "reg_number or text is required")
		return
	}

	log.Printf("analyze start reg=%q checklist=%q text_len=%d title=%q", req.RegNumber, req.ChecklistID, len(req.Text), req.Title)
	res, err := s.svc.Analyze(r.Context(), req)
	if err != nil {
		log.Printf("analyze end reg=%q err=%v", req.RegNumber, err)
		if res != nil {
			writeJSON(w, http.StatusOK, res)
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("analyze end reg=%q status=%s recommendation=%s doses=%d", req.RegNumber, res.Status, res.Recommendation, res.ChunksUsed)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleAnalyzeProgress(w http.ResponseWriter, r *http.Request) {
	reg := strings.TrimSpace(r.PathValue("reg"))
	if reg == "" {
		writeErr(w, http.StatusBadRequest, "reg required")
		return
	}
	info, ok := s.svc.Progress().Get(reg)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"reg_number":  reg,
			"percent":     0,
			"doses_done":  0,
			"doses_total": 0,
			"phase":       "idle",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reg_number":  reg,
		"percent":     info.Percent,
		"doses_done":  info.DosesDone,
		"doses_total": info.DosesTotal,
		"phase":       info.Phase,
	})
}

func (s *Server) handleLMSmoke(w http.ResponseWriter, r *http.Request) {
	content, model, err := s.llm.ChatMaxTokens(r.Context(), []lmstudio.Message{
		{Role: "system", Content: "Ответь одним словом."},
		{Role: "user", Content: "Скажи: ок"},
	}, 32)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":     false,
			"error":  err.Error(),
			"lm_url": s.llm.BaseURL(),
			"model":  s.llm.Model(),
			"expect": "в логе LM Studio должен появиться POST /v1/chat/completions",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"model":   model,
		"content": content,
		"lm_url":  s.llm.BaseURL(),
	})
}

func (s *Server) handleGetAnalysis(w http.ResponseWriter, r *http.Request) {
	reg := strings.TrimSpace(r.PathValue("reg"))
	if reg == "" {
		writeErr(w, http.StatusBadRequest, "reg required")
		return
	}
	res, err := s.store.LoadAnalysis(r.Context(), reg)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
