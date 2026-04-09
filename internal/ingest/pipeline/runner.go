// Package pipeline 提供与传输无关的入库编排：NDJSON 流、纯文本文档 → 分块、嵌入、写入 Milvus。
package pipeline

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"

	"ai-search-v1/internal/ingest/chunk"
	"ai-search-v1/internal/model/embedding"
	"ai-search-v1/internal/storage/es"
	"ai-search-v1/internal/storage/milvus"
	"ai-search-v1/pkg/util"
)

const defaultEmbedBatch = 32

// Runner 将 chunk 行批量嵌入并写入 Milvus（与 cmd/importer 共用）。
type Runner struct {
	Embedder embedding.Embedder
	Repo     *milvus.Repository
	// ES 非 nil 且 chunk 行含 entity_keys 时，在 Milvus 写入成功后同步 bulk 写入实体倒排。
	ES *es.Repository
	// MaxBatch 每批调用 Embed 的最大条数；≤0 时用 embedding 配置或 32。
	MaxBatch int
}

// NDJSONRunOptions 控制 NDJSON 流式导入行为。
type NDJSONRunOptions struct {
	Partition   string
	Upsert      bool
	ChunkExpand bool
	ChunkOpts   chunk.RecursiveChunkOptions
	Flush       bool
	// JobID/TaskID 写入 Milvus/ES；行内非空时覆盖。
	JobID  string
	TaskID string
}

// PlainRunOptions 控制单个纯文本文件（txt/md）导入。
type PlainRunOptions struct {
	ChunkID     string
	DocID       string
	PageNo      int
	SourceType  string
	Lang        string
	ChunkExpand bool
	ChunkOpts   chunk.RecursiveChunkOptions
	Partition   string
	Upsert      bool
	Flush       bool
	JobID       string
	TaskID      string
}

// RunStats 汇总一次导入写入 Milvus 的 chunk 行数（非输入逻辑行数）。
type RunStats struct {
	InputLines    int
	ChunksWritten int
}

// PreviewNDJSON 仅解析与分块统计，不连 Milvus、不嵌入（供 importer -dry-run）。
func PreviewNDJSON(input io.Reader, chunkExpand bool, opts chunk.RecursiveChunkOptions) (inputLines, chunkRows int, err error) {
	sc := bufio.NewScanner(input)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 16*1024*1024)
	for sc.Scan() {
		b := sc.Bytes()
		if len(trimLineNL(b)) == 0 {
			continue
		}
		rec, perr := chunk.ParseTextChunkLine(b)
		if perr != nil {
			return inputLines, chunkRows, fmt.Errorf("line %d: %w", inputLines+1, perr)
		}
		inputLines++
		if chunkExpand {
			rows, serr := chunk.SplitRecord(rec, opts)
			if serr != nil {
				return inputLines, chunkRows, fmt.Errorf("line %d chunk: %w", inputLines, serr)
			}
			chunkRows += len(rows)
		} else {
			chunkRows++
		}
	}
	if err := sc.Err(); err != nil {
		return inputLines, chunkRows, err
	}
	return inputLines, chunkRows, nil
}

