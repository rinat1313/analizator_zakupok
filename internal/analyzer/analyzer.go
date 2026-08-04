package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/rinat1313/analizator_zakupok/internal/config"
	"github.com/rinat1313/analizator_zakupok/internal/lmstudio"
	"github.com/rinat1313/analizator_zakupok/internal/progress"
	"github.com/rinat1313/analizator_zakupok/internal/prompt"
	"github.com/rinat1313/analizator_zakupok/internal/store"
)

// FocusQuestion — целевой вопрос анализа по умолчанию.
const FocusQuestion = "Оцени закупку по возможности участия самозанятого"

// Service оркестрирует дозированный анализ → краткие ответы → итоговый синтез.
type Service struct {
	cfg      config.Config
	llm      LLM
	store    *store.Store
	prompts  prompt.Bundle
	progress *progress.Tracker
}

// LLM — LM Studio клиент или пул endpoint'ов.
type LLM interface {
	ChatMaxTokens(ctx context.Context, messages []lmstudio.Message, maxTokens int) (string, string, error)
	Ping(ctx context.Context) error
	Model() string
	BaseURL() string
}

func New(cfg config.Config, llm LLM, st *store.Store) *Service {
	prompts, err := prompt.Load(cfg.PromptsDir)
	if err != nil {
		log.Printf("prompts warn: %v (using built-in defaults)", err)
		prompts, _ = prompt.Load("")
	}
	return &Service{cfg: cfg, llm: llm, store: st, prompts: prompts, progress: progress.New()}
}

func (s *Service) Progress() *progress.Tracker { return s.progress }

// Request вход анализа.
type Request struct {
	RegNumber    string `json:"reg_number"`
	Text         string `json:"text"`
	ChecklistID  string `json:"checklist_id"`
	Title        string `json:"title"`
	ConfigName   string `json:"config_name"`
	SystemPrompt string `json:"system_prompt"`
	UserPrompt   string `json:"user_prompt"`
	Rules        string `json:"rules"`
}

type doseBrief struct {
	Index      int      `json:"index"`
	Total      int      `json:"total"`
	PageFrom   int      `json:"page_from"`
	PageTo     int      `json:"page_to"`
	Status     string   `json:"status"` // ok|warn|fail|unknown|neutral
	Score      float64  `json:"score"`
	Notes      string   `json:"notes"`
	Flags      []string `json:"flags,omitempty"`
	Raw        string   `json:"-"`
	Model      string   `json:"-"`
	RuneCount  int      `json:"rune_count,omitempty"`
	MoreComing bool     `json:"more_coming"`
}

type synthResult struct {
	Recommendation string   `json:"recommendation"`
	Score          float64  `json:"score"`
	Summary        string   `json:"summary"`
	Risks          []string `json:"risks"`
	Actions        []string `json:"actions"`
	Raw            string   `json:"-"`
}

