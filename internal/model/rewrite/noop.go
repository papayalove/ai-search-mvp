package rewrite

import (
	"context"
	"strings"
)

// Noop 不重写，仅返回去空白后的原 query（单路召回）。
type Noop struct{}

// Rewrite implements Rewriter.
func (Noop) Rewrite(_ context.Context, userQuery string) ([]string, error) {
	q := strings.TrimSpace(userQuery)
	if q == "" {
		return nil, nil
	}
	return []string{q}, nil
}
