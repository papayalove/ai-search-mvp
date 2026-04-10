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
// milvus / hybrid 时对子查询批量 Embed 一次，再并行 ANN，减少远程嵌入 RTT。
func RunParallelTextRetrieval(ctx context.Context, d Deps, mode Mode, queries []string, perQueryTopK int) (*Result, error) {
	queries = DedupeQueriesForParallel(queries, ParallelRecallMaxPaths)
	if len(queries) == 0 {
		return nil, fmt.Errorf("recall parallel: no queries")
	}
	if len(queries) == 1 {
		return RunTextRetrieval(ctx, d, mode, queries[0], perQueryTopK)
	}
	if perQueryTopK <= 0 {
		perQueryTopK = 10
	}

	if mode == ModeMilvus && d.Embedder != nil && d.Milvus != nil {
		return runParallelMilvusBatchedEmbed(ctx, d, queries, perQueryTopK)
	}
	if mode == ModeHybrid && d.Embedder != nil && d.Milvus != nil {
		return runParallelHybridBatchedEmbed(ctx, d, queries, perQueryTopK)
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
	return mergeParallelHitLists(lists, queries)
}

func runParallelMilvusBatchedEmbed(ctx context.Context, d Deps, queries []string, perQueryTopK int) (*Result, error) {
	log.Printf("search: request_id=%s phase=embed_begin n_texts=%d (parallel milvus)", requestIDFromCtx(ctx), len(queries))
	vecs, err := d.Embedder.Embed(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("recall parallel milvus: batch embed: %w", err)
	}
	if len(vecs) != len(queries) {
		return nil, fmt.Errorf("recall parallel milvus: got %d vectors for %d queries", len(vecs), len(queries))
	}
	g, gctx := errgroup.WithContext(ctx)
	lists := make([][]Hit, len(queries))
	for i := range queries {
		i, vec := i, vecs[i]
		g.Go(func() error {
			hits, err := RecallMilvusVector(gctx, d.Milvus, vec, perQueryTopK)
			if err != nil {
				return err
			}
			lists[i] = hits
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return mergeParallelHitLists(lists, queries)
}

func runParallelHybridBatchedEmbed(ctx context.Context, d Deps, queries []string, perQueryTopK int) (*Result, error) {
	log.Printf("search: request_id=%s phase=embed_begin n_texts=%d (parallel hybrid)", requestIDFromCtx(ctx), len(queries))
	vecs, err := d.Embedder.Embed(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("recall parallel hybrid: batch embed: %w", err)
	}
	if len(vecs) != len(queries) {
		return nil, fmt.Errorf("recall parallel hybrid: got %d vectors for %d queries", len(vecs), len(queries))
	}
	g, gctx := errgroup.WithContext(ctx)
	lists := make([][]Hit, len(queries))
	for i := range queries {
		i, q, vec := i, queries[i], vecs[i]
		g.Go(func() error {
			res, err := hybridRecallWithVector(gctx, d, q, perQueryTopK, vec)
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
	return mergeParallelHitLists(lists, queries)
}

func mergeParallelHitLists(lists [][]Hit, queries []string) (*Result, error) {
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

// DedupeQueriesForParallel 子查询去重（小写比较）、保序、最多 maxN 条。
func DedupeQueriesForParallel(qs []string, maxN int) []string {
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
