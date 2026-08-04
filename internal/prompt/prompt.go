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
	DoseSystem       string
	SynthesizeSystem string
	// legacy aliases (старые файлы)
	ItemSystem string
}

var (
	defaults = Bundle{
		DoseSystem: `Ты — эксперт по госзакупкам РФ (44-ФЗ / 223-ФЗ), фокус: участие самозанятого (НПД / физлицо).
Тебе дают ОДНУ порцию текста закупки (не весь документ). Дальше могут прийти ещё порции.
Задача: краткие заметки ТОЛЬКО по этой порции для будущего итогового вердикта.
Не выноси окончательное решение «участвовать/не участвовать» по одной порции, если сказано что будут ещё части.
Ищи признаки: допуск/запрет физлиц и самозанятых, требование юрлица/ИП, СМП, лицензии, СРО, обеспечение, опыт, ЭЦП, субподряд.

Ответь строго JSON без markdown:
{"status":"ok|warn|fail|unknown|neutral","score":0.0,"notes":"...","flags":["..."]}
status: ok — для самозанятого благоприятно/нейтрально-позитивно; warn — ограничения/неясности; fail — явный стоп-фактор для самозанятого; neutral — ничего релевантного; unknown — данных мало.
score 0..1 — насколько эта порция поддерживает возможность участия самозанятого.
notes — 2–5 коротких предложений на русском.
flags — короткие метки рисков/фактов (можно пустой массив).`,
		SynthesizeSystem: `Ты — старший аналитик госзакупок. Вопрос по умолчанию:
«Оцени закупку по возможности участия самозанятого».

На входе — краткие заметки по порциям документов (не сырой текст). Сложи их в итоговую рекомендацию.
Не выдумывай факты, которых нет в заметках.

Ответь строго JSON без markdown:
{"recommendation":"participate|caution|skip|unknown","score":0.0,"summary":"...","risks":["..."],"actions":["..."]}
recommendation: participate — самозанятому целесообразно участвовать; caution — можно, но с оговорками; skip — не стоит / нельзя; unknown — данных мало.
score 0..1 — итоговая оценка возможности/целесообразности участия самозанятого.
summary — 4–8 предложений на русском, явный ответ на вопрос про самозанятого.
risks — ключевые риски; actions — что проверить/сделать дальше.`,
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
	dose, err := readOptional(filepath.Join(dir, "dose_system.txt"))
	if err != nil {
		return Bundle{}, err
	}
	if dose != "" {
		b.DoseSystem = dose
	} else if item, err := readOptional(filepath.Join(dir, "item_system.txt")); err == nil && item != "" {
		// fallback на старый файл
		b.DoseSystem = item
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
