package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"ai-search-v1/internal/ingest/chunk"
	"ai-search-v1/internal/queue"
	storages3 "ai-search-v1/internal/storage/s3"
)

// JobWorker 消费 queue.Job：multipart 从 Redis 取正文；S3 从对象存储流式读取。
type JobWorker struct {
	Runner *Runner
	Broker *queue.RedisBroker
	S3     *storages3.Client
}

// ProcessJob 执行单条入库任务（末尾 Flush 一次）。
func (w *JobWorker) ProcessJob(ctx context.Context, j queue.Job, co chunk.RecursiveChunkOptions) error {
	if w == nil || w.Runner == nil {
		return fmt.Errorf("job worker: nil runner")
	}
	base := NDJSONRunOptions{
		Partition:   strings.TrimSpace(j.Partition),
		Upsert:      j.Upsert,
		ChunkExpand: j.ChunkExpand,
		ChunkOpts:   co,
		Flush:       false,
		JobID:       strings.TrimSpace(j.JobID),
		TaskID:      strings.TrimSpace(j.TaskID),
	}
	switch j.PayloadKind {
	case queue.PayloadKindMultipartRedis:
		return w.processMultipart(ctx, j, base)
	case queue.PayloadKindS3:
		return w.processS3(ctx, j, base)
	default:
		return fmt.Errorf("unknown payload_kind %q", j.PayloadKind)
	}
}

func (w *JobWorker) processMultipart(ctx context.Context, j queue.Job, base NDJSONRunOptions) error {
	if w.Broker == nil {
		return fmt.Errorf("multipart job requires redis broker")
	}
	var payloadKeys []string
	for _, f := range j.Files {
		if f.PayloadKey == "" {
			continue
		}
		payloadKeys = append(payloadKeys, f.PayloadKey)
		body, err := w.Broker.GetPayload(ctx, f.PayloadKey)
		if err != nil {
			return fmt.Errorf("redis get %q: %w", f.PayloadKey, err)
		}
		ext := strings.ToLower(strings.TrimSpace(filepath.Ext(f.Filename)))
		plain := PlainRunOptions{
			Partition:   base.Partition,
			Upsert:      base.Upsert,
			ChunkExpand: base.ChunkExpand,
			ChunkOpts:   base.ChunkOpts,
			Flush:       false,
			JobID:       base.JobID,
			TaskID:      base.TaskID,
			SourceType:  strings.TrimSpace(j.SourceType),
			Lang:        strings.TrimSpace(j.Lang),
			DocID:       strings.TrimSpace(j.DocID),
			PageNo:      j.PageNo,
		}
		var st RunStats
		var runErr error
		switch ext {
		case ".ndjson", ".jsonl":
			st, runErr = w.Runner.RunNDJSON(ctx, bytes.NewReader(body), base)
		case ".txt", ".md", ".markdown":
			plain.ChunkID = strings.TrimSpace(j.ChunkID)
			st, runErr = w.Runner.RunPlain(ctx, string(body), plain)
		default:
			return fmt.Errorf("unsupported file type %q", ext)
		}
		if runErr != nil {
			return runErr
		}
		_ = st
	}
	if err := w.Runner.Flush(ctx); err != nil {
		return err
	}
	_ = w.Broker.DelPayload(ctx, payloadKeys...)
	return nil
}

func (w *JobWorker) processS3(ctx context.Context, j queue.Job, base NDJSONRunOptions) error {
	if w.S3 == nil {
		return fmt.Errorf("s3 job requires s3 client")
	}
	bucket := strings.TrimSpace(j.Bucket)
	var keys []string
	for _, u := range j.S3URIs {
		buck, k, ok := storages3.ParseS3URI(u)
		if !ok {
			return fmt.Errorf("invalid s3_uri %q", u)
		}
		if bucket == "" {
			bucket = buck
		} else if bucket != buck {
			return fmt.Errorf("mixed buckets in one job not supported")
		}
		keys = append(keys, k)
	}
	for _, k := range j.Keys {
		k = strings.TrimSpace(k)
		if k != "" {
			keys = append(keys, k)
		}
	}
	if bucket == "" {
		return fmt.Errorf("s3 job: bucket is required")
	}
	if prefix := strings.TrimSpace(j.Prefix); prefix != "" {
		listed, err := w.S3.ListObjectKeys(ctx, bucket, prefix)
		if err != nil {
			return fmt.Errorf("s3 list: %w", err)
		}
		keys = append(keys, listed...)
	}
	if len(keys) == 0 {
		return fmt.Errorf("s3 job: no keys to read")
	}
	seen := map[string]struct{}{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		rc, err := w.S3.GetObjectBody(ctx, bucket, key)
		if err != nil {
			return fmt.Errorf("s3 get %s/%s: %w", bucket, key, err)
		}
		st, err := w.Runner.RunNDJSON(ctx, rc, base)
		_ = rc.Close()
		if err != nil {
			return fmt.Errorf("run ndjson for %s: %w", key, err)
		}
		_ = st
	}
	return w.Runner.Flush(ctx)
}
