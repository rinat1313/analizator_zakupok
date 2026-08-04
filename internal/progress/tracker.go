package progress

import (
	"sync"
)

// Tracker хранит прогресс дозированного анализа по регномеру.
type Tracker struct {
	mu   sync.RWMutex
	byID map[string]Info
}

type Info struct {
	Percent    int    `json:"percent"`
	DosesDone  int    `json:"doses_done"`
	DosesTotal int    `json:"doses_total"`
	Phase      string `json:"phase"`
}

func New() *Tracker {
	return &Tracker{byID: map[string]Info{}}
}

func (t *Tracker) Set(reg string, info Info) {
	if reg == "" {
		return
	}
	t.mu.Lock()
	t.byID[reg] = info
	t.mu.Unlock()
}

func (t *Tracker) Get(reg string) (Info, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	info, ok := t.byID[reg]
	return info, ok
}

func (t *Tracker) Clear(reg string) {
	t.mu.Lock()
	delete(t.byID, reg)
	t.mu.Unlock()
}

// DosePct: порции занимают 0–90%, синтез 90–100%.
func DosePct(done, total int) int {
	if total <= 0 {
		return 5
	}
	if done <= 0 {
		return 5
	}
	pct := int(float64(done) / float64(total) * 90)
	if pct < 5 {
		pct = 5
	}
	if pct > 90 {
		pct = 90
	}
	return pct
}
