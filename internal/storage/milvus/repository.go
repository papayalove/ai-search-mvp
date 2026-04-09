package milvus

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

// Repository wraps Milvus client operations for chunk_vectors_v1.
type Repository struct {
	c   client.Client
	cfg Config

	// EnsureCollection 成功后会置位；写入前若未置位则再调一次（覆盖未跑启动 EnsureCollection 的 Worker 等路径）。
	lazyWriteMu      sync.Mutex
	lazyWriteEnsured bool
}

// NewRepository connects with gRPC and returns a ready-to-use repository.
func NewRepository(ctx context.Context, cfg Config) (*Repository, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cli, err := client.NewClient(ctx, client.Config{
		Address:       cfg.Address,
		Username:      cfg.Username,
		Password:      cfg.Password,
		APIKey:        cfg.APIKey,
		EnableTLSAuth: cfg.EnableTLS,
		DBName:        cfg.DBName,
	})
	if err != nil {
		return nil, fmt.Errorf("milvus connect (username=%q): %w", cfg.Username, err)
	}
	if db := strings.TrimSpace(cfg.DBName); db != "" {
		if err := cli.UsingDatabase(ctx, db); err != nil {
			_ = cli.Close()
			return nil, fmt.Errorf("milvus use database (username=%q, db=%q): %w", cfg.Username, db, err)
		}
	}
	return &Repository{c: cli, cfg: cfg}, nil
}

// Close releases the underlying gRPC connection.
func (r *Repository) Close() error {
	if r == nil || r.c == nil {
		return nil
	}
	return r.c.Close()
}

// Client exposes the low-level Milvus client for advanced call sites.
func (r *Repository) Client() client.Client {
	return r.c
}

// Config returns the resolved configuration (including defaults).
func (r *Repository) Config() Config {
	return r.cfg
}

