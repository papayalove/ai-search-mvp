package es

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// IngestTaskIndex 运维用 Task（文件）文档索引，与实体倒排索引分离。
type IngestTaskIndex struct {
	r    *Repository
	name string
}

// NewIngestTaskIndex 复用 Repository 的 HTTP 客户端与鉴权。
func NewIngestTaskIndex(r *Repository, indexName string) *IngestTaskIndex {
	if r == nil {
		return nil
	}
	return &IngestTaskIndex{r: r, name: strings.TrimSpace(indexName)}
}

func (t *IngestTaskIndex) idxPath(suffix string) string {
	return t.r.base + "/" + t.name + suffix
}

// EnsureIndex 创建 ingest task 索引（不存在时 PUT）。
func (t *IngestTaskIndex) EnsureIndex(ctx context.Context) error {
	if t == nil || t.r == nil || t.name == "" {
		return fmt.Errorf("ingest task index: nil or empty name")
	}
	headReq, err := http.NewRequestWithContext(ctx, http.MethodHead, t.idxPath(""), nil)
	if err != nil {
		return err
	}
	if h := t.r.authHeader(); h != "" {
		headReq.Header.Set("Authorization", h)
	}
	headRes, err := t.r.client.Do(headReq)
	if err != nil {
		return fmt.Errorf("es head ingest task index: %w", err)
	}
	_ = headRes.Body.Close()
	if headRes.StatusCode == http.StatusOK {
		return nil
	}
	if headRes.StatusCode != http.StatusNotFound {
		return fmt.Errorf("es head ingest task index %q: HTTP %s", t.name, headRes.Status)
	}
	body := map[string]any{
		"mappings": map[string]any{
			"properties": map[string]any{
				"job_id":        map[string]string{"type": "keyword"},
				"task_id":       map[string]string{"type": "keyword"},
				"ordinal":       map[string]string{"type": "integer"},
				"payload_type":  map[string]string{"type": "keyword"},
				"source_ref": map[string]any{"type": "keyword", "ignore_above": 1024},
				"filename": map[string]any{
					"type": "text",
					"fields": map[string]any{
						"keyword": map[string]any{"type": "keyword", "ignore_above": 512},
					},
				},
				"format":        map[string]string{"type": "keyword"},
				"status":        map[string]string{"type": "keyword"},
				"total_docs":    map[string]string{"type": "long"},
				"success_docs":  map[string]string{"type": "long"},
				"fail_docs":     map[string]string{"type": "long"},
				"total_chunks":  map[string]string{"type": "long"},
				"error_excerpt": map[string]string{"type": "text"},
				"created_at":    map[string]string{"type": "date"},
				"updated_at":    map[string]string{"type": "date"},
				"started_at":    map[string]string{"type": "date"},
				"finished_at":   map[string]string{"type": "date"},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, t.idxPath(""), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	putReq.Header.Set("Content-Type", "application/json")
	if h := t.r.authHeader(); h != "" {
		putReq.Header.Set("Authorization", h)
	}
	putRes, err := t.r.client.Do(putReq)
	if err != nil {
		return fmt.Errorf("es create ingest task index: %w", err)
	}
	defer putRes.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(putRes.Body, 1<<20))
	if putRes.StatusCode == http.StatusOK || putRes.StatusCode == http.StatusCreated {
		log.Printf("[ingest-meta] elasticsearch ingest task index %q created", t.name)
		return nil
	}
	return fmt.Errorf("es create ingest task index %q: HTTP %s: %s", t.name, putRes.Status, bytes.TrimSpace(b))
}

// TaskQueuedDoc bulk 占位文档。
type TaskQueuedDoc struct {
	JobID        string
	TaskID       string
	Ordinal      int
	PayloadType  string
	SourceRef    string
	Filename     string
	Format       string
	CreatedAtRFC string
	UpdatedAtRFC string
}

func nowRFC3339Nano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// BulkIndexQueued 方案 A：status=queued，started_at/finished_at 省略。
func (t *IngestTaskIndex) BulkIndexQueued(ctx context.Context, docs []TaskQueuedDoc) error {
	if t == nil || t.r == nil || len(docs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, d := range docs {
		if strings.TrimSpace(d.TaskID) == "" {
			continue
		}
		meta := map[string]any{"index": map[string]any{"_index": t.name, "_id": d.TaskID}}
		if err := enc.Encode(meta); err != nil {
			return err
		}
		src := map[string]any{
			"job_id":       d.JobID,
			"task_id":      d.TaskID,
			"ordinal":      d.Ordinal,
			"payload_type": d.PayloadType,
			"source_ref":   d.SourceRef,
			"filename":     d.Filename,
			"format":       d.Format,
			"status":       "queued",
			"success_docs": 0,
			"fail_docs":    0,
			"total_chunks": 0,
			"created_at":   d.CreatedAtRFC,
			"updated_at":   d.UpdatedAtRFC,
		}
		if err := enc.Encode(src); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.r.base+"/_bulk", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if h := t.r.authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}
	res, err := t.r.client.Do(req)
	if err != nil {
		return fmt.Errorf("ingest task bulk: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		return fmt.Errorf("ingest task bulk: HTTP %s: %s", res.Status, bytes.TrimSpace(raw))
	}
	var br struct {
		Errors bool `json:"errors"`
	}
	if err := json.Unmarshal(raw, &br); err == nil && br.Errors {
		return fmt.Errorf("ingest task bulk: partial failure: %s", bytes.TrimSpace(raw))
	}
	return nil
}

// UpdateTaskPartial POST _update with doc.
func (t *IngestTaskIndex) UpdateTaskPartial(ctx context.Context, taskID string, doc map[string]any) error {
	if t == nil || t.r == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	doc["updated_at"] = nowRFC3339Nano()
	body := map[string]any{"doc": doc}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	u := t.idxPath("/_update/" + url.PathEscape(taskID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if h := t.r.authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}
	res, err := t.r.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return fmt.Errorf("ingest task update %q: HTTP %s: %s", taskID, res.Status, bytes.TrimSpace(b))
	}
	return nil
}
