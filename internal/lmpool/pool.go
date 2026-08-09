package lmpool

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rinat1313/analizator_zakupok/internal/lmstudio"
	"gopkg.in/yaml.v3"
)

// EndpointConfig — одна LM Studio.
type EndpointConfig struct {
	Name    string `yaml:"name" json:"name"`
	BaseURL string `yaml:"base_url" json:"base_url"`
	APIKey  string `yaml:"api_key" json:"api_key"`
	Model   string `yaml:"model" json:"model"`
}

type FileConfig struct {
	Endpoints         []EndpointConfig `yaml:"endpoints"`
	HealthIntervalSec int              `yaml:"health_interval_sec"`
}

type holdKey struct{}

// Pool — ровно одна LM Studio, эксклюзивный доступ (1 процесс за раз).
// Параллельные слоты отключены: max_parallel всегда 1.
type Pool struct {
	cfg     EndpointConfig
	client  *lmstudio.Client
	healthy atomic.Bool
	busy    atomic.Bool
	lastErr atomic.Value // string

	exclusive sync.Mutex

	timeout     time.Duration
	healthEvery time.Duration
}

type Status struct {
	Total       int              `json:"total"`
	Healthy     int              `json:"healthy"`
	Busy        int              `json:"busy"`
	Available   int              `json:"available"`
	MaxParallel int              `json:"max_parallel"`
	Mode        string           `json:"mode"`
	Endpoints   []EndpointStatus `json:"endpoints"`
}

type EndpointStatus struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	Healthy bool   `json:"healthy"`
	Busy    bool   `json:"busy"`
	Error   string `json:"error,omitempty"`
}

// Load создаёт single-LLM пул: ровно 1 endpoint.
// Приоритет: LM_STUDIO_BASE_URL (fallback) → иначе первый из YAML.
// Лишние endpoint'ы в YAML игнорируются.
func Load(path string, fallback lmstudio.Options) (*Pool, error) {
	fc := FileConfig{HealthIntervalSec: 15}
	if path == "" {
		path = os.Getenv("LM_STUDIO_ENDPOINTS_FILE")
	}
	if path == "" {
		path = "configs/lm_studio.yaml"
	}
	if b, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(b, &fc)
		log.Printf("lmpool: loaded %s (%d endpoints in file)", path, len(fc.Endpoints))
	} else {
		log.Printf("lmpool: no file %s (%v) — using LM_STUDIO_BASE_URL", path, err)
	}

	ep, err := pickSingleEndpoint(fc.Endpoints, fallback)
	if err != nil {
		return nil, err
	}

	timeout := fallback.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	healthEvery := time.Duration(fc.HealthIntervalSec) * time.Second
	if healthEvery <= 0 {
		healthEvery = 15 * time.Second
	}

	cl := lmstudio.New(lmstudio.Options{
		BaseURL:     ep.BaseURL,
		APIKey:      ep.APIKey,
		Model:       ep.Model,
		Timeout:     timeout,
		Temperature: fallback.Temperature,
		MaxTokens:   fallback.MaxTokens,
	})

	p := &Pool{
		cfg:         ep,
		client:      cl,
		timeout:     timeout,
		healthEvery: healthEvery,
	}
	p.healthy.Store(true)
	p.lastErr.Store("")
	log.Printf("lmpool: single-LLM mode endpoint=%s model=%s max_parallel=1", ep.BaseURL, ep.Model)
	return p, nil
}

func pickSingleEndpoint(fromFile []EndpointConfig, fallback lmstudio.Options) (EndpointConfig, error) {
	if len(fromFile) > 1 {
		log.Printf("lmpool: single-LLM mode — ignoring %d extra endpoint(s) from config", len(fromFile)-1)
	}

	normalize := func(ep EndpointConfig) EndpointConfig {
		ep.BaseURL = strings.TrimRight(strings.TrimSpace(ep.BaseURL), "/")
		if ep.APIKey == "" {
			ep.APIKey = fallback.APIKey
		}
		if ep.APIKey == "" {
			ep.APIKey = "lm-studio"
		}
		if ep.Model == "" {
			ep.Model = fallback.Model
		}
		if ep.Name == "" {
			ep.Name = "default"
		}
		return ep
	}

	if strings.TrimSpace(fallback.BaseURL) != "" {
		return normalize(EndpointConfig{
			Name:    "default",
			BaseURL: fallback.BaseURL,
			APIKey:  fallback.APIKey,
			Model:   fallback.Model,
		}), nil
	}
	if len(fromFile) > 0 {
		ep := normalize(fromFile[0])
		if ep.BaseURL == "" {
			return EndpointConfig{}, fmt.Errorf("lmpool: empty base_url in configs/lm_studio.yaml")
		}
		return ep, nil
	}
	return EndpointConfig{}, fmt.Errorf("lmpool: no LM Studio endpoint configured (LM_STUDIO_BASE_URL or configs/lm_studio.yaml)")
}

