package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

const (
	DirOrigin    = "origin"
	DirValidDoc  = "valid_doc"
	DirHTML      = "html"
	DirValidInfo = "valid_info"
	DirOverInfo  = "over_info"
	DirAnalysis  = "analysis" // раздел анализа тендера
)

// AnalysisResult — итоговый раздел анализа тендера.
type AnalysisResult struct {
	RegNumber      string       `json:"reg_number"`
	Law            string       `json:"law,omitempty"`
	Status         string       `json:"status"` // pending|running|completed|failed
	ChecklistID    string       `json:"checklist_id"`
	ChecklistName  string       `json:"checklist_name,omitempty"`
	Model          string       `json:"model,omitempty"`
	Recommendation string       `json:"recommendation,omitempty"` // participate|caution|skip|unknown
	Score          float64      `json:"score"`
	Summary        string       `json:"summary,omitempty"`
	Items          []ItemResult `json:"items,omitempty"`
	Risks          []string     `json:"risks,omitempty"`
	Actions        []string     `json:"actions,omitempty"`
	SourcesUsed    []string     `json:"sources_used,omitempty"`
	ChunksTotal    int          `json:"chunks_total,omitempty"`
	ChunksUsed     int          `json:"chunks_used,omitempty"`
	Error          string       `json:"error,omitempty"`
	StartedAt      string       `json:"started_at,omitempty"`
	AnalyzedAt     string       `json:"analyzed_at,omitempty"`
	RawSynthesize  string       `json:"raw_synthesize,omitempty"`
}

// ItemResult — результат по пункту чек-листа.
type ItemResult struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Status   string   `json:"status"` // ok|warn|fail|unknown
	Score    float64  `json:"score"`
	Findings string   `json:"findings"`
	Evidence []string `json:"evidence,omitempty"`
	ChunkIDs []string `json:"chunk_ids,omitempty"`
}

// TenderMeta краткие сведения из valid_info.
type TenderMeta struct {
	RegNumber string
	Law       string
	Title     string
	RawJSON   map[string]json.RawMessage
}

// Store — файловое хранилище тендеров (+ опционально PostgreSQL).
type Store struct {
	root string
	db   *sql.DB
}

