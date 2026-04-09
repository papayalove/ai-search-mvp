package es

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Repository 使用 ES HTTP API 做 bulk 写入与 _search 检索（不依赖官方 Go SDK）。
type Repository struct {
	cfg    Config
	client *http.Client
	base   string
}

// NewRepository 创建客户端；cfg 需已通过 Validate。
func NewRepository(cfg Config) (*Repository, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 32
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = maxIdle
	tr.DisableCompression = cfg.DisableCompress
	client := &http.Client{Timeout: timeout, Transport: tr}
	base := NormalizeAddress(cfg.Addresses[0])
	return &Repository{cfg: cfg, client: client, base: base}, nil
}

// Config 返回连接配置副本。
func (r *Repository) Config() Config {
	return r.cfg
}

func (r *Repository) authHeader() string {
	u := strings.TrimSpace(r.cfg.Username)
	if u == "" {
		return ""
	}
	p := r.cfg.Password
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(u+":"+p))
}

// Ping 调用 GET / 检查集群是否可达。
func (r *Repository) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+"/", nil)
	if err != nil {
		return err
	}
	if h := r.authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}
	res, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("es ping: HTTP %s", res.Status)
	}
	return nil
}

// EnsureIndex 若索引不存在则创建并写入 mappings（方案 2：chunk_id 文档 + entity_keys 数组）。
// 若索引已存在且为旧版单字段 entity_key 结构，需自行删索引后重建。
func (r *Repository) EnsureIndex(ctx context.Context) error {
	headReq, err := http.NewRequestWithContext(ctx, http.MethodHead, r.base+"/"+r.cfg.Index, nil)
	if err != nil {
		return err
	}
	if h := r.authHeader(); h != "" {
		headReq.Header.Set("Authorization", h)
	}
	headRes, err := r.client.Do(headReq)
	if err != nil {
		return fmt.Errorf("es head index: %w", err)
	}
	_ = headRes.Body.Close()
	if headRes.StatusCode == http.StatusOK {
		return nil
	}
	if headRes.StatusCode != http.StatusNotFound {
		return fmt.Errorf("es head index %q: HTTP %s", r.cfg.Index, headRes.Status)
	}

	body := map[string]any{
		"mappings": map[string]any{
			"properties": map[string]any{
				"chunk_id":     map[string]string{"type": "keyword"},
				"entity_keys":  map[string]string{"type": "keyword"},
				"doc_id":       map[string]string{"type": "keyword"},
				"source_type":  map[string]string{"type": "keyword"},
				"lang":         map[string]string{"type": "keyword"},
				"job_id":       map[string]string{"type": "keyword"},
				"task_id":      map[string]string{"type": "keyword"},
				"extra_info":   map[string]any{"type": "object", "dynamic": true},
				"created_time": map[string]string{"type": "date"},
				"update_time":  map[string]string{"type": "date"},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, r.base+"/"+r.cfg.Index, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	putReq.Header.Set("Content-Type", "application/json")
	if h := r.authHeader(); h != "" {
		putReq.Header.Set("Authorization", h)
	}
	putRes, err := r.client.Do(putReq)
	if err != nil {
		return fmt.Errorf("es create index: %w", err)
	}
	defer putRes.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(putRes.Body, 1<<20))
	if putRes.StatusCode == http.StatusOK || putRes.StatusCode == http.StatusCreated {
		return nil
	}
	if putRes.StatusCode == http.StatusBadRequest {
		var wrap struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal(b, &wrap) == nil {
			t := strings.ToLower(wrap.Error.Type)
			if strings.Contains(t, "already_exists") || strings.Contains(t, "resource_already_exists") {
				return nil
			}
		}
	}
	return fmt.Errorf("es create index %q: HTTP %s: %s", r.cfg.Index, putRes.Status, bytes.TrimSpace(b))
}

type bulkAction struct {
	Index struct {
		Index string `json:"_index"`
		ID    string `json:"_id"`
	} `json:"index"`
}

type chunkIndexSource struct {
	ChunkID     string         `json:"chunk_id"`
	EntityKeys  []string       `json:"entity_keys"`
	DocID       string         `json:"doc_id"`
	SourceType  string         `json:"source_type"`
	Lang        string         `json:"lang"`
	JobID       string         `json:"job_id,omitempty"`
	TaskID      string         `json:"task_id,omitempty"`
	ExtraInfo   map[string]any `json:"extra_info,omitempty"`
	CreatedTime string         `json:"created_time"`
	UpdatedTime string         `json:"update_time"`
}

// BulkIndexChunkDocs 使用 _bulk 按 chunk_id 索引文档（_id = chunk_id）；同一 chunk 再次写入则覆盖该文档。
func (r *Repository) BulkIndexChunkDocs(ctx context.Context, docs []ChunkEntityDoc) error {
	if len(docs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	ndocs := 0
	for i := range docs {
		d := &docs[i]
		cid := strings.TrimSpace(d.ChunkID)
		if cid == "" || len(d.EntityKeys) == 0 {
			continue
		}
		ndocs++
		act := bulkAction{}
		act.Index.Index = r.cfg.Index
		act.Index.ID = cid
		if err := enc.Encode(act); err != nil {
			return err
		}
		now := time.Now().UTC()
		ct := d.CreatedTime
		if ct.IsZero() {
			ct = now
		}
		ut := d.UpdatedTime
		if ut.IsZero() {
			ut = now
		}
		ex := d.ExtraInfo
		if len(ex) == 0 {
			ex = nil
		}
		src := chunkIndexSource{
			ChunkID:     cid,
			EntityKeys:  d.EntityKeys,
			DocID:       strings.TrimSpace(d.DocID),
			SourceType:  strings.TrimSpace(d.SourceType),
			Lang:        strings.TrimSpace(d.Lang),
			JobID:       strings.TrimSpace(d.JobID),
			TaskID:      strings.TrimSpace(d.TaskID),
			ExtraInfo:   ex,
			CreatedTime: ct.UTC().Format(time.RFC3339Nano),
			UpdatedTime: ut.UTC().Format(time.RFC3339Nano),
		}
		if err := enc.Encode(src); err != nil {
			return err
		}
	}
	if buf.Len() == 0 {
		return nil
	}
	log.Printf("[ingest] elasticsearch bulk request index=%q docs=%d ndjson_bytes=%d endpoint=%q",
		r.cfg.Index, ndocs, buf.Len(), r.base+"/_bulk")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/_bulk", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if h := r.authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}
	res, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("es bulk: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		return fmt.Errorf("es bulk: HTTP %s: %s", res.Status, bytes.TrimSpace(raw))
	}
	var br struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int             `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &br); err != nil {
		return fmt.Errorf("es bulk: decode response: %w", err)
	}
	if br.Errors {
		return fmt.Errorf("es bulk: partial failure: %s", bytes.TrimSpace(raw))
	}
	log.Printf("[ingest] elasticsearch bulk ok index=%q docs=%d items=%d", r.cfg.Index, ndocs, len(br.Items))
	return nil
}

// SearchByEntityKeys 在 entity_keys 上做 bool.should 多 term，命中数近似加总到 _score；排序 _score、update_time、chunk_id。
func (r *Repository) SearchByEntityKeys(ctx context.Context, keys []string, size int) ([]EntityRecallHit, error) {
	if size <= 0 {
		size = 50
	}
	norm := make([]string, 0, len(keys))
	seen := map[string]struct{}{}
	for _, k := range keys {
		k = NormalizeEntityKey(k)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		norm = append(norm, k)
	}
	if len(norm) == 0 {
		return nil, nil
	}
	reqSize := size + 50
	if reqSize > 500 {
		reqSize = 500
	}
	should := make([]any, 0, len(norm))
	for _, k := range norm {
		should = append(should, map[string]any{
			"term": map[string]any{
				"entity_keys": map[string]any{"value": k, "boost": 1.0},
			},
		})
	}
	qbody := map[string]any{
		"size": reqSize,
		"query": map[string]any{
			"bool": map[string]any{
				"should":                 should,
				"minimum_should_match":   1,
				"adjust_pure_negative":   true,
				"boost":                  1.0,
			},
		},
		"sort": []any{
			map[string]string{"_score": "desc"},
			map[string]map[string]string{"update_time": {"order": "desc"}},
			map[string]map[string]string{"chunk_id": {"order": "asc"}},
		},
	}
	raw, err := json.Marshal(qbody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.base+"/"+r.cfg.Index+"/_search", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if h := r.authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}
	res, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("es search: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("es search: HTTP %s: %s", res.Status, bytes.TrimSpace(body))
	}
	var parsed struct {
		Hits struct {
			Hits []struct {
				Score  float64 `json:"_score"`
				Source struct {
					EntityKeys []string `json:"entity_keys"`
					ChunkID    string   `json:"chunk_id"`
					DocID      string   `json:"doc_id"`
					SourceType string   `json:"source_type"`
					Lang       string   `json:"lang"`
					JobID      string   `json:"job_id"`
					TaskID     string   `json:"task_id"`
					UpdateTime string   `json:"update_time"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("es search decode: %w", err)
	}
	out := make([]EntityRecallHit, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		cid := strings.TrimSpace(h.Source.ChunkID)
		if cid == "" {
			continue
		}
		matched := firstMatchedQueryKey(norm, h.Source.EntityKeys)
		var ut time.Time
		if h.Source.UpdateTime != "" {
			if t, err := time.Parse(time.RFC3339Nano, h.Source.UpdateTime); err == nil {
				ut = t
			} else if t, err := time.Parse(time.RFC3339, h.Source.UpdateTime); err == nil {
				ut = t
			}
		}
		out = append(out, EntityRecallHit{
			ChunkID:     cid,
			EntityKey:   matched,
			DocID:       h.Source.DocID,
			SourceType:  h.Source.SourceType,
			Lang:        h.Source.Lang,
			JobID:       h.Source.JobID,
			TaskID:      h.Source.TaskID,
			Score:       h.Score,
			UpdatedTime: ut,
		})
		if len(out) >= size {
			break
		}
	}
	return out, nil
}

func firstMatchedQueryKey(queryNorm []string, docKeys []string) string {
	docSet := make(map[string]struct{}, len(docKeys))
	for _, k := range docKeys {
		docSet[NormalizeEntityKey(k)] = struct{}{}
	}
	for _, q := range queryNorm {
		if _, ok := docSet[q]; ok {
			return q
		}
	}
	return ""
}

// NormalizeEntityKey 与设计「归一化实体键」对齐的 MVP 实现：去空白并统一小写。
func NormalizeEntityKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
