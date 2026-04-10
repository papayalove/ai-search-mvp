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

// ChatClient 调用 OpenAI 兼容 Chat Completions（阿里云百炼 / DashScope：compatible-mode/v1）。
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

const defaultSystemPrompt = `你是搜索查询改写助手。用户输入一条检索问题，你需要生成 5 条不同的子查询，用于并行检索，含义依次对应：
1) 实体显式化（把隐含实体补全为明确说法）
2) 同义改写（换种说法，保留意图）
3) 背景扩展（补充合理上下文或上位概念，仍保持可检索）
4) 约束强化（强调条件、范围、否定或关键限定）
5) 原 query 保真（与原文一致或仅做轻微规范化）

只输出一行 JSON 对象，不要 markdown、不要解释，格式严格为：
{"queries":["子查询1","子查询2","子查询3","子查询4","子查询5"]}
每条为非空字符串，使用与用户问题相同的语言（中文问题用中文子查询）。`

// Rewrite 调用大模型，解析 JSON，返回至多 5 条去重后的子查询。
func (c *ChatClient) Rewrite(ctx context.Context, userQuery string) ([]string, error) {
	userQuery = strings.TrimSpace(userQuery)
	if userQuery == "" {
		return nil, fmt.Errorf("rewrite: empty query")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, fmt.Errorf("rewrite: api key is empty")
	}
	base := strings.TrimSuffix(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("rewrite: base url is empty")
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

	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": defaultSystemPrompt},
			{"role": "user", "content": "用户问题：" + userQuery},
		},
		"temperature": temp,
		"max_tokens":  maxTok,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.APIKey))

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rewrite http: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rewrite http status %d: %s", resp.StatusCode, truncateStr(string(respBody), 600))
	}

	content, err := parseChatCompletionContent(respBody)
	if err != nil {
		return nil, err
	}
	queries, err := parseQueriesJSON(stripCodeFence(content))
	if err != nil {
		return nil, fmt.Errorf("rewrite parse: %w", err)
	}
	return normalizeQueries(queries, userQuery), nil
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

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		rest := strings.TrimSpace(s[i+1:])
		s = rest
	} else {
		s = strings.TrimSpace(s)
	}
	if j := strings.LastIndex(s, "```"); j >= 0 {
		s = strings.TrimSpace(s[:j])
	}
	return strings.TrimSpace(s)
}

func parseQueriesJSON(s string) ([]string, error) {
	var obj struct {
		Queries []string `json:"queries"`
	}
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return nil, err
	}
	if len(obj.Queries) == 0 {
		return nil, fmt.Errorf("no queries in json")
	}
	return obj.Queries, nil
}

func normalizeQueries(in []string, fallback string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, 5)
	for _, q := range in {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		k := strings.ToLower(q)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, q)
		if len(out) >= 5 {
			break
		}
	}
	if len(out) == 0 {
		return []string{strings.TrimSpace(fallback)}
	}
	return out
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
