package rewrite

import "context"

// StreamingRewriter 在 LLM 流式输出时按「完整子查询行」回调（供 SSE rewrite_query）。
type StreamingRewriter interface {
	Rewriter
	RewriteStream(ctx context.Context, userQuery string, onQueryLine func(string) error) ([]string, error)
}