func (p *Pool) StartHealth(ctx context.Context) {
	p.Refresh(ctx)
	t := time.NewTicker(p.healthEvery)
	go func() {
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.Refresh(ctx)
			}
		}
	}()
}

func (p *Pool) Refresh(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := p.client.Ping(cctx)
	if err != nil {
		p.healthy.Store(false)
		p.lastErr.Store(err.Error())
		return
	}
	p.healthy.Store(true)
	p.lastErr.Store("")
}

// MaxParallel всегда 1 — один процесс на одну LLM.
func (p *Pool) MaxParallel() int { return 1 }

func (p *Pool) HealthyCount() int {
	if p.healthy.Load() {
		return 1
	}
	return 0
}

func (p *Pool) Status() Status {
	errStr, _ := p.lastErr.Load().(string)
	busy := p.busy.Load()
	healthy := p.healthy.Load()
	st := Status{
		Total:       1,
		MaxParallel: 1,
		Mode:        "single_exclusive",
		Endpoints: []EndpointStatus{{
			Name:    p.cfg.Name,
			BaseURL: p.cfg.BaseURL,
			Model:   p.cfg.Model,
			Healthy: healthy,
			Busy:    busy,
			Error:   errStr,
		}},
	}
	if healthy {
		st.Healthy = 1
	}
	if busy {
		st.Busy = 1
	}
	st.Available = st.Healthy - st.Busy
	if st.Available < 0 {
		st.Available = 0
	}
	return st
}

// TryHold — мгновенный захват. ok=false, если LLM уже занята (для smoke).
func (p *Pool) TryHold(ctx context.Context) (context.Context, func(), bool) {
	if !p.exclusive.TryLock() {
		return ctx, nil, false
	}
	return p.afterLock(ctx), p.makeRelease(), true
}

// Hold ждёт эксклюзивный доступ (очередь для UI/auto-AI по поисковым настройкам).
// Пока ждём — в LM Studio ничего не отправляется. Отмена ctx снимает ожидание.
func (p *Pool) Hold(ctx context.Context) (context.Context, func(), error) {
	if held, release, ok := p.TryHold(ctx); ok {
		return held, release, nil
	}
	locked := make(chan struct{})
	go func() {
		p.exclusive.Lock()
		close(locked)
	}()
	select {
	case <-locked:
		return p.afterLock(ctx), p.makeRelease(), nil
	case <-ctx.Done():
		go func() {
			<-locked
			p.exclusive.Unlock()
		}()
		return ctx, nil, fmt.Errorf("lmpool: ожидание LLM отменено: %w", ctx.Err())
	}
}

func (p *Pool) afterLock(ctx context.Context) context.Context {
	p.busy.Store(true)
	return context.WithValue(ctx, holdKey{}, p)
}

func (p *Pool) makeRelease() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			p.busy.Store(false)
			p.exclusive.Unlock()
		})
	}
}

// ChatMaxTokens отправляет запрос только если:
//   - ctx уже под Hold (тот же анализ), или
//   - удалось мгновенно взять эксклюзивный доступ (одиночный smoke).
// Иначе — ошибка, в LM Studio ничего не уходит (не встаём в очередь chat'ом).
func (p *Pool) ChatMaxTokens(ctx context.Context, messages []lmstudio.Message, maxTokens int) (string, string, error) {
	if owned, _ := ctx.Value(holdKey{}).(*Pool); owned == p {
		return p.chat(ctx, messages, maxTokens)
	}

	releaseCtx, release, ok := p.TryHold(ctx)
	if !ok {
		return "", "", fmt.Errorf("lmpool: LLM занята — дождитесь завершения текущего анализа")
	}
	defer release()
	return p.chat(releaseCtx, messages, maxTokens)
}

func (p *Pool) chat(ctx context.Context, messages []lmstudio.Message, maxTokens int) (string, string, error) {
	content, model, err := p.client.ChatMaxTokens(ctx, messages, maxTokens)
	if err != nil {
		if strings.Contains(err.Error(), "connection") || strings.Contains(err.Error(), "timeout") {
			p.healthy.Store(false)
			p.lastErr.Store(err.Error())
		}
		return "", "", err
	}
	return content, model, nil
}

func (p *Pool) Ping(ctx context.Context) error {
	p.Refresh(ctx)
	if !p.healthy.Load() {
		errStr, _ := p.lastErr.Load().(string)
		if errStr == "" {
			errStr = "LM Studio недоступна"
		}
		return fmt.Errorf("lmpool: %s", errStr)
	}
	return nil
}

func (p *Pool) Model() string   { return p.cfg.Model }
func (p *Pool) BaseURL() string { return p.cfg.BaseURL }
