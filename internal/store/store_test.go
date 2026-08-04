package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rinat1313/analizator_zakupok/internal/store"
)

func TestSaveAndLoadAnalysis(t *testing.T) {
	root := t.TempDir()
	reg := "12345678901"
	tenderDir := filepath.Join(root, reg, store.DirValidInfo)
	if err := os.MkdirAll(tenderDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tenderDir, "tender.json"), []byte(`{"object_name":"Поставка бумаги","law":"44"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tenderDir, "export.json"), []byte(`{"law":"44","reg_number":"12345678901"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	docDir := filepath.Join(root, reg, store.DirValidDoc)
	if err := os.MkdirAll(docDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docDir, "tz.txt"), []byte("Техническое задание: бумага А4, 80 г/м2."), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.New(root, "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sources, err := st.CollectSources(reg, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) < 2 {
		t.Fatalf("sources=%d", len(sources))
	}

	res := &store.AnalysisResult{
		RegNumber:      reg,
		Status:         "completed",
		ChecklistID:    "default",
		Recommendation: "caution",
		Score:          0.6,
		Summary:        "тест",
	}
	if err := st.SaveAnalysis(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadAnalysis(context.Background(), reg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Recommendation != "caution" {
		t.Fatalf("got %+v", got)
	}
	// дубль в valid_info
	if _, err := os.Stat(filepath.Join(tenderDir, "analysis.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, reg, store.DirAnalysis, "analysis.json")); err != nil {
		t.Fatal(err)
	}
}
