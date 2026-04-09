package pipeline

import (
	"context"
	"fmt"
	"strings"

	"ai-search-v1/internal/model/embedding"
	"ai-search-v1/internal/storage/milvus"
)

// Searcher 统一承载公开检索与 Milvus chunk 直查（由 SearchInput.SearchType 区分，业务层设置）。
// StubSearcher：占位公开链路；MilvusSearcher：SearchType 非空时走 Admin 直查，否则走公开向量检索。
type Searcher interface {
	Search(ctx context.Context, in SearchInput) (*SearchOutput, error)
}

const (
	chunkLookupDefaultTopK = 20
	chunkLookupMaxTopK     = 100
	publicSearchDefaultTopK = 10
	publicSearchMaxTopK     = 100
)

// MilvusSearcher 实现 Search：SearchType 非空时走 Admin Milvus 直查，否则走公开向量检索。
type MilvusSearcher struct {
	Repo *milvus.Repository
	Emb  embedding.Embedder
}

// NewMilvusSearcher repo 必填；Emb 可为 nil（仅 text 直查会报错）。
func NewMilvusSearcher(repo *milvus.Repository, emb embedding.Embedder) *MilvusSearcher {
	return &MilvusSearcher{Repo: repo, Emb: emb}
}

func (s *MilvusSearcher) Search(ctx context.Context, in SearchInput) (*SearchOutput, error) {
	if s == nil || s.Repo == nil {
		return nil, fmt.Errorf("search: milvus repository is nil")
	}
	if in.SearchType != "" {
		return s.searchChunkLookup(ctx, in)
	}
	return s.searchPublicVector(ctx, in)
}

// searchPublicVector 对应 POST /v1/search：与 admin query 的 text 模式相同，对 query 做嵌入后在 Milvus 上做向量相似度检索。
func (s *MilvusSearcher) searchPublicVector(ctx context.Context, in SearchInput) (*SearchOutput, error) {
	topK := in.TopK
	if topK <= 0 {
		topK = publicSearchDefaultTopK
	}
	if topK > publicSearchMaxTopK {
		topK = publicSearchMaxTopK
	}
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return nil, fmt.Errorf("query is required")
	}
	if s.Emb == nil {
		return nil, fmt.Errorf("search requires embedding.enabled in config")
	}
	hits, err := s.queryByText(ctx, q, topK)
	if err != nil {
		return nil, err
	}
	out := s.chunkLookupOutput(hits)
	if in.IncludeDebug {
		out.Debug = &SearchDebug{
			Rewrites:     nil,
			RecallCounts: map[string]int{},
			MergedCount:  len(hits),
		}
	}
	return out, nil
}

func (s *MilvusSearcher) searchChunkLookup(ctx context.Context, in SearchInput) (*SearchOutput, error) {
	limit := in.TopK
	if limit <= 0 {
		limit = chunkLookupDefaultTopK
	}
	if limit > chunkLookupMaxTopK {
		limit = chunkLookupMaxTopK
	}
	qstr := strings.TrimSpace(in.Query)
	if qstr == "" {
		hits, err := s.listNonEmptyChunkIDs(ctx, int64(limit))
		if err != nil {
			return nil, err
		}
		return s.chunkLookupOutput(hits), nil
	}
	st := strings.ToLower(strings.TrimSpace(in.SearchType))
	if st == "" {
		st = "chunk_id"
	}
	var hits []SearchHit
	var err error
	switch st {
	case "file_name", "filename":
		hits, err = s.queryByChunkIDLike(ctx, qstr, int64(limit))
	case "chunk_id", "id":
		hits, err = s.queryByChunkIDField(ctx, qstr, int64(limit))
	case "text":
		hits, err = s.queryByText(ctx, qstr, limit)
	default:
		return nil, fmt.Errorf("chunk lookup: invalid mode %q (use file_name, chunk_id, text)", st)
	}
	if err != nil {
		return nil, err
	}
	return s.chunkLookupOutput(hits), nil
}

func (s *MilvusSearcher) chunkLookupOutput(hits []SearchHit) *SearchOutput {
	cfg := s.Repo.Config()
	return &SearchOutput{
		Hits: hits,
		ChunkRun: &SearchChunkRunMeta{
			Collection: strings.TrimSpace(cfg.Collection),
			VectorDim:  cfg.VectorDim,
		},
	}
}

func (s *MilvusSearcher) listNonEmptyChunkIDs(ctx context.Context, limit int64) ([]SearchHit, error) {
	recs, err := s.Repo.QueryByExpr(ctx, `chunk_id != ""`, limit, false)
	if err != nil {
		return nil, err
	}
	return recordsToSearchHits(recs, 0), nil
}

func (s *MilvusSearcher) queryByChunkIDLike(ctx context.Context, sub string, limit int64) ([]SearchHit, error) {
	pat := escapeMilvusLikePattern(sub)
	expr := fmt.Sprintf(`chunk_id like '%%%s%%'`, pat)
	recs, err := s.Repo.QueryByExpr(ctx, expr, limit, false)
	if err != nil {
		return nil, err
	}
	return recordsToSearchHits(recs, 0), nil
}

func (s *MilvusSearcher) queryByChunkIDField(ctx context.Context, id string, limit int64) ([]SearchHit, error) {
	out := []string{milvus.FieldChunkID, milvus.FieldDocID, milvus.FieldSourceType, milvus.FieldLang, milvus.FieldUpdatedTime}
	exact, err := s.Repo.QueryByChunkIDs(ctx, []string{id}, out)
	if err != nil {
		return nil, err
	}
	if len(exact) > 0 {
		return recordsToSearchHits(exact, 0), nil
	}
	return s.queryByChunkIDLike(ctx, id, limit)
}

func (s *MilvusSearcher) queryByText(ctx context.Context, text string, topK int) ([]SearchHit, error) {
	if s.Emb == nil {
		return nil, fmt.Errorf("chunk lookup text mode requires embedding.enabled in config")
	}
	vecs, err := s.Emb.Embed(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("chunk lookup embed: %w", err)
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("chunk lookup: expected 1 query vector, got %d", len(vecs))
	}
	mat, err := s.Repo.SearchVectors(ctx, milvus.VectorSearchParams{
		Vectors: [][]float32{vecs[0]},
		TopK:    topK,
		Expr:    "",
	})
	if err != nil {
		return nil, err
	}
	if len(mat) == 0 {
		return nil, nil
	}
	row := mat[0]
	hits := make([]SearchHit, 0, len(row))
	for i := range row {
		m := row[i]
		hits = append(hits, SearchHit{
			ChunkID:    m.ChunkID,
			DocID:      m.DocID,
			SourceType: m.SourceType,
			Lang:       m.Lang,
			Ts:         m.UpdatedTime,
			Score:      float64(m.Score),
			URLOrDocID: m.DocID,
			Title:      m.ChunkID,
		})
	}
	return hits, nil
}

func recordsToSearchHits(recs []milvus.ChunkRecord, score float64) []SearchHit {
	if len(recs) == 0 {
		return nil
	}
	out := make([]SearchHit, len(recs))
	for i := range recs {
		r := recs[i]
		out[i] = SearchHit{
			ChunkID:    r.ChunkID,
			DocID:      r.DocID,
			SourceType: r.SourceType,
			Lang:       r.Lang,
			Ts:         r.UpdatedTime,
			Score:      score,
			URLOrDocID: r.DocID,
			Title:      r.ChunkID,
		}
	}
	return out
}

func escapeMilvusLikePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
