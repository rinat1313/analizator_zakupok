package lmpool

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rinat1313/analizator_zakupok/internal/lmstudio"
	"gopkg.in/yaml.v3"
)

// EndpointConfig — одна LM Studio.
// Concurrent > 1 создаёт несколько слотов на один base_url
// (удобно для одного сервера с lms load --parallel N).
type EndpointConfig struct {
	Name       string `yaml:"name" json:"name"`
	BaseURL    string `yaml:"base_url" json:"base_url"`
	APIKey     string `yaml:"api_key" json:"api_key"`
	Model      string `yaml:"model" json:"model"`
	Concurrent int    `yaml:"concurrent" json:"concurrent"`
}

type FileConfig struct {
	Endpoints         []EndpointConfig `yaml:"endpoints"`
	HealthIntervalSec int              `yaml:"health_interval_sec"`
}

type slot struct {
	cfg     EndpointConfig
	client  *lmstudio.Client
	healthy atomic.Bool
	busy    atomic.Bool
	lastErr atomic.Value // string
}

// Pool — набор LM Studio; Chat берёт свободный живой endpoint.
type Pool struct {
	slots   []*slot
	timeout time.Duration
	temp    float64
	maxTok  int

	healthEvery time.Duration
	mu          sync.Mutex
}

type Status struct {
	Total     int              `json:"total"`
	Healthy   int              `json:"healthy"`
	Busy      int              `json:"busy"`
	Available int              `json:"available"`
	MaxParallel int            `json:"max_parallel"`
	Endpoints []EndpointStatus `json:"endpoints"`
}

type EndpointStatus struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	Healthy bool   `json:"healthy"`
	Busy    bool   `json:"busy"`
	Error   string `json:"error,omitempty"`
}

// Load создаёт пул из YAML + fallback env endpoint.
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
		log.Printf("lmpool: loaded %s (%d endpoints)", path, len(fc.Endpoints))
	} else {
		log.Printf("lmpool: no file %s (%v) — using LM_STUDIO_BASE_URL", path, err)
	}

	p := &Pool{
		timeout:     fallback.Timeout,
		temp:        fallback.Temperature,
		maxTok:      fallback.MaxTokens,
		healthEvery: time.Duration(fc.HealthIntervalSec) * time.Second,
	}
	if p.healthEvery <= 0 {
		p.healthEvery = 15 * time.Second
	}
	if p.timeout <= 0 {
		p.timeout = 5 * time.Minute
	}

	seen := map[string]bool{}
	add := func(ep EndpointConfig) {
		ep.BaseURL = rewriteDockerLocalhost(strings.TrimRight(strings.TrimSpace(ep.BaseURL), "/"))
		if ep.BaseURL == "" {
			return
		}
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
			ep.Name = ep.BaseURL
		}
		n := ep.Concurrent
		if n < 1 {
			n = 1
		}
		for i := 0; i < n; i++ {
			key := fmt.Sprintf("%s#%d", ep.BaseURL, i)
			if seen[key] {
				continue
			}
			seen[key] = true
			cfg := ep
			cfg.Concurrent = 1
			if n > 1 {
				cfg.Name = fmt.Sprintf("%s-%d", ep.Name, i+1)
			}
			cl := lmstudio.New(lmstudio.Options{
				BaseURL:     cfg.BaseURL,
				APIKey:      cfg.APIKey,
				Model:       cfg.Model,
				Timeout:     p.timeout,
				Temperature: p.temp,
				MaxTokens:   p.maxTok,
			})
			s := &slot{cfg: cfg, client: cl}
			s.healthy.Store(true) // optimistic until first health fail
			s.lastErr.Store("")
			p.slots = append(p.slots, s)
		}
	}

	for _, ep := range fc.Endpoints {
		add(ep)
	}
	if fallback.BaseURL != "" {
		add(EndpointConfig{
			Name:    "env-default",
			BaseURL: fallback.BaseURL,
			APIKey:  fallback.APIKey,
			Model:   fallback.Model,
		})
	}
	if len(p.slots) == 0 {
		return nil, fmt.Errorf("lmpool: no LM Studio endpoints configured")
	}
	return p, nil
}

