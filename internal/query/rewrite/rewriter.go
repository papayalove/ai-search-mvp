package rewrite

import "context"

// Rewriter 将用户 query 扩展为多条子查询（与 recall 并行上限等策略配合，通常最多 5 路）。
type Rewriter interface {
	Rewrite(ctx context.Context, userQuery string) (queries []string, err error)
}
