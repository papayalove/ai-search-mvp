package recall

// MergeDedupeMilvusFirst 以 Milvus 结果顺序为主，去重 chunk_id；仅 ES 出现的接在后面。
// 若某 chunk 在 Milvus 与 ES 均出现，保留 Milvus 分数与字段，RecallSource 标为 both。
func MergeDedupeMilvusFirst(milvusHits, esHits []Hit, topK int) []Hit {
	if topK <= 0 {
		topK = 10
	}
	idxByChunk := make(map[string]int)
	out := make([]Hit, 0, topK)

	for _, h := range milvusHits {
		if h.ChunkID == "" {
			continue
		}
		if _, dup := idxByChunk[h.ChunkID]; dup {
			continue
		}
		h2 := h
		if h2.RecallSource == "" {
			h2.RecallSource = "milvus"
		}
		idxByChunk[h2.ChunkID] = len(out)
		out = append(out, h2)
	}

	for _, h := range esHits {
		if h.ChunkID == "" {
			continue
		}
		if i, ok := idxByChunk[h.ChunkID]; ok {
			out[i].RecallSource = "both"
			continue
		}
		h2 := h
		if h2.RecallSource == "" {
			h2.RecallSource = "es"
		}
		idxByChunk[h2.ChunkID] = len(out)
		out = append(out, h2)
		if len(out) >= topK {
			return out[:topK]
		}
	}

	if len(out) > topK {
		return out[:topK]
	}
	return out
}
