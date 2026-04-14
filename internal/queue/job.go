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
	TaskID     string `json:"task_id,omitempty"`
}

// Job 入队 JSON（Redis List 一条元素）。
type Job struct {
	JobID       string    `json:"job_id"`
	JobName     string    `json:"job_name,omitempty"`
	PayloadKind string    `json:"payload_kind"`
	Partition  string `json:"partition,omitempty"`
	Upsert     bool   `json:"upsert"`
	SourceType string `json:"source_type,omitempty"`
	Lang        string    `json:"lang,omitempty"`
	DocID       string    `json:"doc_id,omitempty"`
	Title       string    `json:"title,omitempty"`
	URL         string    `json:"url,omitempty"`
	PageNo      int       `json:"page_no"`
	ChunkID     string    `json:"chunk_id,omitempty"`
	TaskID      string    `json:"task_id,omitempty"`
	Files       []FileRef `json:"files,omitempty"`
	S3URIs      []string  `json:"s3_uris,omitempty"`
	Bucket      string    `json:"bucket,omitempty"`
	Keys        []string  `json:"keys,omitempty"`
	Prefix      string    `json:"prefix,omitempty"`
	// S3DeferTaskES 为 true 时仅 bucket+prefix 入队，Worker 在 list 后再写 ES Task 占位并更新 total_files。
	S3DeferTaskES bool `json:"s3_defer_task_es,omitempty"`
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
