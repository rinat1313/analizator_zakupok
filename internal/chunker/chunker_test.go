package chunker_test

import (
	"strings"
	"testing"

	"github.com/rinat1313/analizator_zakupok/internal/chunker"
)

func TestSplitSmall(t *testing.T) {
	chunks := chunker.Split("a.txt", "короткий текст", chunker.Options{Size: 100, Overlap: 10})
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Text != "короткий текст" {
		t.Fatalf("unexpected text: %q", chunks[0].Text)
	}
}

func TestSplitLargeWithOverlap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("Абзац номер ")
		b.WriteString(strings.Repeat("слово ", 20))
		b.WriteString(".\n\n")
	}
	text := b.String()
	chunks := chunker.Split("doc.txt", text, chunker.Options{Size: 200, Overlap: 40, Max: 10})
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	if len(chunks) > 10 {
		t.Fatalf("max chunks exceeded: %d", len(chunks))
	}
	for i, c := range chunks {
		if c.Source != "doc.txt" {
			t.Fatalf("chunk %d source=%q", i, c.Source)
		}
		if strings.TrimSpace(c.Text) == "" {
			t.Fatalf("chunk %d empty", i)
		}
	}
}

func TestSelectRelevant(t *testing.T) {
	chunks := []chunker.Chunk{
		{ID: "1", Text: "описание предмета закупки мебель", Index: 0},
		{ID: "2", Text: "обеспечение контракта составляет 10 процентов", Index: 1},
		{ID: "3", Text: "погода сегодня солнечная", Index: 2},
	}
	got := chunker.SelectRelevant(chunks, []string{"обеспечение", "контракт"}, 2)
	if len(got) == 0 {
		t.Fatal("expected selections")
	}
	if got[0].ID != "2" {
		t.Fatalf("expected chunk 2 first, got %s", got[0].ID)
	}
}