// RunNDJSON 按行读取 NDJSON，可选递归分块后嵌入并写入 Milvus。
func (r *Runner) RunNDJSON(ctx context.Context, input io.Reader, opt NDJSONRunOptions) (RunStats, error) {
	if err := r.validate(); err != nil {
		return RunStats{}, err
	}
	batch := r.batchSize()
	var st RunStats
	sc := bufio.NewScanner(input)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 16*1024*1024)
	pending := make([]chunk.TextChunkLine, 0, batch)
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		n, err := r.writeBatch(ctx, opt.Partition, opt.Upsert, pending, opt.JobID, opt.TaskID)
		if err != nil {
			return err
		}
		st.ChunksWritten += n
		pending = pending[:0]
		return nil
	}
	for sc.Scan() {
		b := sc.Bytes()
		if len(trimLineNL(b)) == 0 {
			continue
		}
		rec, err := chunk.ParseTextChunkLine(b)
		if err != nil {
			return st, fmt.Errorf("line %d: %w", st.InputLines+1, err)
		}
		st.InputLines++
		var rows []chunk.TextChunkLine
		if opt.ChunkExpand {
			rows, err = chunk.SplitRecord(rec, opt.ChunkOpts)
			if err != nil {
				return st, fmt.Errorf("line %d chunk: %w", st.InputLines, err)
			}
		} else {
			rows = []chunk.TextChunkLine{rec}
		}
		for _, row := range rows {
			pending = append(pending, row)
			if len(pending) >= batch {
				if err := flush(); err != nil {
					return st, err
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return st, err
	}
	if err := flush(); err != nil {
		return st, err
	}
	if opt.Flush {
		if err := r.Repo.Flush(ctx, false); err != nil {
			return st, fmt.Errorf("flush: %w", err)
		}
	}
	return st, nil
}

// RunPlain 将整段文本作为一条逻辑文档导入（可选再递归分块）。
func (r *Runner) RunPlain(ctx context.Context, text string, opt PlainRunOptions) (RunStats, error) {
	if err := r.validate(); err != nil {
		return RunStats{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return RunStats{}, fmt.Errorf("empty text")
	}
	docID := strings.TrimSpace(opt.DocID)
	id := strings.TrimSpace(opt.ChunkID)
	if id == "" {
		if docID == "" {
			return RunStats{}, fmt.Errorf("doc_id is required when chunk_id is omitted (chunk_id = stable hash of doc_id, page_no, chunk_no)")
		}
		id = util.StableChunkID(docID, opt.PageNo, 0)
	} else {
		id = sanitizeChunkID(id)
		if len(id) > r.effectiveMaxChunkIDLen() {
			return RunStats{}, fmt.Errorf("chunk_id exceeds max length %d", r.effectiveMaxChunkIDLen())
		}
	}
	src := strings.TrimSpace(opt.SourceType)
	if src == "" {
		src = "default"
	}
	lang := strings.TrimSpace(opt.Lang)
	if lang == "" {
		lang = "und"
	}
	now := time.Now().UnixMilli()
	rec := chunk.TextChunkLine{
		ChunkID:    id,
		Text:       text,
		DocID:      docID,
		PageNo:     opt.PageNo,
		ChunkNo:    0,
		SourceType: src,
		Lang:       lang,
		CreatedMs:  now,
		UpdatedMs:  now,
		JobID:      strings.TrimSpace(opt.JobID),
		TaskID:     strings.TrimSpace(opt.TaskID),
	}
	var rows []chunk.TextChunkLine
	var err error
	if opt.ChunkExpand {
		rows, err = chunk.SplitRecord(rec, opt.ChunkOpts)
	} else {
		rows = []chunk.TextChunkLine{rec}
	}
	if err != nil {
		return RunStats{}, err
	}
	bs := r.batchSize()
	var written int
	for start := 0; start < len(rows); start += bs {
		end := start + bs
		if end > len(rows) {
			end = len(rows)
		}
		n, werr := r.writeBatch(ctx, opt.Partition, opt.Upsert, rows[start:end], opt.JobID, opt.TaskID)
		if werr != nil {
			return RunStats{InputLines: 1, ChunksWritten: written}, werr
		}
		written += n
	}
	st := RunStats{InputLines: 1, ChunksWritten: written}
	if opt.Flush {
		if err := r.Repo.Flush(ctx, false); err != nil {
			return st, fmt.Errorf("flush: %w", err)
		}
	}
	return st, nil
}

// Flush 将 Milvus 缓冲区落盘（通常在批量上传请求末尾调用一次）。
func (r *Runner) Flush(ctx context.Context) error {
	if r.Repo == nil {
		return fmt.Errorf("pipeline: milvus repository is nil")
	}
	return r.Repo.Flush(ctx, false)
}

func (r *Runner) validate() error {
	if r.Embedder == nil {
		return fmt.Errorf("pipeline: embedder is nil")
	}
	if r.Repo == nil {
		return fmt.Errorf("pipeline: milvus repository is nil")
	}
	return nil
}

func (r *Runner) batchSize() int {
	if r.MaxBatch > 0 {
		return r.MaxBatch
	}
	return defaultEmbedBatch
}

func (r *Runner) effectiveMaxChunkIDLen() int {
	if r.Repo == nil {
		return 512
	}
	ml := r.Repo.Config().MaxChunkIDLen
	if ml <= 0 {
		return 512
	}
	return ml
}

func (r *Runner) writeBatch(ctx context.Context, partition string, upsert bool, batch []chunk.TextChunkLine, defaultJobID, defaultTaskID string) (int, error) {
	if len(batch) == 0 {
		return 0, nil
	}
	texts := make([]string, len(batch))
	for i := range batch {
		texts[i] = batch[i].Text
	}
	vecs, err := r.Embedder.Embed(ctx, texts)
	if err != nil {
		return 0, fmt.Errorf("embed batch of %d: %w", len(batch), err)
	}
	if len(vecs) != len(batch) {
		return 0, fmt.Errorf("embed: got %d vectors for %d texts", len(vecs), len(batch))
	}
	rows := make([]milvus.ChunkEntity, len(batch))
	for i := range batch {
		jid := strings.TrimSpace(batch[i].JobID)
		if jid == "" {
			jid = strings.TrimSpace(defaultJobID)
		}
		tid := strings.TrimSpace(batch[i].TaskID)
		if tid == "" {
			tid = strings.TrimSpace(defaultTaskID)
		}
		rows[i] = milvus.ChunkEntity{
			ChunkID:       batch[i].ChunkID,
			DocID:         strings.TrimSpace(batch[i].DocID),
			Embedding:     vecs[i],
			SourceType:    batch[i].SourceType,
			Lang:          batch[i].Lang,
			JobID:         jid,
			TaskID:        tid,
			ExtraInfoJSON: extraInfoJSONString(batch[i].ExtraInfo),
			CreatedTime:   batch[i].CreatedMs,
			UpdatedTime:   batch[i].UpdatedMs,
		}
	}
	if upsert {
		err = r.Repo.UpsertChunks(ctx, partition, rows)
	} else {
		err = r.Repo.InsertChunks(ctx, partition, rows)
	}
	if err != nil {
		return 0, err
	}
	if r.ES != nil {
		docs, err := entityChunkDocsFromBatch(batch, defaultJobID, defaultTaskID)
		if err != nil {
			return 0, err
		}
		if len(docs) > 0 {
			if err := r.ES.BulkIndexChunkDocs(ctx, docs); err != nil {
				return 0, fmt.Errorf("es bulk: %w", err)
			}
		}
	}
	return len(batch), nil
}

func extraInfoJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// entityChunkDocsFromBatch 方案 2：按 chunk_id 聚合，entity_keys 为归一化后的去重列表；同批同一 chunk 合并 keys。
func entityChunkDocsFromBatch(batch []chunk.TextChunkLine, defaultJobID, defaultTaskID string) ([]es.ChunkEntityDoc, error) {
	type acc struct {
		keys        map[string]struct{}
		docID       string
		sourceType  string
		lang        string
		updatedMs   int64
		createdMs   int64
		jobID       string
		taskID      string
		extra       map[string]any
	}
	byChunk := make(map[string]*acc)
	for i := range batch {
		row := &batch[i]
		if len(row.EntityKeys) == 0 {
			continue
		}
		docID := strings.TrimSpace(row.DocID)
		if docID == "" {
			return nil, fmt.Errorf("es: chunk_id %q has entity_keys but doc_id is empty (doc_id must be set explicitly)", row.ChunkID)
		}
		cid := strings.TrimSpace(row.ChunkID)
		if cid == "" {
			continue
		}
		a, ok := byChunk[cid]
		jid := strings.TrimSpace(row.JobID)
		if jid == "" {
			jid = strings.TrimSpace(defaultJobID)
		}
		tid := strings.TrimSpace(row.TaskID)
		if tid == "" {
			tid = strings.TrimSpace(defaultTaskID)
		}
		var rowExtra map[string]any
		if len(row.ExtraInfo) > 0 {
			_ = json.Unmarshal(row.ExtraInfo, &rowExtra)
		}
		if !ok {
			a = &acc{keys: make(map[string]struct{}), extra: map[string]any{}}
			a.docID = docID
			a.sourceType = row.SourceType
			a.lang = row.Lang
			a.updatedMs = row.UpdatedMs
			a.createdMs = row.CreatedMs
			a.jobID = jid
			a.taskID = tid
			for k, v := range rowExtra {
				a.extra[k] = v
			}
			byChunk[cid] = a
		} else {
			if a.docID != docID {
				return nil, fmt.Errorf("es: chunk_id %q: conflicting doc_id %q vs %q in same batch", cid, a.docID, docID)
			}
			if row.UpdatedMs > a.updatedMs {
				a.updatedMs = row.UpdatedMs
			}
			if row.CreatedMs < a.createdMs || a.createdMs == 0 {
				a.createdMs = row.CreatedMs
			}
			for k, v := range rowExtra {
				a.extra[k] = v
			}
		}
		for _, ek := range row.EntityKeys {
			k := es.NormalizeEntityKey(ek)
			if k != "" {
				a.keys[k] = struct{}{}
			}
		}
	}
	out := make([]es.ChunkEntityDoc, 0, len(byChunk))
	for cid, a := range byChunk {
		keys := make([]string, 0, len(a.keys))
		for k := range a.keys {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			continue
		}
		if len(a.extra) == 0 {
			a.extra = nil
		}
		out = append(out, es.ChunkEntityDoc{
			ChunkID:     cid,
			EntityKeys:  keys,
			DocID:       a.docID,
			SourceType:  a.sourceType,
			Lang:        a.lang,
			JobID:       a.jobID,
			TaskID:      a.taskID,
			ExtraInfo:   a.extra,
			CreatedTime: time.UnixMilli(a.createdMs),
			UpdatedTime: time.UnixMilli(a.updatedMs),
		})
	}
	return out, nil
}

func sanitizeChunkID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else if r == ' ' || r == '/' || r == '\\' {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func trimLineNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	return b[i:]
}
