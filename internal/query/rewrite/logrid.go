package rewrite

import (
	"context"
	"strings"

	"ai-search-v1/internal/api/middleware"
)

func requestIDFromCtx(ctx context.Context) string {
	if ctx == nil {
		return "-"
	}
	s := middleware.RequestIDFromGoContext(ctx)
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