func New(root string, databaseURL string) (*Store, error) {
	s := &Store{root: root}
	if databaseURL != "" {
		db, err := sql.Open("postgres", databaseURL)
		if err != nil {
			return nil, fmt.Errorf("postgres: %w", err)
		}
		db.SetMaxOpenConns(5)
		db.SetConnMaxLifetime(30 * time.Minute)
		var pingErr error
		for attempt := 1; attempt <= 15; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			pingErr = db.PingContext(ctx)
			cancel()
			if pingErr == nil {
				break
			}
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		if pingErr != nil {
			_ = db.Close()
			return nil, fmt.Errorf("postgres ping: %w", pingErr)
		}
		s.db = db
		if err := s.ensureSchema(context.Background()); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Close() {
	if s.db != nil {
		_ = s.db.Close()
	}
}

func (s *Store) TenderDir(regNumber string) string {
	return filepath.Join(s.root, sanitizeID(regNumber))
}

func (s *Store) AnalysisDir(regNumber string) string {
	return filepath.Join(s.TenderDir(regNumber), DirAnalysis)
}

func (s *Store) Exists(regNumber string) bool {
	_, err := os.Stat(s.TenderDir(regNumber))
	return err == nil
}

// LoadTenderMeta читает valid_info/*.json.
func (s *Store) LoadTenderMeta(regNumber string) (*TenderMeta, error) {
	dir := filepath.Join(s.TenderDir(regNumber), DirValidInfo)
	meta := &TenderMeta{
		RegNumber: regNumber,
		RawJSON:   map[string]json.RawMessage{},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("valid_info: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		meta.RawJSON[e.Name()] = json.RawMessage(data)
		if e.Name() == "export.json" {
			var exp struct {
				Law string `json:"law"`
			}
			_ = json.Unmarshal(data, &exp)
			meta.Law = exp.Law
		}
		if e.Name() == "tender.json" {
			var t map[string]any
			if json.Unmarshal(data, &t) == nil {
				for _, key := range []string{"object_name", "name", "subject", "title"} {
					if v, ok := t[key].(string); ok && v != "" {
						meta.Title = v
						break
					}
				}
			}
		}
	}
	return meta, nil
}

// DocumentSource — текстовый источник для анализа.
type DocumentSource struct {
	Name string
	Text string
}

// CollectSources собирает JSON карточки + valid_doc/*.txt (+ опциональный произвольный текст).
func (s *Store) CollectSources(regNumber, extraText string) ([]DocumentSource, error) {
	var sources []DocumentSource

	meta, err := s.LoadTenderMeta(regNumber)
	if err == nil {
		for name, raw := range meta.RawJSON {
			sources = append(sources, DocumentSource{
				Name: "valid_info/" + name,
				Text: string(raw),
			})
		}
	}

	docDir := filepath.Join(s.TenderDir(regNumber), DirValidDoc)
	_ = filepath.WalkDir(docDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".txt") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(s.TenderDir(regNumber), path)
		text := strings.TrimSpace(string(data))
		if text == "" {
			return nil
		}
		sources = append(sources, DocumentSource{Name: rel, Text: text})
		return nil
	})

	if strings.TrimSpace(extraText) != "" {
		sources = append(sources, DocumentSource{Name: "input_text", Text: extraText})
	}

	if len(sources) == 0 {
		return nil, fmt.Errorf("нет данных для анализа по тендеру %s (ожидаются valid_info/ или valid_doc/)", regNumber)
	}
	return sources, nil
}

// CollectTextOnly — источники только из переданного текста (без папки тендера).
func CollectTextOnly(extraText string) []DocumentSource {
	return []DocumentSource{{Name: "input_text", Text: strings.TrimSpace(extraText)}}
}

func (s *Store) SaveAnalysis(ctx context.Context, res *AnalysisResult) error {
	if res.RegNumber == "" {
		return fmt.Errorf("reg_number is required")
	}
	dir := s.AnalysisDir(res.RegNumber)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, "analysis.json"), res); err != nil {
		return err
	}
	validInfo := filepath.Join(s.TenderDir(res.RegNumber), DirValidInfo)
	if _, err := os.Stat(validInfo); err == nil {
		_ = writeJSON(filepath.Join(validInfo, "analysis.json"), res)
	}
	if s.db != nil {
		if err := s.upsertDB(ctx, res); err != nil {
			return fmt.Errorf("save analysis to db: %w", err)
		}
	}
	return nil
}

func (s *Store) LoadAnalysis(ctx context.Context, regNumber string) (*AnalysisResult, error) {
	path := filepath.Join(s.AnalysisDir(regNumber), "analysis.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if s.db != nil {
			return s.loadDB(ctx, regNumber)
		}
		return nil, err
	}
	var res AnalysisResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *Store) ensureSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS tender_analyses (
  reg_number    TEXT PRIMARY KEY,
  law           TEXT,
  status        TEXT NOT NULL,
  checklist_id  TEXT,
  recommendation TEXT,
  score         DOUBLE PRECISION DEFAULT 0,
  summary       TEXT,
  result_json   JSONB NOT NULL,
  analyzed_at   TIMESTAMPTZ,
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`)
	return err
}

func (s *Store) upsertDB(ctx context.Context, res *AnalysisResult) error {
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	var analyzedAt any
	if res.AnalyzedAt != "" {
		if t, err := time.Parse(time.RFC3339, res.AnalyzedAt); err == nil {
			analyzedAt = t
		}
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO tender_analyses (reg_number, law, status, checklist_id, recommendation, score, summary, result_json, analyzed_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW())
ON CONFLICT (reg_number) DO UPDATE SET
  law=EXCLUDED.law,
  status=EXCLUDED.status,
  checklist_id=EXCLUDED.checklist_id,
  recommendation=EXCLUDED.recommendation,
  score=EXCLUDED.score,
  summary=EXCLUDED.summary,
  result_json=EXCLUDED.result_json,
  analyzed_at=EXCLUDED.analyzed_at,
  updated_at=NOW()`,
		res.RegNumber, res.Law, res.Status, res.ChecklistID, res.Recommendation,
		res.Score, res.Summary, raw, analyzedAt)
	return err
}

func (s *Store) loadDB(ctx context.Context, regNumber string) (*AnalysisResult, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT result_json FROM tender_analyses WHERE reg_number=$1`, regNumber).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var res AnalysisResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func sanitizeID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, "..", "")
	id = strings.ReplaceAll(id, "/", "_")
	id = strings.ReplaceAll(id, "\\", "_")
	return id
}
