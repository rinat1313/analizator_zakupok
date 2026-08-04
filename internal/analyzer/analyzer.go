package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/rinat1313/analizator_zakupok/internal/checklist"
	"github.com/rinat1313/analizator_zakupok/internal/chunker"
	"github.com/rinat1313/analizator_zakupok/internal/config"
	"github.com/rinat1313/analizator_zakupok/internal/lmstudio"
	"github.com/rinat1313/analizator_zakupok/internal/prompt"
	"github.com/rinat1313/analizator_zakupok/internal/store"
)

// Service оркестрирует chunking → анализ по чек-листу → синтез рекомендаций.
type Service struct {
	cfg     config.Config
	llm     *lmstudio.Client
	store   *store.Store
	prompts prompt.Bundle
}

func New(cfg config.Config, llm *lmstudio.Client, st *store.Store) *Service {
	prompts, err := prompt.Load(cfg.PromptsDir)
	if err != nil {
		log.Printf("prompts warn: %v (using built-in defaults)", err)
		prompts, _ = prompt.Load("")
	}
	return &Service{cfg: cfg, llm: llm, store: st, prompts: prompts}
}

// Request вход анализа.
type Request struct {
	RegNumber   string `json:"reg_number"`
	Text        string `json:"text"`
	ChecklistID string `json:"checklist_id"`
	Title       string `json:"title"`
}

