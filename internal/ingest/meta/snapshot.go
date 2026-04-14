package ingestmeta

import (
	"encoding/json"

	"ai-search-v1/internal/queue"
)

// PipelineSnapshotJSON 入队参数快照，写入 ingest_job.pipeline_params。
func PipelineSnapshotJSON(j queue.Job) ([]byte, error) {
	m := map[string]any{
		"partition":   j.Partition,
		"upsert":      j.Upsert,
		"source_type": j.SourceType,
		"lang":          j.Lang,
		"doc_id":        j.DocID,
		"page_no":       j.PageNo,
		"chunk_id":      j.ChunkID,
		"payload_kind":  j.PayloadKind,
		"bucket":        j.Bucket,
		"prefix":        j.Prefix,
		"s3_defer_task_es": j.S3DeferTaskES,
	}
	return json.Marshal(m)
}