// Analyze: режет документы на порции → краткий ответ на каждую → итоговая оценка для самозанятого.
//
// Источник текста для платформы zakupki-*:
//   core читает карточку+documents.text_content из PostgreSQL и передаёт corpus в поле text.
// Файлы valid_info/valid_doc на диске analizator — опциональный legacy, не обязательны.
func (s *Service) Analyze(ctx context.Context, req Request) (*store.AnalysisResult, error) {
	req.RegNumber = strings.TrimSpace(req.RegNumber)
	req.Text = strings.TrimSpace(req.Text)
	if req.RegNumber == "" && req.Text == "" {
		return nil, fmt.Errorf("нужен reg_number и/или text")
	}

	id := req.RegNumber
	if id == "" {
		id = "text-" + time.Now().Format("20060102-150405")
	}

	started := time.Now().UTC().Format(time.RFC3339)
	focus := FocusQuestion
	if strings.TrimSpace(req.UserPrompt) != "" {
		focus = strings.TrimSpace(req.UserPrompt)
	}
	checklistID := "samozanyaty-dosed"
	if strings.TrimSpace(req.ChecklistID) != "" {
		checklistID = strings.TrimSpace(req.ChecklistID)
	}
	checklistName := focus
	if strings.TrimSpace(req.ConfigName) != "" {
		checklistName = strings.TrimSpace(req.ConfigName)
	}
	pending := &store.AnalysisResult{
		RegNumber:     id,
		Status:        "running",
		ChecklistID:   checklistID,
		ChecklistName: checklistName,
		StartedAt:     started,
	}

	s.progress.Set(id, progress.Info{Percent: 3, Phase: "prepare"})
	defer s.progress.Clear(id)

	var law, title string
	var sourceNames []string
	var textParts []string

	if req.Title != "" {
		title = req.Title
	}

	// 1) Главный путь: corpus из PostgreSQL (присылает zakupki-core).
	if req.Text != "" {
		textParts = append(textParts, req.Text)
		sourceNames = append(sourceNames, "postgres_corpus")
		log.Printf("analyze %s: got postgres corpus runes=%d", id, len([]rune(req.Text)))
	}

	// 2) Опционально добрать файлы на диске (legacy CLI). Не ошибка, если их нет —
	// SaveAnalysis ниже создаёт каталог analysis/, из‑за чего Exists() становится true
	// даже без valid_info/valid_doc.
	if req.RegNumber != "" {
		if meta, err := s.store.LoadTenderMeta(req.RegNumber); err == nil {
			law = meta.Law
			if title == "" {
				title = meta.Title
			}
		}
		if sources, err := s.store.CollectSources(req.RegNumber, ""); err == nil {
			for _, src := range sources {
				sourceNames = append(sourceNames, src.Name)
				textParts = append(textParts, fmt.Sprintf("[%s]\n%s", src.Name, src.Text))
			}
			log.Printf("analyze %s: added %d file sources", id, len(sources))
		} else if req.Text == "" {
			return s.fail(ctx, pending, err)
		} else {
			log.Printf("analyze %s: skip file sources (%v) — using postgres corpus", id, err)
		}
	}

	corpus := joinCorpus(textParts)
	if corpus == "" {
		return s.fail(ctx, pending, fmt.Errorf(
			"нет текста для анализа: core должен передать text из PostgreSQL (documents.text_content), либо нужны файлы valid_info/valid_doc"))
	}

	// Сохраняем pending уже после проверки текста (иначе MkdirAll ломает Exists()).
	_ = s.store.SaveAnalysis(ctx, pending)

	plan := DosePlan{
		PageChars:   s.cfg.PageChars,
		DosePages:   s.cfg.DosePages,
		BudgetChars: s.cfg.ContextBudgetChars,
	}
	doses := BuildDoses(corpus, plan)
	if len(doses) == 0 {
		return s.fail(ctx, pending, fmt.Errorf("не удалось нарезать текст на порции"))
	}
	log.Printf("analyze %s: corpus_runes≈%d doses=%d page=%d budget=%d sources=%v focus=%q config=%q",
		id, len([]rune(corpus)), len(doses), plan.PageChars, plan.BudgetChars, sourceNames, focus, req.ConfigName)

	doseSystem := s.prompts.DoseSystem
	if strings.TrimSpace(req.SystemPrompt) != "" {
		doseSystem = strings.TrimSpace(req.SystemPrompt)
		if strings.TrimSpace(req.Rules) != "" {
			doseSystem += "\n\nПравила:\n" + strings.TrimSpace(req.Rules)
		}
	} else if strings.TrimSpace(req.Rules) != "" {
		doseSystem += "\n\nПравила:\n" + strings.TrimSpace(req.Rules)
	}

	briefs := make([]doseBrief, 0, len(doses))
	for _, d := range doses {
		s.progress.Set(id, progress.Info{
			Percent:    progress.DosePct(d.Index-1, len(doses)),
			DosesDone:  d.Index - 1,
			DosesTotal: len(doses),
			Phase:      "dose",
		})
		brief, err := s.analyzeDose(ctx, title, id, law, d, focus, doseSystem)
		if err != nil {
			return s.fail(ctx, pending, fmt.Errorf("%s: %w", doseLabel(d), err))
		}
		if brief.Model != "" {
			pending.Model = brief.Model
		}
		briefs = append(briefs, brief)
		s.progress.Set(id, progress.Info{
			Percent:    progress.DosePct(d.Index, len(doses)),
			DosesDone:  d.Index,
			DosesTotal: len(doses),
			Phase:      "dose",
		})
		log.Printf("analyze %s: %s status=%s score=%.2f runes=%d",
			id, doseLabel(d), brief.Status, brief.Score, brief.RuneCount)
	}

	s.progress.Set(id, progress.Info{Percent: 92, DosesDone: len(doses), DosesTotal: len(doses), Phase: "synthesize"})
	synth, model, err := s.synthesizeDoses(ctx, title, id, law, briefs, focus, doseSystem)
	if err != nil {
		log.Printf("synthesize warn: %v", err)
		synth = fallbackFromBriefs(briefs)
	}
	if model != "" {
		pending.Model = model
	}

	items := make([]store.ItemResult, 0, len(briefs))
	for _, b := range briefs {
		items = append(items, store.ItemResult{
			ID:       fmt.Sprintf("dose-%d", b.Index),
			Title:    fmt.Sprintf("Порция %d/%d (стр. ~%d–%d)", b.Index, b.Total, b.PageFrom, b.PageTo),
			Status:   normalizeItemStatus(b.Status),
			Score:    b.Score,
			Findings: b.Notes,
			Evidence: b.Flags,
			ChunkIDs: []string{fmt.Sprintf("dose-%d", b.Index)},
		})
	}

	pending.Status = "completed"
	pending.Law = law
	pending.Items = items
	pending.Recommendation = synth.Recommendation
	pending.Score = synth.Score
	pending.Summary = synth.Summary
	pending.Risks = synth.Risks
	pending.Actions = synth.Actions
	pending.SourcesUsed = uniq(sourceNames)
	pending.ChunksTotal = len(doses)
	pending.ChunksUsed = len(briefs)
	pending.AnalyzedAt = time.Now().UTC().Format(time.RFC3339)
	pending.RawSynthesize = synth.Raw
	pending.Error = ""

	s.progress.Set(id, progress.Info{Percent: 100, DosesDone: len(doses), DosesTotal: len(doses), Phase: "done"})

	if err := s.store.SaveAnalysis(ctx, pending); err != nil {
		return nil, err
	}
	return pending, nil
}

