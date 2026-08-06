package lmstudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
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

	pingMu  sync.Mutex
	pingAt  time.Time
	pingErr error
	pingTTL time.Duration
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
		pingTTL:     10 * time.Second,
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
	Model              string         `json:"model"`
	Messages           []Message      `json:"messages"`
	Temperature        float64        `json:"temperature"`
	MaxTokens          int            `json:"max_tokens,omitempty"`
	Stream             bool           `json:"stream"`
	ChatTemplateKwargs map[string]any `json:"chat_template_kwargs,omitempty"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
	Usage *struct {
		CompletionTokens int `json:"completion_tokens"`
		CompletionTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage,omitempty"`
}

var thinkBlockRe = regexp.MustCompile(`(?is)<think>.*?</think>`)

// Chat отправляет сообщения в LM Studio и возвращает текст ответа.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, string, error) {
	return c.ChatMaxTokens(ctx, messages, c.maxTokens)
}

// ChatMaxTokens — Chat с переопределением max_tokens.
// Для Qwen3: пытаемся выключить thinking и читаем reasoning_content, если content пуст.
func (c *Client) ChatMaxTokens(ctx context.Context, messages []Message, maxTokens int) (string, string, error) {
	if maxTokens <= 0 {
		maxTokens = c.maxTokens
	}
	msgs := withNoThinkPrefill(messages)
	reqBody := chatRequest{
		Model:       c.model,
		Messages:    msgs,
		Temperature: c.temperature,
		MaxTokens:   maxTokens,
		Stream:      false,
		// LM Studio / Qwen: выключить thinking, иначе весь max_tokens уходит в reasoning.
		ChatTemplateKwargs: map[string]any{
			"enable_thinking": false,
		},
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
		case "user":
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
	msg := parsed.Choices[0].Message
	content := ExtractAssistantText(msg.Content, msg.ReasoningContent)
	if content == "" {
		reasonTok := 0
		if parsed.Usage != nil && parsed.Usage.CompletionTokensDetails != nil {
			reasonTok = parsed.Usage.CompletionTokensDetails.ReasoningTokens
		}
		log.Printf("lmstudio warn: empty assistant text (finish=%s reasoning_tokens=%d content_len=%d reasoning_len=%d)",
			parsed.Choices[0].FinishReason, reasonTok, len(msg.Content), len(msg.ReasoningContent))
		return "", "", fmt.Errorf("lm studio: empty content (Qwen thinking съел max_tokens=%d; увеличьте DOSE_MAX_TOKENS или отключите thinking в LM Studio)", maxTokens)
	}
	model := parsed.Model
	if model == "" {
		model = c.model
	}
	return content, model, nil
}

// ExtractAssistantText достаёт полезный ответ из content / reasoning_content / think-блоков.
func ExtractAssistantText(content, reasoning string) string {
	content = strings.TrimSpace(content)
	reasoning = strings.TrimSpace(reasoning)
	if content != "" {
		stripped := stripThink(content)
		if stripped != "" {
			return stripped
		}
	}
	if reasoning != "" {
		stripped := stripThink(reasoning)
		if stripped != "" {
			return stripped
		}
		return reasoning
	}
	return ""
}

func stripThink(s string) string {
	s = thinkBlockRe.ReplaceAllString(s, "")
	// незакрытый think в начале
	if i := strings.Index(strings.ToLower(s), "</think>"); i >= 0 {
		s = s[i+len("</think>"):]
	}
	return strings.TrimSpace(s)
}

// withNoThinkPrefill — workaround для Qwen3 в LM Studio: закрытый think в assistant
// часто отключает фазу рассуждений, иначе content пустой.
func withNoThinkPrefill(messages []Message) []Message {
	out := make([]Message, 0, len(messages)+2)
	out = append(out, messages...)
	// Маркер /no_think в конце user (Qwen3).
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role == "user" {
			if !strings.Contains(out[i].Content, "/no_think") {
				out[i].Content = strings.TrimSpace(out[i].Content) + "\n\n/no_think"
			}
			break
		}
	}
	out = append(out, Message{
		Role:    "assistant",
		Content: "<think>\n\n</think>\n\n",
	})
	return out
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

// Ping проверяет /models. Результат кэшируется.
func (c *Client) Ping(ctx context.Context) error {
	return c.ping(ctx, false)
}

// PingFresh всегда ходит в сеть (для health-пула).
func (c *Client) PingFresh(ctx context.Context) error {
	return c.ping(ctx, true)
}

func (c *Client) ping(ctx context.Context, fresh bool) error {
	c.pingMu.Lock()
	defer c.pingMu.Unlock()
	if !fresh && c.pingTTL > 0 && time.Since(c.pingAt) < c.pingTTL {
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
