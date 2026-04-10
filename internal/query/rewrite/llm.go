package rewrite

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	modelrewrite "ai-search-v1/internal/model/rewrite"
)

// LLMRewriter 使用 OpenAI 兼容 Chat 完成「多路子查询」策略：提示词、解析与归一化。
type LLMRewriter struct {
	Client *modelrewrite.ChatClient
}

// NewLLMRewriter 基于已配置好的 ChatClient；c 为 nil 时返回 nil。
func NewLLMRewriter(c *modelrewrite.ChatClient) *LLMRewriter {
	if c == nil {
		return nil
	}
	return &LLMRewriter{Client: c}
}

const defaultSystemPrompt = `你是搜索查询改写助手。用户输入一条检索问题，你需要生成 5 条不同的子查询，用于并行检索，含义依次对应：
1) 实体显式化（把隐含实体补全为明确说法）
2) 同义改写（换种说法，保留意图）
3) 背景扩展（补充合理上下文或上位概念，仍保持可检索）
4) 约束强化（强调条件、范围、否定或关键限定）
5) 原 query 保真（与原文一致或仅做轻微规范化）

输出格式（必须严格遵守）：
- 恰好 5 行，每行一条子查询；不要空行、不要编号前缀、不要 JSON、不要 markdown、不要解释。
- 每行是一条可直接用于检索的完整问句，语言与用户问题一致（中文问题用中文）。

示例（仅演示格式，勿照抄内容）：
圆周率的定义是什么
π的数值是多少
圆周率在数学里怎么表示`

// Rewrite implements Rewriter：非流式 Chat。
func (w *LLMRewriter) Rewrite(ctx context.Context, userQuery string) ([]string, error) {
	return w.rewriteFromModel(ctx, userQuery, nil)
}

// RewriteStream implements StreamingRewriter：每输出完整一行子查询即回调 onQueryLine。
func (w *LLMRewriter) RewriteStream(ctx context.Context, userQuery string, onQueryLine func(string) error) ([]string, error) {
	return w.rewriteFromModel(ctx, userQuery, onQueryLine)
}

func (w *LLMRewriter) rewriteFromModel(ctx context.Context, userQuery string, onQueryLine func(string) error) ([]string, error) {
	if w == nil || w.Client == nil {
		return nil, fmt.Errorf("rewrite: llm rewriter not configured")
	}
	userQuery = strings.TrimSpace(userQuery)
	if userQuery == "" {
		return nil, fmt.Errorf("rewrite: empty query")
	}
	userMsg := "用户问题：" + userQuery
	var content string
	var err error
	if onQueryLine != nil {
		split := newLineQuerySplitter(onQueryLine, 5)
		content, err = w.Client.CompleteStream(ctx, defaultSystemPrompt, userMsg, split.Write)
		if err != nil {
			return nil, err
		}
		if err := split.flushTail(); err != nil {
			return nil, err
		}
	} else {
		content, err = w.Client.Complete(ctx, defaultSystemPrompt, userMsg)
		if err != nil {
			return nil, err
		}
	}
	queries, err := parseQueriesFromModel(content)
	if err != nil {
		return nil, fmt.Errorf("rewrite parse: %w", err)
	}
	out := normalizeQueries(queries, userQuery)
	log.Printf("search: request_id=%s phase=rewrite_llm_result n_subqueries=%d stream=%v", requestIDFromCtx(ctx), len(out), onQueryLine != nil)
	return out, nil
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
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
