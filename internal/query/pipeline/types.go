package pipeline

// SearchInput 由业务层（公开搜索 / 管理端等）组装后传入 Searcher。
// SearchType 为空：走公开检索（如 POST /v1/search 的向量 ANN）；非空：Admin Milvus 直查，取值 file_name | chunk_id | text。
// 直查时 Query 为空则返回 chunk_id 非空的前 TopK 条；TopK 同时表示 ANN 的 topK 与 Query 条数上限。
type SearchInput struct {
	Query        string
	TopK         int
	SourceTypes  []string
	Filters      map[string]any
	RequestID    string
	IncludeDebug bool

	SearchType string `json:"search_type,omitempty"`
}

// SearchHit is one ranked evidence chunk returned to callers.
type SearchHit struct {
	ChunkID     string
	DocID       string
	Snippet     string
	Score       float64
	SourceType  string
	Lang        string
	Ts          int64
	URLOrDocID  string
	PDFPage     *int
	Title       string
}

// SearchDebug carries optional diagnostics for the search path.
type SearchDebug struct {
	Rewrites     []string
	RecallCounts map[string]int
	MergedCount  int
}

// SearchChunkRunMeta 直查路径的集合元信息，供上层（如 admin JSON）序列化；公开搜索为 nil。
type SearchChunkRunMeta struct {
	Collection string
	VectorDim  int
}

// SearchOutput is the pipeline result before HTTP serialization.
type SearchOutput struct {
	Hits     []SearchHit
	Debug    *SearchDebug
	ChunkRun *SearchChunkRunMeta
}
