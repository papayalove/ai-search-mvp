package recall

import (
	"context"
	"fmt"
	"log"
	"strings"

	"ai-search-v1/internal/model/embedding"
	"ai-search-v1/internal/storage/es"
	"ai-search-v1/internal/storage/milvus"
)

// Mode 文本检索后端。
type Mode string

const (
	ModeHybrid Mode = "hybrid"
	ModeMilvus Mode = "milvus"
	ModeES     Mode = "es"
)

// ParseMode 解析请求字符串，非法时回退 hybrid。
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "milvus", "vector":
		return ModeMilvus
	case "es", "entity":
		return ModeES
	case "hybrid", "":
		return ModeHybrid
	default:
		return ModeHybrid
	}
}

// Deps 召回依赖。
type Deps struct {
	ES       *es.Repository
	Milvus   *milvus.Repository
	Embedder embedding.Embedder
}

// Result 文本检索结果。
type Result struct {
	Hits         []Hit
	RecallCounts map[string]int
	MergedCount  int
}

// RunTextRetrieval 统一入口：es | milvus | hybrid。
func RunTextRetrieval(ctx context.Context, d Deps, mode Mode, query string, topK int) (*Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("recall: empty query")
	}
	if topK <= 0 {
		topK = 10
	}

	switch mode {
	case ModeES:
		if d.ES == nil {
			return nil, fmt.Errorf("recall: elasticsearch is disabled (mode=es)")
		}
		keys := EntityKeysFromQuery(query)
		esHits, err := RecallES(ctx, d.ES, keys, topK)
		if err != nil {
			return nil, err
		}
		return &Result{
			Hits:         esHits,
			RecallCounts: map[string]int{"es": len(esHits), "milvus": 0, "merged": len(esHits)},
			MergedCount:  len(esHits),
		}, nil

	case ModeMilvus:
		if d.Milvus == nil {
			return nil, fmt.Errorf("recall: milvus is nil")
		}
		if d.Embedder == nil {
			return nil, fmt.Errorf("recall: embedder is nil (mode=milvus)")
		}
		mv, err := RecallMilvusText(ctx, d.Milvus, d.Embedder, query, topK)
		if err != nil {
			return nil, err
		}
		return &Result{
			Hits:         mv,
			RecallCounts: map[string]int{"milvus": len(mv), "es": 0, "merged": len(mv)},
			MergedCount:  len(mv),
		}, nil

	case ModeHybrid:
		if d.Milvus == nil || d.Embedder == nil {
			return nil, fmt.Errorf("recall: milvus or embedder is nil (mode=hybrid)")
		}
		keys := EntityKeysFromQuery(query)
		mvHits, err := RecallMilvusText(ctx, d.Milvus, d.Embedder, query, topK)
		if err != nil {
			return nil, err
		}
		var esHits []Hit
		if d.ES != nil && len(keys) > 0 {
			var esErr error
			esHits, esErr = RecallES(ctx, d.ES, keys, topK)
			if esErr != nil {
				log.Printf("recall hybrid: es recall error (continuing with milvus): %v", esErr)
				esHits = nil
			}
		} else if d.ES == nil && len(keys) > 0 {
			log.Printf("recall hybrid: elasticsearch disabled, using milvus only")
		}
		merged := MergeDedupeMilvusFirst(mvHits, esHits, topK)
		return &Result{
			Hits: merged,
			RecallCounts: map[string]int{
				"milvus": len(mvHits),
				"es":     len(esHits),
				"merged": len(merged),
			},
			MergedCount: len(merged),
		}, nil

	default:
		return RunTextRetrieval(ctx, d, ModeHybrid, query, topK)
	}
}
