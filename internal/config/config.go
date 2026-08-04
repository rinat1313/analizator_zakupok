package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config — настройки сервиса анализа закупок.
type Config struct {
	HTTPAddr string

	// Данные парсера (общий volume с ZakupkiParser).
	TendersRoot string

	// LM Studio OpenAI-compatible API.
	LMStudioBaseURL string
	LMStudioAPIKey  string
	LMStudioModel   string
	LMStudioTimeout time.Duration
	Temperature     float64
	MaxTokens       int

	// Chunking больших текстов.
	ChunkSize    int
	ChunkOverlap int
	MaxChunks    int // 0 = без лимита

	// Чек-листы и промпты.
	ChecklistsDir string
	PromptsDir    string
	DefaultList   string

	// PostgreSQL (опционально): дублирование раздела analysis.
	DatabaseURL string

	// Сколько параллельных запросов к LM Studio.
	Concurrency int
}

// Load читает конфиг из переменных окружения.
func Load() Config {
	cfg := Config{
		HTTPAddr:        env("HTTP_ADDR", ":8088"),
		TendersRoot:     env("TENDERS_ROOT", "/data/tenders"),
		LMStudioBaseURL: strings.TrimRight(env("LM_STUDIO_BASE_URL", "http://127.0.0.1:1234/v1"), "/"),
		LMStudioAPIKey:  env("LM_STUDIO_API_KEY", "lm-studio"),
		LMStudioModel:   env("LM_STUDIO_MODEL", "local-model"),
		LMStudioTimeout: envDuration("LM_STUDIO_TIMEOUT", 5*time.Minute),
		Temperature:     envFloat("LM_TEMPERATURE", 0.2),
		MaxTokens:       envInt("LM_MAX_TOKENS", 2048),
		ChunkSize:       envInt("CHUNK_SIZE", 3500),
		ChunkOverlap:    envInt("CHUNK_OVERLAP", 250),
		MaxChunks:       envInt("MAX_CHUNKS", 40),
		ChecklistsDir:   env("CHECKLISTS_DIR", "configs/checklists"),
		PromptsDir:      env("PROMPTS_DIR", "configs/prompts"),
		DefaultList:     env("DEFAULT_CHECKLIST", "default"),
		DatabaseURL:     env("DATABASE_URL", ""),
		Concurrency:     envInt("CONCURRENCY", 2),
	}
	if cfg.ChunkOverlap >= cfg.ChunkSize {
		cfg.ChunkOverlap = cfg.ChunkSize / 5
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	return cfg
}

func (c Config) Validate() error {
	if c.TendersRoot == "" {
		return fmt.Errorf("TENDERS_ROOT is required")
	}
	if c.LMStudioBaseURL == "" {
		return fmt.Errorf("LM_STUDIO_BASE_URL is required")
	}
	return nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envFloat(key string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return n
}

func envDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
