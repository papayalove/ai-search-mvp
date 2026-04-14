package dto

// SearchRequest is the JSON body for POST /v1/search.
type SearchRequest struct {
	Query        string         `json:"query"`
	TopK         int            `json:"top_k"`
	SourceTypes  []string       `json:"source_types,omitempty"`
	Filters      map[string]any `json:"filters,omitempty"`
	RequestID    string         `json:"request_id,omitempty"`
	// Retrieval hybrid（默认）| milvus | es
	Retrieval string `json:"retrieval,omitempty"`
	// Stream true 时响应为 text/event-stream：rewrite_query（每行一条）→ rewrite_queries → done。
	Stream bool `json:"stream,omitempty"`
}

// SearchResponse is the JSON envelope returned by POST /v1/search.
type SearchResponse struct {
	Hits []SearchHit    `json:"hits"`
	Debug *SearchDebug  `json:"debug,omitempty"`
}

// SearchHit is one item in the hits array.
type SearchHit struct {
	ChunkID     string  `json:"chunk_id"`
	DocID       string  `json:"doc_id,omitempty"`
	Snippet     string  `json:"snippet"`
	Score       float64 `json:"score"`
	SourceType  string  `json:"source_type"`
	URLOrDocID  string  `json:"url_or_doc_id"`
	PDFPage     *int    `json:"pdf_page,omitempty"`
	Title       string  `json:"title"`
	Offset      int64   `json:"offset"`
	PageNo      int     `json:"page_no"`
	// Source 为可用于 GET /v1/content 的 http(s) 或 s3://（与入库 url 对齐）；无可用源时省略。
	Source string `json:"source,omitempty"`
}

// SearchDebug is optional diagnostic payload.
type SearchDebug struct {
	Rewrites     []string       `json:"rewrites,omitempty"`
	RecallCounts map[string]int `json:"recall_counts,omitempty"`
	MergedCount  int            `json:"merged_count,omitempty"`
}
