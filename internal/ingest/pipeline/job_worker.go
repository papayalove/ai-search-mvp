package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sort"
	"strings"

	"ai-search-v1/internal/ingest/chunk"
	"ai-search-v1/internal/ingest/meta"
	"ai-search-v1/internal/queue"
	storages3 "ai-search-v1/internal/storage/s3"
	"ai-search-v1/pkg/util"
)

// maxS3ObjectBody 与 admin multipart 单文件上限一致，避免单对象撑爆内存。
const maxS3ObjectBody = 32 << 20

// JobWorker 消费 queue.Job：multipart 从 Redis 取正文；S3 从对象存储流式读取。
type JobWorker struct {
	Runner *Runner
	Broker *queue.RedisBroker
	S3     *storages3.Client
	Meta   *ingestmeta.Service
}

// ProcessJob 执行单条入库任务（末尾 Flush 一次）。
func (w *JobWorker) ProcessJob(ctx context.Context, j queue.Job, co chunk.RecursiveChunkOptions) (err error) {
	if w == nil || w.Runner == nil {
		return fmt.Errorf("job worker: nil runner")
	}
	j.JobID = strings.TrimSpace(j.JobID)
	metaOn := w.Meta != nil && w.Meta.Enabled()
	if metaOn {
		if err := w.Meta.OnWorkerJobStart(ctx, j.JobID); err != nil {
			log.Printf("ingest meta: OnWorkerJobStart job_id=%s: %v", j.JobID, err)
			return err
		}
		defer func() {
			if err != nil {
				if e := w.Meta.OnWorkerJobFailed(ctx, j.JobID, err); e != nil {
					log.Printf("ingest meta: OnWorkerJobFailed job_id=%s: %v", j.JobID, e)
				}
			} else {
				if e := w.Meta.OnWorkerJobSucceeded(ctx, j.JobID); e != nil {
					log.Printf("ingest meta: OnWorkerJobSucceeded job_id=%s: %v", j.JobID, e)
				}
			}
		}()
	}
	base := NDJSONRunOptions{
		Partition:     strings.TrimSpace(j.Partition),
		Upsert:        j.Upsert,
		ChunkOpts:     co,
		Flush:         false,
		JobID:         strings.TrimSpace(j.JobID),
		TaskID:        strings.TrimSpace(j.TaskID),
		JobSourceType: strings.TrimSpace(j.SourceType),
	}
	switch j.PayloadKind {
	case queue.PayloadKindMultipartRedis:
		err = w.processMultipart(ctx, j, base, metaOn)
	case queue.PayloadKindS3:
		err = w.processS3(ctx, j, base, metaOn)
	default:
		err = fmt.Errorf("unknown payload_kind %q", j.PayloadKind)
	}
	return err
}

func taskIDForMultipartFile(j queue.Job, f queue.FileRef) string {
	if tid := strings.TrimSpace(f.TaskID); tid != "" {
		return tid
	}
	return strings.TrimSpace(j.TaskID)
}

func (w *JobWorker) processMultipart(ctx context.Context, j queue.Job, base NDJSONRunOptions, metaOn bool) error {
	if w.Broker == nil {
		return fmt.Errorf("multipart job requires redis broker")
	}
	var payloadKeys []string
	for _, f := range j.Files {
		if f.PayloadKey == "" {
			continue
		}
		payloadKeys = append(payloadKeys, f.PayloadKey)
		taskID := taskIDForMultipartFile(j, f)
		body, err := w.Broker.GetPayload(ctx, f.PayloadKey)
		if err != nil {
			if metaOn && taskID != "" {
				_ = w.Meta.OnTaskFailed(ctx, j.JobID, taskID, err)
			}
			return fmt.Errorf("redis get %q: %w", f.PayloadKey, err)
		}
		if metaOn && taskID != "" {
			if err := w.Meta.OnTaskRunning(ctx, taskID); err != nil {
				return err
			}
		}
		ext := strings.ToLower(strings.TrimSpace(filepath.Ext(f.Filename)))
		plain := PlainRunOptions{
			Partition:  base.Partition,
			Upsert:     base.Upsert,
			ChunkOpts:  base.ChunkOpts,
			Flush:      false,
			JobID:      base.JobID,
			TaskID:     taskID,
			SourceType: EffectiveIngestSourceType(ext, j.SourceType),
			Lang:       strings.TrimSpace(j.Lang),
			DocID:      strings.TrimSpace(j.DocID),
			Title:      strings.TrimSpace(j.Title),
			URL:        strings.TrimSpace(j.URL),
			PageNo:     j.PageNo,
		}
		var st RunStats
		var runErr error
		switch ext {
		case ".ndjson", ".jsonl", ".json":
			opt := base
			opt.TaskID = taskID
			opt.FileExt = ext
			st, runErr = w.Runner.RunNDJSON(ctx, bytes.NewReader(body), opt)
		case ".txt", ".md", ".markdown":
			plain.ChunkID = strings.TrimSpace(j.ChunkID)
			if plain.Title == "" {
				plain.Title = filepath.Base(f.Filename)
			}
			st, runErr = w.Runner.RunPlain(ctx, string(body), plain)
		default:
			runErr = fmt.Errorf("unsupported file type %q", ext)
		}
		if metaOn && taskID != "" {
			if runErr != nil {
				if e := w.Meta.OnTaskFailed(ctx, j.JobID, taskID, runErr); e != nil {
					return e
				}
				return runErr
			}
			if e := w.Meta.OnTaskSucceeded(ctx, j.JobID, taskID, int64(st.InputLines), int64(st.ChunksWritten)); e != nil {
				return e
			}
		} else if runErr != nil {
			return runErr
		}
	}
	if err := w.Runner.Flush(ctx); err != nil {
		return err
	}
	_ = w.Broker.DelPayload(ctx, payloadKeys...)
	return nil
}

