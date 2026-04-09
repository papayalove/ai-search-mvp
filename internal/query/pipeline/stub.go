package pipeline

import (
	"context"
	"fmt"
)

// StubSearcher is a no-op pipeline used until ES/Milvus/rerank are integrated.
type StubSearcher struct{}

func (StubSearcher) Search(_ context.Context, in SearchInput) (*SearchOutput, error) {
	if in.SearchType != "" {
		return nil, fmt.Errorf("chunk lookup requires Milvus (enable milvus.enabled and use MilvusSearcher)")
	}
	out := &SearchOutput{Hits: make([]SearchHit, 0)}
	if in.IncludeDebug {
		out.Debug = &SearchDebug{
			Rewrites:     nil,
			RecallCounts: map[string]int{},
			MergedCount:  0,
		}
	}
	return out, nil
}
