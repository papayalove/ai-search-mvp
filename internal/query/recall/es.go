package recall

import (
	"context"
	"strings"
	"time"

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
		out = append(out, Hit{
			ChunkID:      cid,
			DocID:        strings.TrimSpace(h.DocID),
			SourceType:   strings.TrimSpace(h.SourceType),
			Lang:         strings.TrimSpace(h.Lang),
			Ts:           ut,
			CreatedTs:    ct,
			Score:        h.Score,
			URLOrDocID:   strings.TrimSpace(h.DocID),
			Title:        cid,
			RecallSource: "es",
		})
	}
	return out, nil
}