// Analyze выполняет полный анализ и сохраняет раздел analysis.
func (s *Service) Analyze(ctx context.Context, req Request) (*store.AnalysisResult, error) {
	req.RegNumber = strings.TrimSpace(req.RegNumber)
	req.Text = strings.TrimSpace(req.Text)
	if req.RegNumber == "" && req.Text == "" {
		return nil, fmt.Errorf("нужен reg_number и/или text")
	}
	if req.ChecklistID == "" {
		req.ChecklistID = s.cfg.DefaultList
	}

	list, err := checklist.Load(s.cfg.ChecklistsDir, req.ChecklistID)
	if err != nil {
		return nil, err
	}

	id := req.RegNumber
	if id == "" {
		id = "text-" + time.Now().Format("20060102-150405")
	}

	started := time.Now().UTC().Format(time.RFC3339)
	pending := &store.AnalysisResult{
		RegNumber:     id,
		Status:        "running",
		ChecklistID:   list.ID,
		ChecklistName: list.Name,
		StartedAt:     started,
	}
	_ = s.store.SaveAnalysis(ctx, pending)

	var sources []store.DocumentSource
	var law, title string
	if req.RegNumber != "" && s.store.Exists(req.RegNumber) {
		meta, err := s.store.LoadTenderMeta(req.RegNumber)
		if err == nil {
			law = meta.Law
			title = meta.Title
		}
		sources, err = s.store.CollectSources(req.RegNumber, req.Text)
		if err != nil {
			return s.fail(ctx, pending, err)
		}
	} else if req.Text != "" {
		sources = store.CollectTextOnly(req.Text)
		if req.Title != "" {
			title = req.Title
		}
	} else {
		return s.fail(ctx, pending, fmt.Errorf("тендер %s не найден в %s", req.RegNumber, s.cfg.TendersRoot))
	}
	if title == "" {
		title = req.Title
	}

	opt := chunker.Options{
		Size:    s.cfg.ChunkSize,
		Overlap: s.cfg.ChunkOverlap,
		Max:     0,
	}
	var allChunks []chunker.Chunk
	var sourceNames []string
	for _, src := range sources {
		sourceNames = append(sourceNames, src.Name)
		parts := chunker.Split(src.Name, src.Text, opt)
		allChunks = append(allChunks, parts...)
	}
	if s.cfg.MaxChunks > 0 && len(allChunks) > s.cfg.MaxChunks {
		// приоритет: valid_info + начало документов
		allChunks = prioritizeChunks(allChunks, s.cfg.MaxChunks)
	}

	itemResults := make([]store.ItemResult, len(list.Items))
	sem := make(chan struct{}, s.cfg.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	chunksUsed := map[string]struct{}{}

	for i, item := range list.Items {
		i, item := i, item
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			selected := chunker.SelectRelevant(allChunks, item.Keywords, item.MaxChunks)
			res, usedModel, err := s.analyzeItem(ctx, title, id, law, item, selected)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				itemResults[i] = store.ItemResult{
					ID:       item.ID,
					Title:    item.Title,
					Status:   "unknown",
					Findings: "ошибка анализа: " + err.Error(),
				}
				return
			}
			if usedModel != "" {
				pending.Model = usedModel
			}
			itemResults[i] = res
			for _, cid := range res.ChunkIDs {
				chunksUsed[cid] = struct{}{}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil && allFailed(itemResults) {
		return s.fail(ctx, pending, firstErr)
	}

	synth, model, err := s.synthesize(ctx, title, id, law, list, itemResults)
	if err != nil {
		log.Printf("synthesize warn: %v", err)
		synth = fallbackSynth(itemResults)
	}
	if model != "" {
		pending.Model = model
	}

	pending.Status = "completed"
	pending.Law = law
	pending.Items = itemResults
	pending.Recommendation = synth.Recommendation
	pending.Score = synth.Score
	pending.Summary = synth.Summary
	pending.Risks = synth.Risks
	pending.Actions = synth.Actions
	pending.SourcesUsed = sourceNames
	pending.ChunksTotal = len(allChunks)
	pending.ChunksUsed = len(chunksUsed)
	pending.AnalyzedAt = time.Now().UTC().Format(time.RFC3339)
	pending.RawSynthesize = synth.Raw
	pending.Error = ""

	if err := s.store.SaveAnalysis(ctx, pending); err != nil {
		return nil, err
	}
	return pending, nil
}

type synthResult struct {
	Recommendation string   `json:"recommendation"`
	Score          float64  `json:"score"`
	Summary        string   `json:"summary"`
	Risks          []string `json:"risks"`
	Actions        []string `json:"actions"`
	Raw            string   `json:"-"`
}

func (s *Service) analyzeItem(ctx context.Context, title, reg, law string, item checklist.Item, chunks []chunker.Chunk) (store.ItemResult, string, error) {
	sys := s.prompts.ItemSystem

	user := strings.Builder{}
	fmt.Fprintf(&user, "Тендер: %s\nРегномер: %s\nЗакон: %s\n", title, reg, law)
	fmt.Fprintf(&user, "Пункт чек-листа: %s (%s)\n%s\n", item.Title, item.ID, item.Description)
	if item.Prompt != "" {
		fmt.Fprintf(&user, "Инструкция: %s\n", item.Prompt)
	}
	user.WriteString("\n--- ФРАГМЕНТЫ ---\n")
	ids := make([]string, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
		fmt.Fprintf(&user, "\n### %s (source=%s)\n%s\n", c.ID, c.Source, c.Text)
	}
	if len(chunks) == 0 {
		user.WriteString("\n(фрагменты не найдены)\n")
	}

	content, model, err := s.llm.Chat(ctx, []lmstudio.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: user.String()},
	})
	if err != nil {
		return store.ItemResult{}, "", err
	}

	parsed := parseItemJSON(content)
	parsed.ID = item.ID
	parsed.Title = item.Title
	parsed.ChunkIDs = ids
	if parsed.Findings == "" {
		parsed.Findings = content
	}
	return parsed, model, nil
}

func (s *Service) synthesize(ctx context.Context, title, reg, law string, list *checklist.List, items []store.ItemResult) (synthResult, string, error) {
	sys := s.prompts.SynthesizeSystem

	var user strings.Builder
	fmt.Fprintf(&user, "Тендер: %s\nРегномер: %s\nЗакон: %s\nЧек-лист: %s\n\n", title, reg, law, list.Name)
	rawItems, _ := json.MarshalIndent(items, "", "  ")
	user.Write(rawItems)

	content, model, err := s.llm.Chat(ctx, []lmstudio.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: user.String()},
	})
	if err != nil {
		return synthResult{}, "", err
	}
	out := parseSynthJSON(content)
	out.Raw = content
	return out, model, nil
}

