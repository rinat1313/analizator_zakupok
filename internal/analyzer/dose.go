package analyzer

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Dose — порция текста для одного запроса к модели (несколько «страниц»).
type Dose struct {
	Index      int // 1-based
	Total      int
	PageFrom   int // 1-based
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
// Границы порций — смысловые: абзац → предложение → строка → слово (без разреза посередине).
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
			end = preferSemanticBreak(runes, start, end)
		}
		// Пропуск ведущих разделителей абзацев у следующей порции (абзац остаётся целым).
		for end < len(runes) && (runes[end] == '\n' || runes[end] == '\r') {
			end++
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

// ShrinkDose уменьшает текст порции (при Context size exceeded), сохраняя смысловые границы.
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
	cut := preferSemanticBreak(r, 0, n)
	return strings.TrimSpace(string(r[:cut]))
}

// preferSemanticBreak выбирает границу порции ≤ end так, чтобы не резать абзацы и предложения.
//
// Приоритет (от сильного к слабому):
//  1. граница абзаца (\n\n / пустая строка);
//  2. конец предложения (. ! ? …) с пробелом/переносом после;
//  3. конец строки (\n);
//  4. граница слова (пробел).
//
// Если целевой end попадает в середину слова или строки — откат к ближайшей
// более сильной границе. Абзацы не делятся: текущий уходит целиком в эту порцию,
// следующий начинается с начала следующего абзаца.
func preferSemanticBreak(runes []rune, start, end int) int {
	if end <= start || end > len(runes) {
		return end
	}
	if end == len(runes) {
		return end
	}

	span := end - start
	// Не сжимаем порцию сильнее ~35% цели — иначе слишком мелкие куски.
	minCut := start + span*35/100
	if minCut <= start {
		minCut = start + 1
	}

	// 1) Абзац — основной смысловой блок.
	if cut := findParagraphBreak(runes, start, end, minCut); cut > start {
		return cut
	}

	// 2) Конец предложения (после точки и т.п.).
	if cut := findSentenceBreak(runes, start, end, minCut); cut > start {
		return cut
	}

	// 3) Конец строки — не оставляем половину строки.
	if cut := findLineBreak(runes, start, end, minCut); cut > start {
		return cut
	}

	// 4) Граница слова — никогда не режем слово пополам.
	if cut := findWordBreak(runes, start, end, minCut); cut > start {
		return cut
	}

	// Крайний случай: огромный абзац/слово без пробелов — режем по budget,
	// но если end внутри слова, откатываемся к началу слова (если оно не с start).
	if end < len(runes) && isWordRune(runes[end-1]) && isWordRune(runes[end]) {
		for i := end; i > start; i-- {
			if !isWordRune(runes[i-1]) {
				return i
			}
		}
	}
	return end
}

// findParagraphBreak — ближайший к end разрыв абзаца (≥ minCut): позиция сразу после ≥2 \n.
func findParagraphBreak(runes []rune, start, end, minCut int) int {
	for i := end; i >= minCut; i-- {
		if isParagraphBoundaryAt(runes, start, i) {
			return i
		}
	}
	return -1
}

// isParagraphBoundaryAt: i стоит сразу после блока из ≥2 переносов строк (абзац).
func isParagraphBoundaryAt(runes []rune, start, i int) bool {
	if i <= start || i > len(runes) {
		return false
	}
	nCount := 0
	j := i
	for j > start {
		r := runes[j-1]
		if r == '\n' {
			nCount++
			j--
			continue
		}
		if r == '\r' {
			j--
			continue
		}
		break
	}
	return nCount >= 2
}

// findSentenceBreak — конец предложения: . ! ? … затем пробел/перевод строки или конец.
func findSentenceBreak(runes []rune, start, end, minCut int) int {
	for i := end; i > minCut; i-- {
		r := runes[i-1]
		if !isSentenceEnd(r) {
			continue
		}
		// Не режем на «т.д.» / «№12.» внутри слова цифр без пробела после — требуем разделитель.
		if i < len(runes) {
			next := runes[i]
			if next != ' ' && next != '\n' && next != '\r' && next != '\t' &&
				next != '"' && next != '»' && next != ')' && next != ']' {
				// допускаем кавычку/скобку сразу после точки, затем пробел
				if unicode.IsLetter(next) || unicode.IsDigit(next) {
					continue
				}
			}
		}
		// Пропускаем цепочку закрывающих кавычек/скобок после точки.
		cut := i
		for cut < end && cut < len(runes) {
			switch runes[cut] {
			case '"', '\'', '»', ')', ']', '…':
				cut++
				continue
			}
			break
		}
		if cut >= minCut && cut <= end {
			return cut
		}
	}
	return -1
}

func findLineBreak(runes []rune, start, end, minCut int) int {
	for i := end; i > minCut; i-- {
		if runes[i-1] == '\n' {
			return i
		}
	}
	return -1
}

func findWordBreak(runes []rune, start, end, minCut int) int {
	for i := end; i > minCut; i-- {
		if unicode.IsSpace(runes[i-1]) {
			return i
		}
	}
	return -1
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?', '…':
		return true
	default:
		return false
	}
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
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

// preferRuneBreak — устаревший алиас; оставлен для совместимости тестов/вызовов.
func preferRuneBreak(runes []rune, start, end int) int {
	return preferSemanticBreak(runes, start, end)
}