func (s *Service) analyzeDose(ctx context.Context, title, reg, law string, d Dose, focus, doseSystem string) (doseBrief, error) {
	text := d.Text
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			text = ShrinkDose(text, 0.55)
			if utf8Len(text) < 200 {
				break
			}
			log.Printf("dose %d/%d shrink attempt=%d runes=%d", d.Index, d.Total, attempt, utf8Len(text))
		}
		user := buildDoseUser(title, reg, law, d, text, focus)
		sys := doseSystem
		if sys == "" {
			sys = s.prompts.DoseSystem
		}
		content, model, err := s.llm.ChatMaxTokens(ctx, []lmstudio.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: user},
		}, s.cfg.DoseMaxTokens)
		if err != nil {
			lastErr = err
			if lmstudio.IsContextExceeded(err) {
				continue
			}
			// Пустой content у Qwen — пробуем увеличить бюджет / уменьшить текст.
			if strings.Contains(err.Error(), "empty content") {
				continue
			}
			return doseBrief{}, err
		}
		if strings.TrimSpace(content) == "" {
			lastErr = fmt.Errorf("пустой ответ модели на порцию")
			continue
		}
		parsed := parseDoseJSON(content)
		parsed.Index = d.Index
		parsed.Total = d.Total
		parsed.PageFrom = d.PageFrom
		parsed.PageTo = d.PageTo
		parsed.MoreComing = d.MoreComing
		parsed.RuneCount = utf8Len(text)
		parsed.Raw = content
		parsed.Model = model
		if parsed.Notes == "" {
			parsed.Notes = content
		}
		// Если JSON не распарсился и notes = сырой мусор без пользы — ещё попытка с меньшей порцией.
		if parsed.Status == "unknown" && len([]rune(parsed.Notes)) < 8 {
			lastErr = fmt.Errorf("модель не дала заметок по порции")
			continue
		}
		return parsed, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("не удалось уместить порцию в контекст модели")
	}
	return doseBrief{}, lastErr
}