func (s *Service) fail(ctx context.Context, pending *store.AnalysisResult, err error) (*store.AnalysisResult, error) {
	pending.Status = "failed"
	pending.Error = err.Error()
	pending.AnalyzedAt = time.Now().UTC().Format(time.RFC3339)
	_ = s.store.SaveAnalysis(ctx, pending)
	return pending, err
}

func parseItemJSON(content string) store.ItemResult {
	var raw struct {
		Status   string   `json:"status"`
		Score    float64  `json:"score"`
		Findings string   `json:"findings"`
		Evidence []string `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(extractJSON(content)), &raw); err != nil {
		return store.ItemResult{
			Status:   "unknown",
			Findings: content,
			Score:    0.5,
		}
	}
	st := strings.ToLower(raw.Status)
	switch st {
	case "ok", "warn", "fail", "unknown":
	default:
		st = "unknown"
	}
	return store.ItemResult{
		Status:   st,
		Score:    clamp01(raw.Score),
		Findings: raw.Findings,
		Evidence: raw.Evidence,
	}
}

func parseSynthJSON(content string) synthResult {
	var raw synthResult
	if err := json.Unmarshal([]byte(extractJSON(content)), &raw); err != nil {
		return synthResult{
			Recommendation: "unknown",
			Score:          0.5,
			Summary:        content,
			Raw:            content,
		}
	}
	raw.Recommendation = strings.ToLower(strings.TrimSpace(raw.Recommendation))
	switch raw.Recommendation {
	case "participate", "caution", "skip", "unknown":
	default:
		raw.Recommendation = "unknown"
	}
	raw.Score = clamp01(raw.Score)
	return raw
}

func fallbackSynth(items []store.ItemResult) synthResult {
	if len(items) == 0 {
		return synthResult{Recommendation: "unknown", Score: 0.5, Summary: "Нет результатов по пунктам чек-листа."}
	}
	sum := 0.0
	fails, warns := 0, 0
	var risks []string
	for _, it := range items {
		sum += it.Score
		switch it.Status {
		case "fail":
			fails++
			risks = append(risks, it.Title+": "+it.Findings)
		case "warn":
			warns++
			risks = append(risks, it.Title+": "+it.Findings)
		}
	}
	avg := sum / float64(len(items))
	rec := "participate"
	if fails > 0 {
		rec = "skip"
	} else if warns > 0 || avg < 0.55 {
		rec = "caution"
	}
	return synthResult{
		Recommendation: rec,
		Score:          avg,
		Summary:        fmt.Sprintf("Агрегация без LLM: пунктов=%d, fail=%d, warn=%d, avg=%.2f", len(items), fails, warns, avg),
		Risks:          risks,
		Actions:        []string{"Проверить исходные документы вручную", "Уточнить требования заказчика"},
	}
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```JSON")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func allFailed(items []store.ItemResult) bool {
	if len(items) == 0 {
		return true
	}
	for _, it := range items {
		if !strings.HasPrefix(it.Findings, "ошибка анализа:") {
			return false
		}
	}
	return true
}

func prioritizeChunks(chunks []chunker.Chunk, max int) []chunker.Chunk {
	if len(chunks) <= max {
		return chunks
	}
	var meta, docs []chunker.Chunk
	for _, c := range chunks {
		if strings.HasPrefix(c.Source, "valid_info/") {
			meta = append(meta, c)
		} else {
			docs = append(docs, c)
		}
	}
	out := append([]chunker.Chunk{}, meta...)
	for _, c := range docs {
		if len(out) >= max {
			break
		}
		out = append(out, c)
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}
