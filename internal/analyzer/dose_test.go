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
	if !strings.HasSuffix(strings.TrimSpace(out), ".") && !strings.HasSuffix(strings.TrimSpace(out), "текст.") {
		// shrink должен остановиться на границе предложения
		if strings.Contains(out, "текс") && !strings.Contains(out, "текст") {
			t.Fatalf("shrink cut mid-word: %q", out[len(out)-20:])
		}
	}
}

func TestPreferSemanticBreakParagraph(t *testing.T) {
	// Два абзаца; budget попадает в середину второго — откат к границе абзаца.
	p1 := strings.Repeat("Первый абзац целиком. ", 40) // ~880 runes
	p2 := strings.Repeat("Второй абзац тоже длинный. ", 40)
	corpus := p1 + "\n\n" + p2
	runes := []rune(corpus)
	// Цель — примерно середина второго абзаца.
	target := len([]rune(p1)) + 2 + len([]rune(p2))/2
	cut := preferSemanticBreak(runes, 0, target)
	got := string(runes[:cut])
	if strings.Contains(got, "Второй") {
		t.Fatalf("expected cut before second paragraph, got tail=%q", got[len(got)-40:])
	}
	if !strings.Contains(strings.TrimSpace(got), "Первый") {
		t.Fatal("first paragraph missing")
	}
	// Следующая порция начинается со второго абзаца целиком.
	rest := strings.TrimSpace(string(runes[cut:]))
	if !strings.HasPrefix(rest, "Второй") {
		t.Fatalf("next dose should start with second paragraph, got %q", rest[:min(40, len(rest))])
	}
}

func TestBuildDosesKeepsParagraphsWhole(t *testing.T) {
	var parts []string
	for i := 0; i < 20; i++ {
		parts = append(parts, strings.Repeat("Блок смысловой номер. ", 30)+"Маркер"+string(rune('A'+i))+".")
	}
	corpus := strings.Join(parts, "\n\n")
	doses := BuildDoses(corpus, DosePlan{PageChars: 500, DosePages: 2, BudgetChars: 1200})
	if len(doses) < 2 {
		t.Fatalf("expected multiple doses, got %d", len(doses))
	}
	joined := ""
	for _, d := range doses {
		// Ни одна порция не должна обрываться на полуслове «МаркерX» без точки.
		trim := strings.TrimSpace(d.Text)
		if strings.HasSuffix(trim, "Марке") || strings.HasSuffix(trim, "Бло") {
			t.Fatalf("dose %d cut mid-word: …%q", d.Index, trim[max(0, len(trim)-15):])
		}
		// Абзац с МаркерN не должен быть разрезан: если Маркер есть — есть и точка после буквы.
		for c := 'A'; c <= 'T'; c++ {
			tag := "Маркер" + string(c)
			if strings.Contains(d.Text, tag) && !strings.Contains(d.Text, tag+".") {
				t.Fatalf("dose %d split paragraph marker %s", d.Index, tag)
			}
		}
		joined += d.Text
	}
	for c := 'A'; c <= 'T'; c++ {
		tag := "Маркер" + string(c) + "."
		if !strings.Contains(corpus, tag) {
			continue
		}
		if !strings.Contains(joined, tag) {
			t.Fatalf("marker %s lost across doses", tag)
		}
	}
}

func TestPreferSemanticBreakNoMidWord(t *testing.T) {
	text := "Короткое предложение. Затем оченьдлинноесловобезпробелов и ещё текст дальше по смыслу."
	runes := []rune(text)
	// Цель внутри «оченьдлинноесловобезпробелов» (индекс в рунах).
	long := []rune("оченьдлинноесловобезпробелов")
	idx := indexRunes(runes, long)
	if idx < 0 {
		t.Fatal("long word not found")
	}
	cut := preferSemanticBreak(runes, 0, idx+len(long)/2)
	got := strings.TrimSpace(string(runes[:cut]))
	// Нельзя остановиться внутри длинного слова.
	if strings.HasSuffix(got, "очень") || strings.Contains(got, "оченьдлин") && !strings.Contains(got, "оченьдлинноесловобезпробелов") {
		t.Fatalf("cut mid-word: %q", got)
	}
	// Предпочтительно откат к концу предложения до длинного слова.
	if !strings.HasSuffix(got, "предложение.") {
		t.Fatalf("expected rollback to sentence end, got %q", got)
	}
}

func indexRunes(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		ok := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func TestPreferSemanticBreakSentenceFallback(t *testing.T) {
	// Один длинный абзац без \n\n — режем по предложениям.
	s1 := strings.Repeat("Услуги оказываются удалённо. ", 50)
	s2 := strings.Repeat("Обеспечение контракта не требуется. ", 50)
	corpus := s1 + s2
	runes := []rune(corpus)
	target := len([]rune(s1)) + 20 // внутрь второго блока предложений
	cut := preferSemanticBreak(runes, 0, target)
	got := strings.TrimSpace(string(runes[:cut]))
	if !strings.HasSuffix(got, ".") {
		t.Fatalf("expected sentence boundary, tail=%q", got[max(0, len(got)-30):])
	}
	rest := strings.TrimLeft(string(runes[cut:]), " ")
	if !strings.HasPrefix(rest, "Обеспечение") && !strings.HasPrefix(rest, "Услуги") {
		// после точки может быть продолжение того же типа фраз
		if cut <= len([]rune(s1))-5 {
			t.Fatalf("cut too early: %d vs s1=%d", cut, len([]rune(s1)))
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
