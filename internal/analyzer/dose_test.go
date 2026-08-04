package analyzer

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildDosesPages(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString(strings.Repeat("абзац ", 50))
		b.WriteString("\n\n")
	}
	corpus := b.String()
	doses := BuildDoses(corpus, DosePlan{PageChars: 1800, DosePages: 5, BudgetChars: 10000})
	if len(doses) < 2 {
		t.Fatalf("expected several doses, got %d", len(doses))
	}
	last := doses[len(doses)-1]
	if last.Total != len(doses) {
		t.Fatalf("total mismatch")
	}
	if last.MoreComing {
		t.Fatal("last dose must not have more coming")
	}
	if !doses[0].MoreComing && len(doses) > 1 {
		t.Fatal("first dose should announce more coming")
	}
	for _, d := range doses {
		if d.RuneCount > 10000 {
			t.Fatalf("dose %d exceeds budget: %d", d.Index, d.RuneCount)
		}
	}
}

func TestBuildDosesRespectsBudget(t *testing.T) {
	text := strings.Repeat("слово ", 5000)
	doses := BuildDoses(text, DosePlan{PageChars: 2000, DosePages: 5, BudgetChars: 3000})
	if len(doses) == 0 {
		t.Fatal("no doses")
	}
	for _, d := range doses {
		if utf8.RuneCountInString(d.Text) > 3200 {
			t.Fatalf("dose too large: %d", utf8.RuneCountInString(d.Text))
		}
	}
}

func TestShrinkDose(t *testing.T) {
	in := strings.Repeat("текст. ", 400)
	out := ShrinkDose(in, 0.5)
	if utf8.RuneCountInString(out) >= utf8.RuneCountInString(in) {
		t.Fatal("expected shrink")
	}
	if utf8.RuneCountInString(out) < 200 {
		t.Fatal("over-shrunk")
	}
}
