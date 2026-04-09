package queue

import (
	"encoding/json"
	"strconv"
)

// PayloadKind 与表单 source_type（web/pdf）区分。
const (
	PayloadKindMultipartRedis = "multipart_redis"
	PayloadKindS3             = "s3"
)

// FileRef multipart 时 Redis 中正文 key 与原始文件名。
type FileRef struct {
	PayloadKey string `json:"payload_key"`
	Filename   string `json:"filename"`
}

// Job 入队 JSON（Redis List 一条元素）。
type Job struct {
	JobID       string    `json:"job_id"`
	PayloadKind string    `json:"payload_kind"`
	Partition   string    `json:"partition,omitempty"`
	Upsert      bool      `json:"upsert"`
	ChunkExpand bool      `json:"chunk_expand"`
	SourceType  string    `json:"source_type,omitempty"`
	Lang        string    `json:"lang,omitempty"`
	DocID       string    `json:"doc_id,omitempty"`
	PageNo      int       `json:"page_no"`
	ChunkID     string    `json:"chunk_id,omitempty"`
	TaskID      string    `json:"task_id,omitempty"`
	Files       []FileRef `json:"files,omitempty"`
	S3URIs      []string  `json:"s3_uris,omitempty"`
	Bucket      string    `json:"bucket,omitempty"`
	Keys        []string  `json:"keys,omitempty"`
	Prefix      string    `json:"prefix,omitempty"`
}

// Marshal 序列化为 JSON 一行。
func (j Job) Marshal() ([]byte, error) {
	return json.Marshal(j)
}

// UnmarshalJob 反序列化。
func UnmarshalJob(b []byte) (Job, error) {
	var j Job
	err := json.Unmarshal(b, &j)
	return j, err
}

// PayloadRedisKey 生成 multipart 正文 key。
func PayloadRedisKey(jobID string, index int) string {
	return "ingest:p:" + jobID + ":" + strconv.Itoa(index)
}

// JobMetaRedisKey job 元数据（状态）key。
func JobMetaRedisKey(jobID string) string {
	return "ingest:j:" + jobID
}
