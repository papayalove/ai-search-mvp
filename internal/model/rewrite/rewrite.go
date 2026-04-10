package rewrite

import "context"

// Rewriter 将用户 query 扩展为多条子查询（设计建议最多 5 路）。
type Rewriter interface {
	Rewrite(ctx context.Context, userQuery string) (queries []string, err error)
}
