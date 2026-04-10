package recall

import (
	"context"
	"fmt"
	"log"
	"strings"

	"golang.org/x/sync/errgroup"
)

// 与设计 §5.1 对齐的粗粒度上限：每路子召回条数、合并后候选规模。
const (
	ParallelRecallMaxPaths   = 5
	ParallelRecallPerPathCap = 50
	ParallelMergedCap        = 250
)

// RunParallelTextRetrieval 对多条 query 并行执行 RunTextRetrieval，再按 chunk_id 去重合并。
// perQueryTopK 每条子查询传入 RunTextRetrieval 的 topK（建议 min(用户 topK, ParallelRecallPerPathCap)）。
func RunParallelTextRetrieval(ctx context.Context, d Deps, mode Mode, queries []string, perQueryTopK int) (*Result, error) {
	queries = dedupeQueriesForParallel(queries, ParallelRecallMaxPaths)
	if len(queries) == 0 {
		return nil, fmt.Errorf("recall parallel: no queries")
	}
	if len(queries) == 1 {
		return RunTextRetrieval(ctx, d, mode, queries[0], perQueryTopK)
	}
	if perQueryTopK <= 0 {
		perQueryTopK = 10
	}

	g, gctx := errgroup.WithContext(ctx)
	lists := make([][]Hit, len(queries))
	for i := range queries {
		i, q := i, queries[i]
		g.Go(func() error {
			res, err := RunTextRetrieval(gctx, d, mode, q, perQueryTopK)
			if err != nil {
				return err
			}
			lists[i] = res.Hits
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var nonEmpty [][]Hit
	for _, list := range lists {
		if len(list) == 0 {
			continue
		}
		nonEmpty = append(nonEmpty, list)
	}
	merged := MergeHitsDedupeSequential(nonEmpty, ParallelMergedCap)
	return &Result{
		Hits: merged,
		RecallCounts: map[string]int{
			"merged":      len(merged),
			"sub_queries": len(queries),
		},
		MergedCount: len(merged),
	}, nil
}

func dedupeQueriesForParallel(qs []string, maxN int) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, maxN)
	for _, q := range qs {
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
		if len(out) >= maxN {
			break
		}
	}
	return out
}

// RunTextRetrievalWithOptionalRewrite 先 Rewrite 再单路或多路召回；rewrite 失败时记录日志并退回原 query。
func RunTextRetrievalWithOptionalRewrite(
	ctx context.Context,
	d Deps,
	mode Mode,
	original string,
	finalTopK int,
	rw interface{ Rewrite(context.Context, string) ([]string, error) },
) (res *Result, rewriteQueries []string, err error) {
	original = strings.TrimSpace(original)
	if original == "" {
		return nil, nil, fmt.Errorf("recall: empty query")
	}
	queries := []string{original}
	if rw != nil {
		rs, rerr := rw.Rewrite(ctx, original)
		if rerr != nil {
			log.Printf("rewrite failed (fallback single query): %v", rerr)
		} else if len(rs) > 0 {
			queries = dedupeQueriesForParallel(rs, ParallelRecallMaxPaths)
			if len(queries) == 0 {
				queries = []string{original}
			}
		}
	}
	rewriteQueries = queries

	perK := finalTopK
	if perK > ParallelRecallPerPathCap {
		perK = ParallelRecallPerPathCap
	}
	if perK <= 0 {
		perK = 10
	}

	if len(queries) == 1 {
		res, err = RunTextRetrieval(ctx, d, mode, queries[0], finalTopK)
	} else {
		res, err = RunParallelTextRetrieval(ctx, d, mode, queries, perK)
	}
	if err != nil {
		return nil, rewriteQueries, err
	}
	if len(res.Hits) > finalTopK && finalTopK > 0 {
		res.Hits = res.Hits[:finalTopK]
		res.MergedCount = len(res.Hits)
	}
	return res, rewriteQueries, nil
}