func buildDoseUser(title, reg, law string, d Dose, text, focus string) string {
	if focus == "" {
		focus = FocusQuestion
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Целевой вопрос анализа: %s\n", focus)
	fmt.Fprintf(&b, "Тендер: %s\nРегномер: %s\nЗакон: %s\n", title, reg, law)
	fmt.Fprintf(&b, "Порция: %d из %d\n", d.Index, d.Total)
	fmt.Fprintf(&b, "Страницы текста (примерно): %d–%d\n", d.PageFrom, d.PageTo)
	if d.MoreComing {
		b.WriteString("ВАЖНО: это НЕ весь документ. После этой порции будут ещё части текста. ")
		b.WriteString("Не делай итоговый вердикт — только краткие заметки по ЭТОЙ порции. ")
		b.WriteString("Запомни: полный вывод будет позже по сумме всех порций.\n")
	} else {
		b.WriteString("ДОКУМЕНТЫ ЗАКОНЧИЛИСЬ: это последняя порция текста по закупке. ")
		b.WriteString("Дай краткие заметки только по ней. Итоговый ответ будет отдельным шагом после всех порций.\n")
	}
	b.WriteString("\n--- ТЕКСТ ПОРЦИИ ---\n")
	b.WriteString(text)
	return b.String()
}

func (s *Service) synthesizeDoses(ctx context.Context, title, reg, law string, briefs []doseBrief, focus, systemExtra string) (synthResult, string, error) {
	if focus == "" {
		focus = FocusQuestion
	}
	var user strings.Builder
	fmt.Fprintf(&user, "Целевой вопрос: %s\n", focus)
	fmt.Fprintf(&user, "Тендер: %s\nРегномер: %s\nЗакон: %s\n", title, reg, law)
	user.WriteString("ДОКУМЕНТЫ ЗАКОНЧИЛИСЬ. Все порции текста переданы. ")
	user.WriteString("Сформируй итоговый ответ по целевому вопросу (да / нет / с оговорками) и почему. ")
	user.WriteString("Ниже — краткие заметки модели по каждой порции документов (не сырой текст).\n\n")
	raw, _ := json.MarshalIndent(briefsForSynth(briefs), "", "  ")
	user.Write(raw)

	sys := s.prompts.SynthesizeSystem
	if strings.TrimSpace(systemExtra) != "" {
		sys = systemExtra + "\n\n" + s.prompts.SynthesizeSystem
	}

	content, model, err := s.llm.ChatMaxTokens(ctx, []lmstudio.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: user.String()},
	}, s.cfg.SynthMaxTokens)
	if err != nil {
		return synthResult{}, "", err
	}
	out := parseSynthJSON(content)
	out.Raw = content
	return out, model, nil
}

func briefsForSynth(briefs []doseBrief) []map[string]any {
	out := make([]map[string]any, 0, len(briefs))
	for _, b := range briefs {
		out = append(out, map[string]any{
			"dose":       b.Index,
			"of":         b.Total,
			"pages":      fmt.Sprintf("%d-%d", b.PageFrom, b.PageTo),
			"status":     b.Status,
			"score":      b.Score,
			"notes":      b.Notes,
			"flags":      b.Flags,
			"more_after": b.MoreComing,
		})
	}
	return out
}

