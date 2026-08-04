package lmstudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client — OpenAI-compatible клиент к LM Studio (/v1/chat/completions).
type Client struct {
	baseURL     string
	apiKey      string
	model       string
	httpClient  *http.Client
	temperature float64
	maxTokens   int

	pingMu   sync.Mutex
	pingAt   time.Time
	pingErr  error
	pingTTL  time.Duration
}

type Options struct {
	BaseURL     string
	APIKey      string
	Model       string
	Timeout     time.Duration
	Temperature float64
	MaxTokens   int
}

func New(opt Options) *Client {
	if opt.Timeout <= 0 {
		opt.Timeout = 5 * time.Minute
	}
	if opt.APIKey == "" {
		opt.APIKey = "lm-studio"
	}
	return &Client{
		baseURL:     strings.TrimRight(opt.BaseURL, "/"),
		apiKey:      opt.APIKey,
		model:       opt.Model,
		temperature: opt.Temperature,
		maxTokens:   opt.MaxTokens,
		pingTTL:     60 * time.Second,
		httpClient: &http.Client{
			Timeout: opt.Timeout,
		},
	}
}

func (c *Client) Model() string   { return c.model }
func (c *Client) BaseURL() string { return c.baseURL }

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Chat отправляет сообщения в LM Studio и возвращает текст ответа.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, string, error) {
	return c.ChatMaxTokens(ctx, messages, c.maxTokens)
}

// ChatMaxTokens — Chat с переопределением max_tokens (короткие ответы на порции).
func (c *Client) ChatMaxTokens(ctx context.Context, messages []Message, maxTokens int) (string, string, error) {
	if maxTokens <= 0 {
		maxTokens = c.maxTokens
	}
	reqBody := chatRequest{
		Model:       c.model,
		Messages:    messages,
		Temperature: c.temperature,
		MaxTokens:   maxTokens,
		Stream:      false,
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", err
	}

	sysLen, userLen := 0, 0
	for _, m := range messages {
		n := len([]rune(m.Content))
		switch m.Role {
		case "system":
			sysLen += n
		default:
			userLen += n
		}
	}
	log.Printf("lmstudio → POST %s/chat/completions model=%s max_tokens=%d sys_runes=%d user_runes=%d body_bytes=%d",
		c.baseURL, c.model, maxTokens, sysLen, userLen, len(raw))

	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	started := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("lm studio request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", "", err
	}
	log.Printf("lmstudio ← HTTP %d in %s bytes=%d", resp.StatusCode, time.Since(started).Round(time.Millisecond), len(body))
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("lm studio HTTP %d: %s", resp.StatusCode, truncate(string(body), 500))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", "", fmt.Errorf("decode lm studio response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", "", fmt.Errorf("lm studio error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", "", fmt.Errorf("lm studio: empty choices")
	}
	model := parsed.Model
	if model == "" {
		model = c.model
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), model, nil
}

// IsContextExceeded — ответ LM Studio про переполнение контекста.
func IsContextExceeded(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "context size") ||
		strings.Contains(low, "context length") ||
		strings.Contains(low, "context_length") ||
		(strings.Contains(low, "exceeded") && strings.Contains(low, "context"))
}

// Ping проверяет /models. Результат кэшируется, чтобы Docker healthcheck
// не долбил LM Studio каждые 10с (в логах это выглядело как «странные» GET).
func (c *Client) Ping(ctx context.Context) error {
	c.pingMu.Lock()
	defer c.pingMu.Unlock()
	if c.pingTTL > 0 && time.Since(c.pingAt) < c.pingTTL {
		return c.pingErr
	}
	err := c.pingNow(ctx)
	c.pingAt = time.Now()
	c.pingErr = err
	return err
}

func (c *Client) pingNow(ctx context.Context) error {
	url := c.baseURL + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("lm studio /models HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
