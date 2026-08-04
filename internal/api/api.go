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
	"github.com/rinat1313/analizator_zakupok/internal/lmstudio"
	"github.com/rinat1313/analizator_zakupok/internal/store"
)

// Server HTTP API анализатора.
type Server struct {
	cfg      config.Config
	svc      *analyzer.Service
	store    *store.Store
	llm      *lmstudio.Client
	mux      *http.ServeMux
}

func New(cfg config.Config, svc *analyzer.Service, st *store.Store, llm *lmstudio.Client) *Server {
	s := &Server{cfg: cfg, svc: svc, store: st, llm: llm, mux: http.NewServeMux()}
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
	s.mux.HandleFunc("GET /api/v1/analysis/{reg}", s.handleGetAnalysis)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := map[string]any{
		"status":       "ok",
		"service":      "analizator_zakupok",
		"tenders_root": s.cfg.TendersRoot,
		"time":         time.Now().UTC().Format(time.RFC3339),
	}
	ctx := r.Context()
	if err := s.llm.Ping(ctx); err != nil {
		status["lm_studio"] = "unavailable"
		status["lm_studio_error"] = err.Error()
	} else {
		status["lm_studio"] = "ok"
	}
	writeJSON(w, http.StatusOK, status)
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

	log.Printf("analyze start reg=%q checklist=%q text_len=%d", req.RegNumber, req.ChecklistID, len(req.Text))
	res, err := s.svc.Analyze(r.Context(), req)
	if err != nil {
		if res != nil {
			writeJSON(w, http.StatusOK, res) // статус failed уже в теле
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleGetAnalysis(w http.ResponseWriter, r *http.Request) {
	reg := strings.TrimSpace(r.PathValue("reg"))
	if reg == "" {
		writeErr(w, http.StatusBadRequest, "reg is required")
		return
	}
	res, err := s.store.LoadAnalysis(r.Context(), reg)
	if err != nil {
		writeErr(w, http.StatusNotFound, "analysis not found: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