func (s *Service) fail(ctx context.Context, pending *store.AnalysisResult, err error) (*store.AnalysisResult, error) {
	pending.Status = "failed"
	pending.Error = err.Error()
	pending.AnalyzedAt = time.Now().UTC().Format(time.RFC3339)
	_ = s.store.SaveAnalysis(ctx, pending)
	return pending, err
}

func parseDoseJSON(content string) doseBrief {
	var raw struct {
		Status string   `json:"status"`
		Score  float64  `json:"score"`
		Notes  string   `json:"notes"`
		Flags  []string `json:"flags"`
	}
	if err := json.Unmarshal([]byte(extractJSON(content)), &raw); err != nil {
		return doseBrief{Status: "unknown", Score: 0.5, Notes: content}
	}
	st := strings.ToLower(strings.TrimSpace(raw.Status))
	switch st {
	case "ok", "warn", "fail", "unknown", "neutral":
	default:
		st = "unknown"
	}
	notes := raw.Notes
	if notes == "" {
		notes = content
	}
	return doseBrief{
		Status: st,
		Score:  clamp01(raw.Score),
		Notes:  notes,
		Flags:  raw.Flags,
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
	case "participate", "да", "yes":
		raw.Recommendation = "participate"
	case "caution", "осторожно", "с оговорками":
		raw.Recommendation = "caution"
	case "skip", "нет", "no":
		raw.Recommendation = "skip"
	case "unknown":
	default:
		// Попробуем вывести из summary.
		low := strings.ToLower(raw.Summary)
		switch {
		case strings.HasPrefix(low, "да:") || strings.HasPrefix(low, "да "):
			raw.Recommendation = "participate"
		case strings.HasPrefix(low, "нет:") || strings.HasPrefix(low, "нет "):
			raw.Recommendation = "skip"
		case strings.Contains(low, "оговорк"):
			raw.Recommendation = "caution"
		default:
			raw.Recommendation = "unknown"
		}
	}
	raw.Score = clamp01(raw.Score)
	return raw
}

func fallbackFromBriefs(briefs []doseBrief) synthResult {
	if len(briefs) == 0 {
		return synthResult{
			Recommendation: "unknown",
			Score:          0.5,
			Summary:        "Нет заметок по порциям текста.",
		}
	}
	sum := 0.0
	fails, warns := 0, 0
	var risks []string
	var notes []string
	for _, b := range briefs {
		sum += b.Score
		notes = append(notes, fmt.Sprintf("Порция %d/%d: %s", b.Index, b.Total, trimRunes(b.Notes, 240)))
		switch b.Status {
		case "fail":
			fails++
			risks = append(risks, b.Flags...)
			if b.Notes != "" {
				risks = append(risks, b.Notes)
			}
		case "warn":
			warns++
		}
	}
	avg := sum / float64(len(briefs))
	rec := "participate"
	if fails > 0 {
		rec = "skip"
	} else if warns > 0 || avg < 0.55 {
		rec = "caution"
	}
	return synthResult{
		Recommendation: rec,
		Score:          avg,
		Summary: fmt.Sprintf(
			"Агрегация без LLM по %d порциям (fail=%d warn=%d avg=%.2f). Вопрос: %s. %s",
			len(briefs), fails, warns, avg, FocusQuestion, strings.Join(notes, " | "),
		),
		Risks:   uniq(risks),
		Actions: []string{"Проверить требования к участникам вручную", "Уточнить, допускаются ли физлица / НПД / самозанятые"},
	}
}

func normalizeItemStatus(st string) string {
	switch strings.ToLower(st) {
	case "ok", "warn", "fail", "unknown":
		return strings.ToLower(st)
	case "neutral":
		return "ok"
	default:
		return "unknown"
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

func utf8Len(s string) int { return len([]rune(s)) }

func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func uniq(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
