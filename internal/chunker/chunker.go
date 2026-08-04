package chunker

import (
	"strings"
	"unicode/utf8"
)

// Chunk — фрагмент текста для подачи в LLM.
type Chunk struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Index    int    `json:"index"`
	Text     string `json:"text"`
	CharFrom int    `json:"char_from"`
	CharTo   int    `json:"char_to"`
}

// Options настройки нарезки.
type Options struct {
	Size    int // целевой размер куска в рунах
	Overlap int
	Max     int // максимум кусков на один источник (0 = без лимита)
}

// Split режет текст на перекрывающиеся куски по границам абзацев/предложений.
func Split(source, text string, opt Options) []Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if opt.Size < 200 {
		opt.Size = 200
	}
	if opt.Overlap < 0 {
		opt.Overlap = 0
	}
	if opt.Overlap >= opt.Size {
		opt.Overlap = opt.Size / 5
	}

	runes := []rune(text)
	if len(runes) <= opt.Size {
		return []Chunk{{
			ID:       source + "#0",
			Source:   source,
			Index:    0,
			Text:     text,
			CharFrom: 0,
			CharTo:   utf8.RuneCountInString(text),
		}}
	}

	var out []Chunk
	start := 0
	for start < len(runes) {
		end := start + opt.Size
		if end > len(runes) {
			end = len(runes)
		} else {
			end = preferBreak(runes, start, end)
		}
		piece := strings.TrimSpace(string(runes[start:end]))
		if piece != "" {
			out = append(out, Chunk{
				ID:       source + "#" + itoa(len(out)),
				Source:   source,
				Index:    len(out),
				Text:     piece,
				CharFrom: start,
				CharTo:   end,
			})
		}
		if end >= len(runes) {
			break
		}
		next := end - opt.Overlap
		if next <= start {
			next = end
		}
		start = next
		if opt.Max > 0 && len(out) >= opt.Max {
			break
		}
	}
	return out
}

// SelectRelevant выбирает куски, релевантные keywords (простое scoring).
// Если keywords пусты — возвращает все куски (с лимитом limit).
func SelectRelevant(chunks []Chunk, keywords []string, limit int) []Chunk {
	if len(chunks) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(chunks) {
		limit = len(chunks)
	}
	if len(keywords) == 0 {
		if len(chunks) > limit {
			return chunks[:limit]
		}
		return chunks
	}

	type scored struct {
		c Chunk
		s int
	}
	normKW := make([]string, 0, len(keywords))
	for _, k := range keywords {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" {
			normKW = append(normKW, k)
		}
	}
	var ranked []scored
	for _, c := range chunks {
		low := strings.ToLower(c.Text)
		score := 0
		for _, k := range normKW {
			score += strings.Count(low, k)
		}
		// лёгкий бонус первым кускам источника (часто summary/шапка)
		if c.Index == 0 {
			score++
		}
		ranked = append(ranked, scored{c: c, s: score})
	}
	// partial selection sort top-N
	for i := 0; i < len(ranked) && i < limit; i++ {
		best := i
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].s > ranked[best].s {
				best = j
			}
		}
		ranked[i], ranked[best] = ranked[best], ranked[i]
	}
	n := limit
	if n > len(ranked) {
		n = len(ranked)
	}
	// отбрасываем нулевые, но оставляем хотя бы 1
	out := make([]Chunk, 0, n)
	for i := 0; i < n; i++ {
		if ranked[i].s == 0 && len(out) > 0 && i > 0 {
			continue
		}
		out = append(out, ranked[i].c)
	}
	if len(out) == 0 && len(ranked) > 0 {
		out = append(out, ranked[0].c)
	}
	return out
}

func preferBreak(runes []rune, start, end int) int {
	// ищем ближайший разрыв в последней трети окна
	windowStart := start + (end-start)*2/3
	for i := end; i > windowStart; i-- {
		switch runes[i-1] {
		case '\n':
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

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
