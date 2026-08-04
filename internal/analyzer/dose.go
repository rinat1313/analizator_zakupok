package analyzer

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Dose — порция текста для одного запроса к модели (несколько «страниц»).
type Dose struct {
	Index      int    // 1-based
	Total      int
	PageFrom   int    // 1-based
	PageTo     int
	Text       string
	RuneCount  int
	MoreComing bool
}

// DosePlan настройки нарезки.
type DosePlan struct {
	PageChars   int // рун на «страницу»
	DosePages   int // страниц в одной порции (цель)
	BudgetChars int // максимум рун текста порции под контекст модели
}

func (p DosePlan) normalize() DosePlan {
	if p.PageChars < 400 {
		p.PageChars = 400
	}
	if p.DosePages < 1 {
		p.DosePages = 1
	}
	if p.BudgetChars < 800 {
		p.BudgetChars = 800
	}
	// Порция не может быть больше бюджета.
	maxPages := p.BudgetChars / p.PageChars
	if maxPages < 1 {
		maxPages = 1
		p.PageChars = p.BudgetChars
	}
	if p.DosePages > maxPages {
		p.DosePages = maxPages
	}
	return p
}

// BuildDoses режет весь корпус на порции по ~DosePages страниц, укладываясь в BudgetChars.
func BuildDoses(corpus string, plan DosePlan) []Dose {
	plan = plan.normalize()
	corpus = strings.TrimSpace(corpus)
	if corpus == "" {
		return nil
	}
	runes := []rune(corpus)
	pageSize := plan.PageChars
	doseRunes := plan.DosePages * pageSize
	if doseRunes > plan.BudgetChars {
		doseRunes = plan.BudgetChars
	}
	if doseRunes < 400 {
		doseRunes = 400
	}

	var doses []Dose
	start := 0
	for start < len(runes) {
		end := start + doseRunes
		if end > len(runes) {
			end = len(runes)
		} else {
			end = preferRuneBreak(runes, start, end)
		}
		piece := strings.TrimSpace(string(runes[start:end]))
		if piece == "" {
			if end <= start {
				end = start + 1
			}
			start = end
			continue
		}
		pageFrom := start/pageSize + 1
		pageTo := (end-1)/pageSize + 1
		doses = append(doses, Dose{
			Index:     len(doses) + 1,
			PageFrom:  pageFrom,
			PageTo:    pageTo,
			Text:      piece,
			RuneCount: utf8.RuneCountInString(piece),
		})
		start = end
	}
	total := len(doses)
	for i := range doses {
		doses[i].Index = i + 1
		doses[i].Total = total
		doses[i].MoreComing = i+1 < total
	}
	return doses
}

// ShrinkDose уменьшает текст порции (при Context size exceeded).
func ShrinkDose(text string, factor float64) string {
	if factor <= 0 || factor >= 1 {
		factor = 0.5
	}
	r := []rune(strings.TrimSpace(text))
	n := int(float64(len(r)) * factor)
	if n < 300 && len(r) > 300 {
		n = 300
	}
	if n >= len(r) {
		return text
	}
	cut := preferRuneBreak(r, 0, n)
	return strings.TrimSpace(string(r[:cut]))
}

func preferRuneBreak(runes []rune, start, end int) int {
	if end <= start || end > len(runes) {
		return end
	}
	windowStart := start + (end-start)*2/3
	for i := end; i > windowStart; i-- {
		if runes[i-1] == '\n' {
			return i
		}
	}
	for i := end; i > windowStart; i-- {
		switch runes[i-1] {
		case '.', '!', '?', ';':
			return i
		}
	}
	for i := end; i > windowStart; i-- {
		if runes[i-1] == ' ' {
			return i
		}
	}
	return end
}

func joinCorpus(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n---\n\n")
		}
		b.WriteString(p)
	}
	return b.String()
}

func doseLabel(d Dose) string {
	return fmt.Sprintf("порция %d/%d (стр. ~%d–%d)", d.Index, d.Total, d.PageFrom, d.PageTo)
}
