package rewrite

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CompleteStream 与 Complete 相同请求体，但设置 stream:true，并按 SSE 增量解析助手正文。
// onDelta 在每个非空 content 片段时调用；返回值为累积全文（与 Complete 解析方式一致，供上层解析 JSON）。
func (c *ChatClient) CompleteStream(ctx context.Context, systemPrompt, userMessage string, onDelta func(string) error) (string, error) {
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
		"stream":      true,
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
	req.Header.Set("Accept", "text/event-stream")

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	} else if client.Timeout > 0 {
		// 流式读 body 可能长于单次响应超时，保留 Transport，取消整请求 deadline。
		sc := *client
		sc.Timeout = 0
		client = &sc
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", fmt.Errorf("chat http status %d: %s", resp.StatusCode, truncateStr(string(respBody), 600))
	}

	var acc strings.Builder
	sc := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil && strings.TrimSpace(chunk.Error.Message) != "" {
			return acc.String(), fmt.Errorf("chat stream: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		piece := chunk.Choices[0].Delta.Content
		if piece == "" {
			continue
		}
		acc.WriteString(piece)
		if onDelta != nil {
			if err := onDelta(piece); err != nil {
				return acc.String(), err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return acc.String(), fmt.Errorf("chat stream read: %w", err)
	}
	return acc.String(), nil
}
