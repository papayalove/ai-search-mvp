package ingestmeta

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"ai-search-v1/internal/queue"
	"ai-search-v1/internal/storage/es"
	"ai-search-v1/internal/storage/mysqldb"
)

// Service 入库 Job（MySQL）与 Task 文档（ES 独立索引）协调；Tasks 可为 nil（仅写 MySQL）。
type Service struct {
	Jobs  *mysqldb.IngestJobRepository
	Tasks *es.IngestTaskIndex
}

// NewService jobs 为 nil 时返回 nil。
func NewService(jobs *mysqldb.IngestJobRepository, tasks *es.IngestTaskIndex) *Service {
	if jobs == nil {
		return nil
	}
	return &Service{Jobs: jobs, Tasks: tasks}
}

// Enabled 表示已配置 MySQL 侧。
func (s *Service) Enabled() bool {
	return s != nil && s.Jobs != nil
}

func formatFromFilename(name string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if ext == "" {
		return "unknown"
	}
	return ext
}

func clipErr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// OnEnqueueMultipart 在 Redis 正文写入成功后、入队前调用。
func (s *Service) OnEnqueueMultipart(ctx context.Context, requestID, jobName string, j queue.Job) error {
	if !s.Enabled() {
		return nil
	}
	if jobName == "" {
		jobName = "ingest"
	}
	pipelineJSON, err := PipelineSnapshotJSON(j)
	if err != nil {
		return err
	}
	if err := s.Jobs.InsertQueued(ctx, j.JobID, jobName, queue.PayloadKindMultipartRedis, requestID, len(j.Files), pipelineJSON); err != nil {
		return err
	}
	if s.Tasks == nil {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	docs := make([]es.TaskQueuedDoc, 0, len(j.Files))
	for i, f := range j.Files {
		tid := strings.TrimSpace(f.TaskID)
		if tid == "" {
			continue
		}
		docs = append(docs, es.TaskQueuedDoc{
			JobID:        j.JobID,
			TaskID:       tid,
			Ordinal:      i,
			PayloadType:  queue.PayloadKindMultipartRedis,
			SourceRef:    f.PayloadKey,
			Filename:     f.Filename,
			Format:       formatFromFilename(f.Filename),
			CreatedAtRFC: now,
			UpdatedAtRFC: now,
		})
	}
	if len(docs) == 0 {
		return nil
	}
	return s.Tasks.BulkIndexQueued(ctx, docs)
}

// OnEnqueueS3 在入队前调用；deferTaskES 为 true 时仅写 Job 行（total_files=0），不占 ES。
func (s *Service) OnEnqueueS3(ctx context.Context, requestID, jobName string, j queue.Job, deferTaskES bool) error {
	if !s.Enabled() {
		return nil
	}
	if jobName == "" {
		jobName = "ingest-remote"
	}
	pipelineJSON, err := PipelineSnapshotJSON(j)
	if err != nil {
		return err
	}
	total := 0
	if deferTaskES {
		total = 0
	} else {
		_, keys, err := ResolveS3EnqueueKeys(j.Bucket, j.S3URIs, j.Keys)
		if err != nil {
			return err
		}
		total = len(keys)
	}
	if err := s.Jobs.InsertQueued(ctx, j.JobID, jobName, queue.PayloadKindS3, requestID, total, pipelineJSON); err != nil {
		return err
	}
	if deferTaskES || s.Tasks == nil {
		return nil
	}
	_, keys, err := ResolveS3EnqueueKeys(j.Bucket, j.S3URIs, j.Keys)
	if err != nil {
		return err
	}
	bucket, err := s3BucketForJob(j)
	if err != nil {
		return err
	}
	return s.bulkS3Queued(ctx, j.JobID, bucket, keys, time.Now().UTC().Format(time.RFC3339Nano))
}

func s3BucketForJob(j queue.Job) (string, error) {
	bucket, _, err := ResolveS3EnqueueKeys(j.Bucket, j.S3URIs, j.Keys)
	return bucket, err
}

func (s *Service) bulkS3Queued(ctx context.Context, jobID, bucket string, keys []string, nowRFC string) error {
	docs := make([]es.TaskQueuedDoc, 0, len(keys))
	for i, k := range keys {
		docs = append(docs, es.TaskQueuedDoc{
			JobID:        jobID,
			TaskID:       S3TaskID(jobID, k),
			Ordinal:      i,
			PayloadType:  queue.PayloadKindS3,
			SourceRef:    "s3://" + bucket + "/" + k,
			Filename:     filepath.Base(k),
			Format:       formatFromFilename(k),
			CreatedAtRFC: nowRFC,
			UpdatedAtRFC: nowRFC,
		})
	}
	if len(docs) == 0 {
		return nil
	}
	return s.Tasks.BulkIndexQueued(ctx, docs)
}

// AfterS3List 仅 prefix 入队时，Worker list 完成后写 ES 占位并更新 total_files。
func (s *Service) AfterS3List(ctx context.Context, jobID, bucket string, keys []string) error {
	if !s.Enabled() || s.Tasks == nil {
		if s.Enabled() && len(keys) > 0 {
			return s.Jobs.SetTotalFiles(ctx, jobID, len(keys))
		}
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.bulkS3Queued(ctx, jobID, bucket, keys, now); err != nil {
		return err
	}
	return s.Jobs.SetTotalFiles(ctx, jobID, len(keys))
}

// OnWorkerJobStart Worker 开始处理整 Job。
func (s *Service) OnWorkerJobStart(ctx context.Context, jobID string) error {
	if !s.Enabled() {
		return nil
	}
	return s.Jobs.MarkRunning(ctx, jobID)
}

// OnTaskRunning 单文件开始处理。
func (s *Service) OnTaskRunning(ctx context.Context, taskID string) error {
	if !s.Enabled() || s.Tasks == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	return s.Tasks.UpdateTaskPartial(ctx, taskID, map[string]any{
		"status":     "running",
		"started_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// OnTaskSucceeded 单文件成功结束。
func (s *Service) OnTaskSucceeded(ctx context.Context, jobID, taskID string, inputLines, chunksWritten int64) error {
	if !s.Enabled() {
		return nil
	}
	if strings.TrimSpace(taskID) != "" && s.Tasks != nil {
		doc := map[string]any{
			"status":        "succeeded",
			"finished_at":   time.Now().UTC().Format(time.RFC3339Nano),
			"success_docs":  inputLines,
			"fail_docs":     0,
			"total_chunks":  chunksWritten,
		}
		if err := s.Tasks.UpdateTaskPartial(ctx, taskID, doc); err != nil {
			return err
		}
	}
	return s.Jobs.AddFileOutcome(ctx, jobID, true, inputLines, 0, chunksWritten)
}

// OnTaskFailed 单文件失败。
func (s *Service) OnTaskFailed(ctx context.Context, jobID, taskID string, runErr error) error {
	if !s.Enabled() {
		return nil
	}
	msg := ""
	if runErr != nil {
		msg = clipErr(runErr.Error(), 512)
	}
	if strings.TrimSpace(taskID) != "" && s.Tasks != nil {
		doc := map[string]any{
			"status":        "failed",
			"finished_at":   time.Now().UTC().Format(time.RFC3339Nano),
			"error_excerpt": msg,
		}
		if err := s.Tasks.UpdateTaskPartial(ctx, taskID, doc); err != nil {
			return err
		}
	}
	return s.Jobs.AddFileOutcome(ctx, jobID, false, 0, 1, 0)
}

// OnWorkerJobSucceeded 整 Job 成功结束。
func (s *Service) OnWorkerJobSucceeded(ctx context.Context, jobID string) error {
	if !s.Enabled() {
		return nil
	}
	return s.Jobs.MarkTerminal(ctx, jobID, "succeeded", "")
}

// OnWorkerJobFailed 整 Job 失败。
func (s *Service) OnWorkerJobFailed(ctx context.Context, jobID string, runErr error) error {
	if !s.Enabled() {
		return nil
	}
	msg := ""
	if runErr != nil {
		msg = clipErr(runErr.Error(), 2040)
	}
	return s.Jobs.MarkTerminal(ctx, jobID, "failed", msg)
}
