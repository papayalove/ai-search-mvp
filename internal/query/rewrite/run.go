package rewrite

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"ai-search-v1/internal/query/recall"
)

// RunTextRetrievalWithOptionalRewrite 先 LLM 改写再单路或多路召回；失败时记录日志并退回原 query。
// onRewriteQueryLine 非 nil 且 rw 实现 StreamingRewriter 时，每生成完整一行子查询回调一次（供 SSE）。
// requestID 用于日志关联（可为空）。
func RunTextRetrievalWithOptionalRewrite(
	ctx context.Context,
	d recall.Deps,
	mode recall.Mode,
	original string,
	finalTopK int,
	rw Rewriter,
	onRewriteQueryLine func(string) error,
	requestID string,
) (res *recall.Result, rewriteQueries []string, err error) {
	rid := strings.TrimSpace(requestID)
	if rid == "" {
		rid = "-"
	}
	original = strings.TrimSpace(original)
	if original == "" {
		return nil, nil, fmt.Errorf("rewrite: empty query")
	}
	queries := []string{original}
	tRewrite := time.Now()
	var rerr error
	if rw != nil {
		var rs []string
		streaming := false
		if onRewriteQueryLine != nil {
			if sw, ok := rw.(StreamingRewriter); ok {
				streaming = true
				rs, rerr = sw.RewriteStream(ctx, original, onRewriteQueryLine)
			} else {
				rs, rerr = rw.Rewrite(ctx, original)
			}
		} else {
			rs, rerr = rw.Rewrite(ctx, original)
		}
		if rerr != nil {
			log.Printf("search: request_id=%s rewrite LLM error (fallback single query): %v", rid, rerr)
		} else if len(rs) > 0 {
			queries = recall.DedupeQueriesForParallel(rs, recall.ParallelRecallMaxPaths)
			if len(queries) == 0 {
				queries = []string{original}
			}
		}
		log.Printf("search: request_id=%s phase=rewrite_done_for_recall dur=%v streaming=%v sub_queries=%d queries=%s",
			rid, time.Since(tRewrite), streaming, len(queries), queriesLogSummary(queries))
	} else {
		log.Printf("search: request_id=%s phase=rewrite_skipped reason=nil_rewriter (single query recall)", rid)
	}
	rewriteQueries = queries

	perK := finalTopK
	if perK > recall.ParallelRecallPerPathCap {
		perK = recall.ParallelRecallPerPathCap
	}
	if perK <= 0 {
		perK = 10
	}

	tRecall := time.Now()
	log.Printf("search: request_id=%s phase=recall_begin mode=%s parallel=%v sub_queries=%d top_k=%d",
		rid, mode, len(queries) > 1, len(queries), finalTopK)
	if len(queries) == 1 {
		res, err = recall.RunTextRetrieval(ctx, d, mode, queries[0], finalTopK)
	} else {
		res, err = recall.RunParallelTextRetrieval(ctx, d, mode, queries, perK)
	}
	recallDur := time.Since(tRecall)
	if err != nil {
		log.Printf("search: request_id=%s recall FAIL mode=%s dur=%v sub_queries=%d err=%v",
			rid, mode, recallDur, len(queries), err)
		return nil, rewriteQueries, err
	}
	nHit := 0
	rc := map[string]int{}
	if res != nil {
		nHit = len(res.Hits)
		rc = res.RecallCounts
	}
	if nHit == 0 {
		log.Printf("search: request_id=%s recall OK but 0 hits mode=%s dur=%v sub_queries=%d top_k=%d counts=%v (check collection 是否有数据、hybrid 是否抽到实体键、embedding 是否正常)",
			rid, mode, recallDur, len(queries), finalTopK, rc)
	} else {
		log.Printf("search: request_id=%s recall OK mode=%s dur=%v sub_queries=%d hits=%d counts=%v",
			rid, mode, recallDur, len(queries), nHit, rc)
	}
	if len(res.Hits) > finalTopK && finalTopK > 0 {
		res.Hits = res.Hits[:finalTopK]
		res.MergedCount = len(res.Hits)
	}
	return res, rewriteQueries, nil
}

func queriesLogSummary(qs []string) string {
	if len(qs) == 0 {
		return "[]"
	}
	const maxEach = 72
	parts := make([]string, 0, len(qs))
	for _, q := range qs {
		q = strings.TrimSpace(q)
		if len(q) > maxEach {
			q = q[:maxEach] + "…"
		}
		parts = append(parts, strconv.Quote(q))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
