package recall

import (
	"context"
	"fmt"
	"strings"

	"ai-search-v1/internal/model/embedding"
	"ai-search-v1/internal/storage/milvus"
)

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
	vecs, err := emb.Embed(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("recall milvus embed: %w", err)
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("recall milvus: expected 1 query vector, got %d", len(vecs))
	}
	mat, err := repo.SearchVectors(ctx, milvus.VectorSearchParams{
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
	out := make([]Hit, 0, len(row))
	for i := range row {
		m := row[i]
		out = append(out, Hit{
			ChunkID:      m.ChunkID,
			DocID:        m.DocID,
			SourceType:   m.SourceType,
			Lang:         m.Lang,
			Ts:           m.UpdatedTime,
			CreatedTs:    m.CreatedTime,
			Score:        float64(m.Score),
			URLOrDocID:   m.DocID,
			Title:        m.ChunkID,
			RecallSource: "milvus",
		})
	}
	return out, nil
}
