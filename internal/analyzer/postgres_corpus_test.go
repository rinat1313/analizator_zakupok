package analyzer

import (
	"context"
	"strings"
	"testing"

	"github.com/rinat1313/analizator_zakupok/internal/config"
	"github.com/rinat1313/analizator_zakupok/internal/lmstudio"
	"github.com/rinat1313/analizator_zakupok/internal/store"
)

// Регрессия: SaveAnalysis создаёт каталог тендера → старый код шёл в CollectSources
// и падал с «ожидаются valid_info/», хотя text из Postgres уже был.
func TestAnalyzeUsesPostgresTextWithoutFiles(t *testing.T) {
	root := t.TempDir()
	st, err := store.New(root, "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	cfg := config.Config{
		TendersRoot:        root,
		PageChars:          500,
		DosePages:          2,
		ContextBudgetChars: 2000,
		DoseMaxTokens:      32,
		SynthMaxTokens:     32,
		PromptsDir:         "",
	}
	llm := lmstudio.New(lmstudio.Options{
		BaseURL: "http://127.0.0.1:1/v1", // недоступен — упадём на первой порции, но не на CollectSources
		Model:   "x",
	})
	svc := New(cfg, llm, st)

	reg := "0167300000526000547"
	corpus := "Объект закупки: тест\nЗакон: 44\n\n--- документ 1 ---\n" + strings.Repeat("Текст контракта для самозанятого. ", 80)

	// Имитируем прошлый баг: каталог уже есть (как после SaveAnalysis).
	_ = st.SaveAnalysis(context.Background(), &store.AnalysisResult{
		RegNumber: reg,
		Status:    "running",
	})
	if !st.Exists(reg) {
		t.Fatal("expected tender dir to exist")
	}

	_, err = svc.Analyze(context.Background(), Request{
		RegNumber: reg,
		Text:      corpus,
		Title:     "тест",
	})
	if err == nil {
		t.Fatal("expected LLM connection error, got nil")
	}
	if strings.Contains(err.Error(), "valid_info") || strings.Contains(err.Error(), "valid_doc") {
		t.Fatalf("must use postgres text, not fail on files: %v", err)
	}
	// Должны дойти до вызова LM Studio (connection refused / request error), значит corpus собран.
	if !strings.Contains(err.Error(), "lm studio") && !strings.Contains(err.Error(), "connection") && !strings.Contains(err.Error(), "порция") {
		t.Logf("got err=%v (acceptable if dose wrapper)", err)
	}
}
