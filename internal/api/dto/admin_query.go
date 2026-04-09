package dto

// AdminQueryRequest POST /v1/admin/query
type AdminQueryRequest struct {
	// SearchType: file_name | chunk_id | text；q 为空时忽略，直接拉取前 limit 条 chunk。
	SearchType string `json:"search_type"`
	Q          string `json:"q"`
	Limit      int    `json:"limit"`
}

// AdminQueryResponse 管理端查询结果。
type AdminQueryResponse struct {
	Records []AdminQueryRecord `json:"records"`
}

// AdminQueryRecord 与前端 MilvusRecord 对齐的扁平字段。
type AdminQueryRecord struct {
	ID         string            `json:"id"`
	ChunkID    string            `json:"chunk_id"`
	DocID      string            `json:"doc_id,omitempty"`
	FileName   string            `json:"file_name"`
	Collection string            `json:"collection"`
	SourceType string            `json:"source_type"`
	Lang       string            `json:"lang"`
	Score      float64           `json:"score,omitempty"`
	Ts         int64             `json:"ts"`
	Status     string            `json:"status"`
	VectorDim  int               `json:"vector_dim"`
	Metadata   map[string]string `json:"metadata"`
	CreatedAt  string            `json:"created_at"`
}
