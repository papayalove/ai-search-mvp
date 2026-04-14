package recall

import (
	"context"
	"log"
	"strings"
	"time"

	"ai-search-v1/internal/query"
	"ai-search-v1/internal/storage/es"
)

// RecallES 调用实体倒排 SearchByEntityKeys，转为 Hit。
func RecallES(ctx context.Context, repo *es.Repository, keys []string, topK int) ([]Hit, error) {
	if repo == nil {
		return nil, nil
	}
	if len(keys) == 0 {
		return nil, nil
	}
	log.Printf("search: request_id=%s phase=es_search_begin entity_keys=%d top_k=%d", requestIDFromCtx(ctx), len(keys), topK)
	hits, err := repo.SearchByEntityKeys(ctx, keys, topK)
	if err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		cid := strings.TrimSpace(h.ChunkID)
		if cid == "" {
			continue
		}
		ut := h.UpdatedTime.UnixMilli()
		if ut <= 0 {
			ut = time.Now().UnixMilli()
		}
		ct := h.CreatedTime.UnixMilli()
		if ct <= 0 {
			ct = ut
		}
		doc := strings.TrimSpace(h.DocID)
		out = append(out, Hit{
			ChunkID:      cid,
			DocID:        doc,
			SourceType:   strings.TrimSpace(h.SourceType),
			Lang:         strings.TrimSpace(h.Lang),
			Ts:           ut,
			CreatedTs:    ct,
			Score:        h.Score,
			URLOrDocID:   doc,
			Title:        cid,
			Offset:       h.Offset,
			PageNo:       int(h.PageNo),
			Source:       query.ContentFetchSource("", doc),
			RecallSource: "es",
		})
	}
	return out, nil
}
