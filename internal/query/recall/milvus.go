package recall

import (
	"context"
	"fmt"
	"log"
	"strings"

	"ai-search-v1/internal/model/embedding"
	"ai-search-v1/internal/query"
	"ai-search-v1/internal/storage/milvus"
)

// RecallMilvusVector 使用已算好的查询向量做 Milvus ANN（供批量嵌入后的多路并行召回）。
func RecallMilvusVector(ctx context.Context, repo *milvus.Repository, vec []float32, topK int) ([]Hit, error) {
	if repo == nil {
		return nil, fmt.Errorf("recall milvus: nil repository")
	}
	if len(vec) == 0 {
		return nil, fmt.Errorf("recall milvus: empty vector")
	}
	log.Printf("search: request_id=%s phase=milvus_search_begin top_k=%d dim=%d", requestIDFromCtx(ctx), topK, len(vec))
	mat, err := repo.SearchVectors(ctx, milvus.VectorSearchParams{
		Vectors: [][]float32{vec},
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
	out := make([]Hit, 0, len(row))
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
		out = append(out, Hit{
			ChunkID:      m.ChunkID,
			DocID:        m.DocID,
			SourceType:   m.SourceType,
			Lang:         m.Lang,
			Ts:           m.UpdatedTime,
			CreatedTs:    m.CreatedTime,
			Score:        float64(m.Score),
			URLOrDocID:   url,
			Title:        title,
			Offset:       m.Offset,
			PageNo:       int(m.PageNo),
			Source:       query.ContentFetchSource(m.URL, url),
			RecallSource: "milvus",
		})
	}
	return out, nil
}

// RecallMilvusText 嵌入 query 后 Milvus ANN，与 pipeline.queryByText 行为一致。
func RecallMilvusText(ctx context.Context, repo *milvus.Repository, emb embedding.Embedder, text string, topK int) ([]Hit, error) {
	if repo == nil {
		return nil, fmt.Errorf("recall milvus: nil repository")
	}
	if emb == nil {
		return nil, fmt.Errorf("recall milvus: embedder is nil")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	log.Printf("search: request_id=%s phase=embed_begin n_texts=1 (milvus path)", requestIDFromCtx(ctx))
	vecs, err := emb.Embed(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("recall milvus embed: %w", err)
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("recall milvus: expected 1 query vector, got %d", len(vecs))
	}
	return RecallMilvusVector(ctx, repo, vecs[0], topK)
}
