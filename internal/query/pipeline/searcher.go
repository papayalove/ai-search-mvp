package pipeline

import (
	"context"
	"fmt"
	"strings"

	"ai-search-v1/internal/model/embedding"
	"ai-search-v1/internal/query"
	"ai-search-v1/internal/query/recall"
	queryrewrite "ai-search-v1/internal/query/rewrite"
	"ai-search-v1/internal/storage/es"
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
	ES   *es.Repository // 可选；用于 hybrid / es 文本检索
	// Rewriter 非 nil 且启用时：POST /v1/search 先多路改写再并行召回（见 queryrewrite.RunTextRetrievalWithOptionalRewrite）。
	Rewriter queryrewrite.Rewriter
}

// NewMilvusSearcher repo 必填；Emb 可为 nil（仅 text 直查会报错）；es 可为 nil（无 ES 混合）。
func NewMilvusSearcher(repo *milvus.Repository, emb embedding.Embedder, es *es.Repository) *MilvusSearcher {
	return &MilvusSearcher{Repo: repo, Emb: emb, ES: es}
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

// searchPublicVector 对应 POST /v1/search：默认 hybrid（ES 实体 + Milvus 向量去重），可 retrieval=milvus|es。
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
	mode := recall.ParseMode(in.Retrieval)
	res, rewrites, err := queryrewrite.RunTextRetrievalWithOptionalRewrite(ctx, recall.Deps{
		ES:       s.ES,
		Milvus:   s.Repo,
		Embedder: s.Emb,
	}, mode, q, topK, s.Rewriter, in.OnRewriteQueryLine, in.RequestID)
	if err != nil {
		return nil, err
	}
	hits := recallHitsToSearchHits(res.Hits)
	out := s.chunkLookupOutput(hits)
	if in.IncludeDebug || in.OnRewriteQueryLine != nil {
		d := &SearchDebug{Rewrites: rewrites}
		if in.IncludeDebug {
			d.RecallCounts = res.RecallCounts
			d.MergedCount = res.MergedCount
		}
		out.Debug = d
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
		res, err2 := recall.RunTextRetrieval(ctx, recall.Deps{
			ES:       s.ES,
			Milvus:   s.Repo,
			Embedder: s.Emb,
		}, recall.ParseMode(in.Retrieval), qstr, limit)
		if err2 != nil {
			return nil, err2
		}
		hits = recallHitsToSearchHits(res.Hits)
		out := s.chunkLookupOutput(hits)
		if in.IncludeDebug {
			out.Debug = &SearchDebug{
				RecallCounts: res.RecallCounts,
				MergedCount:  res.MergedCount,
			}
		}
		return out, nil
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
	out := []string{milvus.FieldChunkID, milvus.FieldDocID, milvus.FieldTitle, milvus.FieldURL, milvus.FieldSourceType, milvus.FieldLang, milvus.FieldUpdatedTime, milvus.FieldOffset, milvus.FieldPageNo}
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
		title := m.Title
		if title == "" {
			title = m.ChunkID
		}
		url := m.URL
		if url == "" {
			url = m.DocID
		}
		hits = append(hits, SearchHit{
			ChunkID:    m.ChunkID,
			DocID:      m.DocID,
			SourceType: m.SourceType,
			Lang:       m.Lang,
			Ts:         m.UpdatedTime,
			CreatedTs:  m.CreatedTime,
			Score:      float64(m.Score),
			URLOrDocID: url,
			Title:      title,
			Offset:     m.Offset,
			PageNo:     int(m.PageNo),
			Source:     query.ContentFetchSource(m.URL, url),
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
		title := r.Title
		if title == "" {
			title = r.ChunkID
		}
		url := r.URL
		if url == "" {
			url = r.DocID
		}
		out[i] = SearchHit{
			ChunkID:    r.ChunkID,
			DocID:      r.DocID,
			SourceType: r.SourceType,
			Lang:       r.Lang,
			Ts:         r.UpdatedTime,
			CreatedTs:  r.CreatedTime,
			Score:      score,
			URLOrDocID: url,
			Title:      title,
			Offset:     r.Offset,
			PageNo:     int(r.PageNo),
			Source:     query.ContentFetchSource(r.URL, url),
		}
	}
	return out
}

func recallHitsToSearchHits(in []recall.Hit) []SearchHit {
	if len(in) == 0 {
		return nil
	}
	out := make([]SearchHit, len(in))
	for i := range in {
		h := in[i]
		out[i] = SearchHit{
			ChunkID:      h.ChunkID,
			DocID:        h.DocID,
			Snippet:      h.Snippet,
			Score:        h.Score,
			SourceType:   h.SourceType,
			Lang:         h.Lang,
			Ts:           h.Ts,
			CreatedTs:    h.CreatedTs,
			URLOrDocID:   h.URLOrDocID,
			PDFPage:      h.PDFPage,
			Title:        h.Title,
			Offset:       h.Offset,
			PageNo:       h.PageNo,
			Source:       h.Source,
			RecallSource: h.RecallSource,
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
