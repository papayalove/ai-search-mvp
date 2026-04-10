package rewrite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChatClient 调用 OpenAI 兼容 Chat Completions（如阿里云 DashScope compatible-mode/v1）。
type ChatClient struct {
	HTTPClient *http.Client
	// BaseURL 例如 https://dashscope.aliyuncs.com/compatible-mode/v1（勿尾斜杠）
	BaseURL string
	APIKey  string
	Model   string
	// Temperature 0~2，默认 0.3
	Temperature float64
	// MaxTokens 模型回复上限，默认 512
	MaxTokens int
}

// Complete 发送可选 system 与 user 消息，返回助手回复正文（不做 JSON / 业务解析）。
func (c *ChatClient) Complete(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	userMessage = strings.TrimSpace(userMessage)
	if userMessage == "" {
		return "", fmt.Errorf("chat: empty user message")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return "", fmt.Errorf("chat: api key is empty")
	}
	base := strings.TrimSuffix(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("chat: base url is empty")
	}
	url := base + "/chat/completions"
	model := strings.TrimSpace(c.Model)
	if model == "" {
		model = "qwen-turbo"
	}
	temp := c.Temperature
	if temp <= 0 {
		temp = 0.3
	}
	maxTok := c.MaxTokens
	if maxTok <= 0 {
		maxTok = 512
	}

	msgs := []map[string]string{}
	if sp := strings.TrimSpace(systemPrompt); sp != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": sp})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": userMessage})

	body := map[string]any{
		"model":       model,
		"messages":    msgs,
		"temperature": temp,
		"max_tokens":  maxTok,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.APIKey))

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat http: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("chat http status %d: %s", resp.StatusCode, truncateStr(string(respBody), 600))
	}

	return parseChatCompletionContent(respBody)
}

func parseChatCompletionContent(b []byte) (string, error) {
	var wrap struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &wrap); err != nil {
		return "", fmt.Errorf("decode chat completion: %w", err)
	}
	if len(wrap.Choices) == 0 {
		return "", fmt.Errorf("empty choices in chat completion")
	}
	s := strings.TrimSpace(wrap.Choices[0].Message.Content)
	if s == "" {
		return "", fmt.Errorf("empty message content")
	}
	return s, nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
