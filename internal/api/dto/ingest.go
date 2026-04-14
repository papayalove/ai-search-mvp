package dto

// IngestFileResult 同步路径下单文件结果（保留类型名供兼容）。
type IngestFileResult struct {
	Name          string `json:"name"`
	OK            bool   `json:"ok"`
	InputLines    int    `json:"input_lines,omitempty"`
	ChunksWritten int    `json:"chunks_written,omitempty"`
	Error         string `json:"error,omitempty"`
}

// IngestResponse 旧版同步响应（已不再用于默认 admin ingest）。
type IngestResponse struct {
	Files []IngestFileResult `json:"files"`
}

// IngestAcceptedFile 异步入队后单文件说明。
type IngestAcceptedFile struct {
	Name       string `json:"name"`
	PayloadKey string `json:"payload_key,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
}

// IngestAcceptedResponse POST /v1/admin/ingest 异步入队 202。
type IngestAcceptedResponse struct {
	JobID  string               `json:"job_id"`
	Status string               `json:"status"`
	Files  []IngestAcceptedFile `json:"files"`
}

// IngestRemoteRequest POST /v1/admin/ingest/remote JSON。
type IngestRemoteRequest struct {
	JobName     string   `json:"job_name"`
	S3URIs      []string `json:"s3_uris"`
	Bucket      string   `json:"bucket"`
	Keys        []string `json:"keys"`
	Prefix      string   `json:"prefix"`
	SourceURL   string   `json:"source_url"`
	Partition  string `json:"partition"`
	// Upsert 省略或为 null 时由 API 默认为 true（Milvus Upsert）；显式 false 则 Insert。
	Upsert *bool `json:"upsert,omitempty"`
	SourceType string `json:"source_type"`
	Lang       string `json:"lang"`
	DocID      string `json:"doc_id"`
	Title      string `json:"title,omitempty"`
	URL        string `json:"url,omitempty"`
	PageNo     int    `json:"page_no"`
	TaskID     string `json:"task_id"`
}

// IngestRemoteResponse 202。
type IngestRemoteResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}