// ListCollectionNames returns all collection names in the current database (sorted).
func (r *Repository) ListCollectionNames(ctx context.Context) ([]string, error) {
	if r == nil || r.c == nil {
		return nil, fmt.Errorf("milvus: repository not ready")
	}
	cols, err := r.c.ListCollections(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		if c == nil {
			continue
		}
		n := strings.TrimSpace(c.Name)
		if n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

// HasCollection reports whether the configured collection exists.
func (r *Repository) HasCollection(ctx context.Context) (bool, error) {
	return r.c.HasCollection(ctx, r.cfg.Collection)
}

// EnsureCollection creates the collection (if missing), ensures a vector index, and loads it.
func (r *Repository) EnsureCollection(ctx context.Context) error {
	sch, err := collectionSchema(r.cfg)
	if err != nil {
		return err
	}
	has, err := r.c.HasCollection(ctx, r.cfg.Collection)
	if err != nil {
		return fmt.Errorf("has collection: %w", err)
	}
	if !has {
		if err := r.c.CreateCollection(ctx, sch, r.cfg.ShardNum); err != nil {
			return fmt.Errorf("create collection: %w", err)
		}
	}
	if err := r.ensureVectorIndex(ctx); err != nil {
		return fmt.Errorf("ensure vector index: %w", err)
	}
	if err := r.c.LoadCollection(ctx, r.cfg.Collection, false); err != nil {
		return fmt.Errorf("load collection: %w", err)
	}
	r.lazyWriteMu.Lock()
	r.lazyWriteEnsured = true
	r.lazyWriteMu.Unlock()
	return nil
}

func (r *Repository) ensureCollectionForWrite(ctx context.Context) error {
	if r == nil || r.c == nil {
		return fmt.Errorf("milvus: repository not ready")
	}
	r.lazyWriteMu.Lock()
	done := r.lazyWriteEnsured
	r.lazyWriteMu.Unlock()
	if done {
		return nil
	}
	return r.EnsureCollection(ctx)
}

func (r *Repository) ensureVectorIndex(ctx context.Context) error {
	idxs, err := r.c.DescribeIndex(ctx, r.cfg.Collection, FieldEmbedding)
	if err == nil && len(idxs) > 0 {
		return nil
	}
	var idx entity.Index
	switch r.cfg.IndexType {
	case "hnsw":
		idx, err = entity.NewIndexHNSW(entity.COSINE, r.cfg.HNSW_M, r.cfg.HNSW_EfConstruction)
	default:
		idx, err = entity.NewIndexAUTOINDEX(entity.COSINE)
	}
	if err != nil {
		return err
	}
	if err := r.c.CreateIndex(ctx, r.cfg.Collection, FieldEmbedding, idx, false); err != nil {
		return err
	}
	return nil
}

func (r *Repository) searchParam(topK int) (entity.SearchParam, error) {
	switch r.cfg.IndexType {
	case "hnsw":
		ef := r.cfg.HNSW_EF
		if topK > ef {
			ef = topK
		}
		return entity.NewIndexHNSWSearchParam(ef)
	default:
		return entity.NewIndexAUTOINDEXSearchParam(1)
	}
}

// EnsurePartition creates the partition in the configured collection if it does not exist.
// Empty or whitespace partition means default partition — no op.
func (r *Repository) EnsurePartition(ctx context.Context, partition string) error {
	if r == nil || r.c == nil {
		return fmt.Errorf("milvus: repository not ready")
	}
	p := strings.TrimSpace(partition)
	if p == "" {
		return nil
	}
	has, err := r.c.HasPartition(ctx, r.cfg.Collection, p)
	if err != nil {
		return fmt.Errorf("milvus has partition %q: %w", p, err)
	}
	if has {
		return nil
	}
	if err := r.c.CreatePartition(ctx, r.cfg.Collection, p); err != nil {
		return fmt.Errorf("milvus create partition %q: %w", p, err)
	}
	return nil
}

// ListPartitionNames returns partition names for the configured collection (sorted).
func (r *Repository) ListPartitionNames(ctx context.Context) ([]string, error) {
	if r == nil || r.c == nil {
		return nil, fmt.Errorf("milvus: repository not ready")
	}
	parts, err := r.c.ShowPartitions(ctx, r.cfg.Collection)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == nil {
			continue
		}
		n := strings.TrimSpace(p.Name)
		if n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

// InsertChunks inserts entities in batches. Partition empty string uses the default partition.
func (r *Repository) InsertChunks(ctx context.Context, partition string, rows []ChunkEntity) error {
	return r.writeChunks(ctx, partition, rows, false)
}

// UpsertChunks upserts entities in batches (same schema as insert).
func (r *Repository) UpsertChunks(ctx context.Context, partition string, rows []ChunkEntity) error {
	return r.writeChunks(ctx, partition, rows, true)
}

func (r *Repository) writeChunks(ctx context.Context, partition string, rows []ChunkEntity, upsert bool) error {
	if len(rows) == 0 {
		return nil
	}
	if err := r.ensureCollectionForWrite(ctx); err != nil {
		return err
	}
	if err := r.EnsurePartition(ctx, partition); err != nil {
		return err
	}
	if err := validateChunkEntities(rows, r.cfg); err != nil {
		return err
	}
	bs := r.cfg.InsertBatch
	op := "insert"
	if upsert {
		op = "upsert"
	}
	partLabel := strings.TrimSpace(partition)
	if partLabel == "" {
		partLabel = "_default"
	}
	dbLabel := strings.TrimSpace(r.cfg.DBName)
	if dbLabel == "" {
		dbLabel = "(default)"
	}
	n := len(rows)
	rpcN := (n + bs - 1) / bs
	for start := 0; start < len(rows); start += bs {
		end := start + bs
		if end > len(rows) {
			end = len(rows)
		}
		batch := rows[start:end]
		cols, err := chunkColumns(batch, r.cfg.VectorDim)
		if err != nil {
			return err
		}
		var werr error
		if upsert {
			_, werr = r.c.Upsert(ctx, r.cfg.Collection, partition, cols...)
		} else {
			_, werr = r.c.Insert(ctx, r.cfg.Collection, partition, cols...)
		}
		if werr != nil {
			if upsert {
				return fmt.Errorf("milvus upsert: %w", werr)
			}
			return fmt.Errorf("milvus insert: %w", werr)
		}
		log.Printf("[ingest] milvus %s ok collection=%q db=%q partition=%q batch_rows=%d/%d rpc=%d/%d dim=%d",
			op, r.cfg.Collection, dbLabel, partLabel, len(batch), n, (start/bs)+1, rpcN, r.cfg.VectorDim)
	}
	return nil
}

func validateChunkEntities(rows []ChunkEntity, cfg Config) error {
	for i := range rows {
		row := rows[i]
		id := strings.TrimSpace(row.ChunkID)
		if id == "" {
			return fmt.Errorf("row %d: empty chunk_id", i)
		}
		maxLen := cfg.MaxChunkIDLen
		if maxLen <= 0 {
			maxLen = defaultMaxChunkIDLen
		}
		if len(id) > maxLen {
			return fmt.Errorf("row %d: chunk_id longer than %d", i, maxLen)
		}
		if len(row.Embedding) != cfg.VectorDim {
			return fmt.Errorf("row %d: embedding dim %d want %d", i, len(row.Embedding), cfg.VectorDim)
		}
		if len(row.SourceType) > 32 {
			return fmt.Errorf("row %d: source_type exceeds 32 chars", i)
		}
		if len(row.Lang) > 32 {
			return fmt.Errorf("row %d: lang exceeds 32 chars", i)
		}
		maxDoc := effectiveMaxDocIDLen(cfg)
		if len(row.DocID) > maxDoc {
			return fmt.Errorf("row %d: doc_id longer than %d", i, maxDoc)
		}
		maxJ := cfg.MaxJobIDLen
		if maxJ <= 0 {
			maxJ = defaultMaxJobIDLen
		}
		if len(row.JobID) > maxJ {
			return fmt.Errorf("row %d: job_id longer than %d", i, maxJ)
		}
		maxT := cfg.MaxTaskIDLen
		if maxT <= 0 {
			maxT = defaultMaxTaskIDLen
		}
		if len(row.TaskID) > maxT {
			return fmt.Errorf("row %d: task_id longer than %d", i, maxT)
		}
		maxE := cfg.MaxExtraInfoLen
		if maxE <= 0 {
			maxE = defaultMaxExtraInfoLen
		}
		if len(row.ExtraInfoJSON) > maxE {
			return fmt.Errorf("row %d: extra_info longer than %d", i, maxE)
		}
	}
	return nil
}

func effectiveMaxDocIDLen(cfg Config) int {
	if cfg.MaxDocIDLen > 0 {
		return cfg.MaxDocIDLen
	}
	return defaultMaxChunkIDLen
}

func chunkColumns(rows []ChunkEntity, dim int) ([]entity.Column, error) {
	n := len(rows)
	ids := make([]string, n)
	docs := make([]string, n)
	vecs := make([][]float32, n)
	src := make([]string, n)
	langs := make([]string, n)
	jobs := make([]string, n)
	tasks := make([]string, n)
	extras := make([]string, n)
	created := make([]int64, n)
	updated := make([]int64, n)
	for i := range rows {
		ids[i] = strings.TrimSpace(rows[i].ChunkID)
		docs[i] = strings.TrimSpace(rows[i].DocID)
		v := rows[i].Embedding
		cp := make([]float32, len(v))
		copy(cp, v)
		vecs[i] = cp
		src[i] = strings.TrimSpace(rows[i].SourceType)
		langs[i] = strings.TrimSpace(rows[i].Lang)
		jobs[i] = strings.TrimSpace(rows[i].JobID)
		tasks[i] = strings.TrimSpace(rows[i].TaskID)
		ex := strings.TrimSpace(rows[i].ExtraInfoJSON)
		if ex == "" {
			ex = "{}"
		}
		extras[i] = ex
		created[i] = rows[i].CreatedTime
		updated[i] = rows[i].UpdatedTime
	}
	return []entity.Column{
		entity.NewColumnVarChar(FieldChunkID, ids),
		entity.NewColumnVarChar(FieldDocID, docs),
		entity.NewColumnFloatVector(FieldEmbedding, dim, vecs),
		entity.NewColumnVarChar(FieldSourceType, src),
		entity.NewColumnVarChar(FieldLang, langs),
		entity.NewColumnVarChar(FieldJobID, jobs),
		entity.NewColumnVarChar(FieldTaskID, tasks),
		entity.NewColumnVarChar(FieldExtraInfo, extras),
		entity.NewColumnInt64(FieldCreatedTime, created),
		entity.NewColumnInt64(FieldUpdatedTime, updated),
	}, nil
}

// Flush persists inserted data (async=false waits for segment seal).
func (r *Repository) Flush(ctx context.Context, async bool) error {
	log.Printf("[ingest] milvus flush collection=%q async=%v", r.cfg.Collection, async)
	if err := r.c.Flush(ctx, r.cfg.Collection, async); err != nil {
		return err
	}
	log.Printf("[ingest] milvus flush done collection=%q", r.cfg.Collection)
	return nil
}

// LoadCollection loads collection into query nodes.
func (r *Repository) LoadCollection(ctx context.Context, async bool) error {
	return r.c.LoadCollection(ctx, r.cfg.Collection, async)
}

// ReleaseCollection releases loaded collection from memory.
func (r *Repository) ReleaseCollection(ctx context.Context) error {
	return r.c.ReleaseCollection(ctx, r.cfg.Collection)
}

// SearchVectors runs ANN search for one or more query embeddings.
func (r *Repository) SearchVectors(ctx context.Context, p VectorSearchParams) ([][]VectorMatch, error) {
	if len(p.Vectors) == 0 {
		return nil, nil
	}
	if p.TopK <= 0 {
		return nil, fmt.Errorf("topK must be positive")
	}
	for i := range p.Vectors {
		if len(p.Vectors[i]) != r.cfg.VectorDim {
			return nil, fmt.Errorf("query %d: embedding dim %d want %d", i, len(p.Vectors[i]), r.cfg.VectorDim)
		}
	}
	vecs := make([]entity.Vector, len(p.Vectors))
	for i := range p.Vectors {
		vecs[i] = entity.FloatVector(p.Vectors[i])
	}
	sp, err := r.searchParam(p.TopK)
	if err != nil {
		return nil, err
	}
	out := []string{FieldChunkID, FieldDocID, FieldSourceType, FieldLang, FieldJobID, FieldTaskID, FieldCreatedTime, FieldUpdatedTime}
	raw, err := r.c.Search(ctx, r.cfg.Collection, p.Partitions, p.Expr, out, vecs, FieldEmbedding, entity.COSINE, p.TopK, sp)
	if err != nil {
		return nil, fmt.Errorf("milvus search: %w", err)
	}
	res := make([][]VectorMatch, 0, len(raw))
	for qi := range raw {
		sr := raw[qi]
		if sr.Err != nil {
			return nil, sr.Err
		}
		if sr.ResultCount == 0 {
			res = append(res, nil)
			continue
		}
		docCol := sr.Fields.GetColumn(FieldDocID)
		stCol := sr.Fields.GetColumn(FieldSourceType)
		langCol := sr.Fields.GetColumn(FieldLang)
		jobCol := sr.Fields.GetColumn(FieldJobID)
		taskCol := sr.Fields.GetColumn(FieldTaskID)
		ctCol := sr.Fields.GetColumn(FieldCreatedTime)
		utCol := sr.Fields.GetColumn(FieldUpdatedTime)
		row := make([]VectorMatch, 0, sr.ResultCount)
		for j := 0; j < sr.ResultCount; j++ {
			id, err := sr.IDs.GetAsString(j)
			if err != nil {
				return nil, fmt.Errorf("hit %d/%d: id: %w", qi, j, err)
			}
			score := float32(0)
			if j < len(sr.Scores) {
				score = sr.Scores[j]
			}
			row = append(row, VectorMatch{
				ChunkID:     id,
				DocID:       strAt(docCol, j),
				Score:       score,
				SourceType:  strAt(stCol, j),
				Lang:        strAt(langCol, j),
				JobID:       strAt(jobCol, j),
				TaskID:      strAt(taskCol, j),
				CreatedTime: int64At(ctCol, j),
				UpdatedTime: int64At(utCol, j),
			})
		}
		res = append(res, row)
	}
	return res, nil
}

// QueryByChunkIDs loads scalars (and optionally vectors) by primary key.
func (r *Repository) QueryByChunkIDs(ctx context.Context, chunkIDs []string, outputFields []string) ([]ChunkRecord, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	if len(outputFields) == 0 {
		outputFields = []string{FieldChunkID, FieldDocID, FieldSourceType, FieldLang, FieldJobID, FieldTaskID, FieldExtraInfo, FieldCreatedTime, FieldUpdatedTime}
	}
	col := entity.NewColumnVarChar(FieldChunkID, chunkIDs)
	rs, err := r.c.QueryByPks(ctx, r.cfg.Collection, nil, col, outputFields)
	if err != nil {
		return nil, fmt.Errorf("milvus query: %w", err)
	}
	if rs.Len() == 0 {
		return nil, nil
	}
	idCol := rs.GetColumn(FieldChunkID)
	docCol := rs.GetColumn(FieldDocID)
	stCol := rs.GetColumn(FieldSourceType)
	langCol := rs.GetColumn(FieldLang)
	jobCol := rs.GetColumn(FieldJobID)
	taskCol := rs.GetColumn(FieldTaskID)
	exCol := rs.GetColumn(FieldExtraInfo)
	ctCol := rs.GetColumn(FieldCreatedTime)
	utCol := rs.GetColumn(FieldUpdatedTime)
	embCol := rs.GetColumn(FieldEmbedding)
	out := make([]ChunkRecord, rs.Len())
	for i := 0; i < rs.Len(); i++ {
		rec := ChunkRecord{
			ChunkID:       strAt(idCol, i),
			DocID:         strAt(docCol, i),
			SourceType:    strAt(stCol, i),
			Lang:          strAt(langCol, i),
			JobID:         strAt(jobCol, i),
			TaskID:        strAt(taskCol, i),
			ExtraInfoJSON: strAt(exCol, i),
			CreatedTime:   int64At(ctCol, i),
			UpdatedTime:   int64At(utCol, i),
		}
		if embCol != nil {
			v, err := embCol.Get(i)
			if err == nil {
				if fv, ok := v.([]float32); ok {
					rec.Embedding = fv
				}
			}
		}
		out[i] = rec
	}
	return out, nil
}

// QueryByExpr 按布尔表达式查询，带条数上限（Milvus Query + WithLimit）。
// expr 为空时使用 chunk_id != "" 以抽样拉取已有行。withVector 为 true 时包含 embedding 字段。
func (r *Repository) QueryByExpr(ctx context.Context, expr string, limit int64, withVector bool) ([]ChunkRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	if strings.TrimSpace(expr) == "" {
		expr = `chunk_id != ""`
	}
	outFields := []string{FieldChunkID, FieldDocID, FieldSourceType, FieldLang, FieldJobID, FieldTaskID, FieldExtraInfo, FieldCreatedTime, FieldUpdatedTime}
	if withVector {
		outFields = append(outFields, FieldEmbedding)
	}
	rs, err := r.c.Query(ctx, r.cfg.Collection, nil, expr, outFields, client.WithLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("milvus query: %w", err)
	}
	if rs.Len() == 0 {
		return nil, nil
	}
	idCol := rs.GetColumn(FieldChunkID)
	docCol := rs.GetColumn(FieldDocID)
	stCol := rs.GetColumn(FieldSourceType)
	langCol := rs.GetColumn(FieldLang)
	jobCol := rs.GetColumn(FieldJobID)
	taskCol := rs.GetColumn(FieldTaskID)
	exCol := rs.GetColumn(FieldExtraInfo)
	ctCol := rs.GetColumn(FieldCreatedTime)
	utCol := rs.GetColumn(FieldUpdatedTime)
	embCol := rs.GetColumn(FieldEmbedding)
	out := make([]ChunkRecord, rs.Len())
	for i := 0; i < rs.Len(); i++ {
		rec := ChunkRecord{
			ChunkID:       strAt(idCol, i),
			DocID:         strAt(docCol, i),
			SourceType:    strAt(stCol, i),
			Lang:          strAt(langCol, i),
			JobID:         strAt(jobCol, i),
			TaskID:        strAt(taskCol, i),
			ExtraInfoJSON: strAt(exCol, i),
			CreatedTime:   int64At(ctCol, i),
			UpdatedTime:   int64At(utCol, i),
		}
		if embCol != nil {
			v, err := embCol.Get(i)
			if err == nil {
				if fv, ok := v.([]float32); ok {
					rec.Embedding = fv
				}
			}
		}
		out[i] = rec
	}
	return out, nil
}

// DeleteByChunkIDs deletes rows by chunk_id PK.
func (r *Repository) DeleteByChunkIDs(ctx context.Context, partition string, chunkIDs []string) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	const batch = 512
	for start := 0; start < len(chunkIDs); start += batch {
		end := start + batch
		if end > len(chunkIDs) {
			end = len(chunkIDs)
		}
		col := entity.NewColumnVarChar(FieldChunkID, chunkIDs[start:end])
		if err := r.c.DeleteByPks(ctx, r.cfg.Collection, partition, col); err != nil {
			return fmt.Errorf("milvus delete: %w", err)
		}
	}
	return nil
}

func strAt(col entity.Column, i int) string {
	if col == nil {
		return ""
	}
	s, err := col.GetAsString(i)
	if err != nil {
		return ""
	}
	return s
}

func int64At(col entity.Column, i int) int64 {
	if col == nil {
		return 0
	}
	v, err := col.GetAsInt64(i)
	if err != nil {
		return 0
	}
	return v
}
