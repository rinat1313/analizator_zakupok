package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Bundle — системные промпты для LM Studio.
type Bundle struct {
	ItemSystem       string
	SynthesizeSystem string
}

var (
	defaults = Bundle{
		ItemSystem: `Ты — эксперт по госзакупкам РФ (44-ФЗ / 223-ФЗ).
Проанализируй ТОЛЬКО предоставленные фрагменты по пункту чек-листа.
Ответь строго JSON без markdown:
{"status":"ok|warn|fail|unknown","score":0.0,"findings":"...","evidence":["..."]}
status=ok — пункт в порядке; warn — риски/неясности; fail — критично; unknown — данных мало.
score от 0 до 1 (выше = лучше для участия поставщика).`,
		SynthesizeSystem: `Ты — старший аналитик госзакупок. На основе результатов чек-листа сформируй итоговую рекомендацию.
Ответь строго JSON без markdown:
{"recommendation":"participate|caution|skip|unknown","score":0.0,"summary":"...","risks":["..."],"actions":["..."]}
recommendation: participate — участвовать; caution — осторожно/нужна доработка; skip — не участвовать; unknown — недостаточно данных.
score 0..1 — общая привлекательность для поставщика.`,
	}
	cacheMu sync.Mutex
	cache   = map[string]Bundle{}
)

// Load читает configs/prompts/*.txt. При отсутствии файлов — встроенные defaults.
func Load(dir string) (Bundle, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return defaults, nil
	}
	cacheMu.Lock()
	if b, ok := cache[dir]; ok {
		cacheMu.Unlock()
		return b, nil
	}
	cacheMu.Unlock()

	b := defaults
	item, err := readOptional(filepath.Join(dir, "item_system.txt"))
	if err != nil {
		return Bundle{}, err
	}
	if item != "" {
		b.ItemSystem = item
	}
	synth, err := readOptional(filepath.Join(dir, "synthesize_system.txt"))
	if err != nil {
		return Bundle{}, err
	}
	if synth != "" {
		b.SynthesizeSystem = synth
	}

	cacheMu.Lock()
	cache[dir] = b
	cacheMu.Unlock()
	return b, nil
}

// Reload сбрасывает кэш (удобно после правки файлов без рестарта в тестах).
func Reload(dir string) (Bundle, error) {
	cacheMu.Lock()
	delete(cache, dir)
	cacheMu.Unlock()
	return Load(dir)
}

func readOptional(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read prompt %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}