func (w *JobWorker) processS3(ctx context.Context, j queue.Job, base NDJSONRunOptions, metaOn bool) error {
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
	seen := map[string]struct{}{}
	var uniq []string
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		uniq = append(uniq, key)
	}
	sort.Strings(uniq)
	if len(uniq) == 0 {
		return fmt.Errorf("s3 job: no keys to read")
	}
	if metaOn && j.S3DeferTaskES {
		if err := w.Meta.AfterS3List(ctx, j.JobID, bucket, uniq); err != nil {
			return err
		}
	}
	for _, key := range uniq {
		tid := ingestmeta.S3TaskID(j.JobID, key)
		if metaOn {
			if err := w.Meta.OnTaskRunning(ctx, tid); err != nil {
				return err
			}
		}
		rc, err := w.S3.GetObjectBody(ctx, bucket, key)
		if err != nil {
			if metaOn {
				if e := w.Meta.OnTaskFailed(ctx, j.JobID, tid, err); e != nil {
					return e
				}
			}
			return fmt.Errorf("s3 get %s/%s: %w", bucket, key, err)
		}
		body, rerr := io.ReadAll(io.LimitReader(rc, maxS3ObjectBody+1))
		_ = rc.Close()
		if rerr != nil {
			if metaOn {
				if e := w.Meta.OnTaskFailed(ctx, j.JobID, tid, rerr); e != nil {
					return e
				}
			}
			return fmt.Errorf("s3 read %s/%s: %w", bucket, key, rerr)
		}
		if len(body) > maxS3ObjectBody {
			tooBig := fmt.Errorf("object exceeds %d bytes", maxS3ObjectBody)
			if metaOn {
				if e := w.Meta.OnTaskFailed(ctx, j.JobID, tid, tooBig); e != nil {
					return e
				}
			}
			return fmt.Errorf("s3 %s/%s: %w", bucket, key, tooBig)
		}

		opt := base
		opt.TaskID = tid
		ext := strings.ToLower(strings.TrimSpace(filepath.Ext(key)))
		plain := PlainRunOptions{
			Partition:  base.Partition,
			Upsert:     base.Upsert,
			ChunkOpts:  base.ChunkOpts,
			Flush:      false,
			JobID:      base.JobID,
			TaskID:     tid,
			SourceType: EffectiveIngestSourceType(ext, j.SourceType),
			Lang:       strings.TrimSpace(j.Lang),
			DocID:      strings.TrimSpace(j.DocID),
			Title:      strings.TrimSpace(j.Title),
			URL:        strings.TrimSpace(j.URL),
			PageNo:     j.PageNo,
		}
		var st RunStats
		switch ext {
		case ".ndjson", ".jsonl", ".json":
			opt.FileExt = ext
			st, err = w.Runner.RunNDJSON(ctx, bytes.NewReader(body), opt)
		case ".txt", ".md", ".markdown":
			plain.ChunkID = strings.TrimSpace(j.ChunkID)
			if strings.TrimSpace(plain.DocID) == "" {
				plain.DocID = util.StableDocIDFromS3Object(bucket, key)
			}
			if plain.Title == "" {
				plain.Title = filepath.Base(key)
			}
			if plain.URL == "" {
				plain.URL = fmt.Sprintf("s3://%s/%s", bucket, key)
			}
			st, err = w.Runner.RunPlain(ctx, string(body), plain)
		default:
			err = fmt.Errorf("unsupported extension %q (use .ndjson, .jsonl, .json, .txt, .md, .markdown)", ext)
		}
		if metaOn {
			if err != nil {
				if e := w.Meta.OnTaskFailed(ctx, j.JobID, tid, err); e != nil {
					return e
				}
				return fmt.Errorf("ingest s3 key %s: %w", key, err)
			}
			if e := w.Meta.OnTaskSucceeded(ctx, j.JobID, tid, int64(st.InputLines), int64(st.ChunksWritten)); e != nil {
				return e
			}
		} else if err != nil {
			return fmt.Errorf("ingest s3 key %s: %w", key, err)
		}
	}
	return w.Runner.Flush(ctx)
}