// rewriteDockerLocalhost меняет 127.0.0.1/localhost → host.docker.internal
// внутри контейнера (иначе LM Studio на Mac недоступен).
func rewriteDockerLocalhost(u string) string {
	if u == "" || os.Getenv("LM_STUDIO_REWRITE_LOCALHOST") == "0" {
		return u
	}
	inDocker := os.Getenv("ZAKUPKI_IN_DOCKER") == "1"
	if !inDocker {
		if _, err := os.Stat("/.dockerenv"); err == nil {
			inDocker = true
		}
	}
	if !inDocker {
		return u
	}
	repls := []struct{ old, neu string }{
		{"http://127.0.0.1:", "http://host.docker.internal:"},
		{"http://localhost:", "http://host.docker.internal:"},
		{"https://127.0.0.1:", "https://host.docker.internal:"},
		{"https://localhost:", "https://host.docker.internal:"},
	}
	for _, r := range repls {
		if strings.HasPrefix(u, r.old) {
			out := r.neu + strings.TrimPrefix(u, r.old)
			if out != u {
				log.Printf("lmpool: rewrite %s → %s (docker)", u, out)
			}
			return out
		}
	}
	return u
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
	var wg sync.WaitGroup
	for _, s := range p.slots {
		wg.Add(1)
		go func(s *slot) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			err := s.client.Ping(cctx)
			if err != nil {
				s.healthy.Store(false)
				s.lastErr.Store(err.Error())
			} else {
				s.healthy.Store(true)
				s.lastErr.Store("")
			}
		}(s)
	}
	wg.Wait()
}

func envMaxParallel() int {
	const defaultMax = 4
	v := strings.TrimSpace(os.Getenv("LM_MAX_PARALLEL"))
	if v == "" {
		return defaultMax
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return defaultMax
	}
	if n > 64 {
		return 64
	}
	return n
}

func (p *Pool) MaxParallel() int {
	h := p.HealthyCount()
	if h < 1 {
		return 0
	}
	// Параллелизм = число живых слотов LM Studio, не CPU контейнера analizator
	// (LLM крутится на хосте). Потолок по умолчанию 4 (LM_MAX_PARALLEL).
	maxN := envMaxParallel()
	if h > maxN {
		return maxN
	}
	return h
}

func (p *Pool) HealthyCount() int {
	n := 0
	for _, s := range p.slots {
		if s.healthy.Load() {
			n++
		}
	}
	return n
}

func (p *Pool) Status() Status {
	st := Status{Total: len(p.slots), MaxParallel: p.MaxParallel()}
	for _, s := range p.slots {
		errStr, _ := s.lastErr.Load().(string)
		es := EndpointStatus{
			Name:    s.cfg.Name,
			BaseURL: s.cfg.BaseURL,
			Model:   s.cfg.Model,
			Healthy: s.healthy.Load(),
			Busy:    s.busy.Load(),
			Error:   errStr,
		}
		if es.Healthy {
			st.Healthy++
		}
		if es.Busy {
			st.Busy++
		}
		st.Endpoints = append(st.Endpoints, es)
	}
	st.Available = st.Healthy - st.Busy
	if st.Available < 0 {
		st.Available = 0
	}
	return st
}

func (p *Pool) acquire(ctx context.Context) (*slot, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		for _, s := range p.slots {
			if !s.healthy.Load() {
				continue
			}
			if s.busy.CompareAndSwap(false, true) {
				return s, nil
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lmpool: no free LM Studio endpoint")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (p *Pool) release(s *slot) {
	if s != nil {
		s.busy.Store(false)
	}
}

// ChatMaxTokens — совместимо с lmstudio.Client.
func (p *Pool) ChatMaxTokens(ctx context.Context, messages []lmstudio.Message, maxTokens int) (string, string, error) {
	s, err := p.acquire(ctx)
	if err != nil {
		return "", "", err
	}
	defer p.release(s)
	content, model, err := s.client.ChatMaxTokens(ctx, messages, maxTokens)
	if err != nil {
		// при ошибке связи — пометить unhealthy
		if strings.Contains(err.Error(), "connection") || strings.Contains(err.Error(), "timeout") {
			s.healthy.Store(false)
			s.lastErr.Store(err.Error())
		}
		return "", "", err
	}
	return content, model, nil
}

func (p *Pool) Ping(ctx context.Context) error {
	p.Refresh(ctx)
	if p.HealthyCount() == 0 {
		return fmt.Errorf("lmpool: все LM Studio недоступны")
	}
	return nil
}

func (p *Pool) Model() string {
	for _, s := range p.slots {
		if s.healthy.Load() {
			return s.cfg.Model
		}
	}
	if len(p.slots) > 0 {
		return p.slots[0].cfg.Model
	}
	return ""
}

func (p *Pool) BaseURL() string {
	for _, s := range p.slots {
		if s.healthy.Load() {
			return s.cfg.BaseURL
		}
	}
	if len(p.slots) > 0 {
		return p.slots[0].cfg.BaseURL
	}
	return ""
}
