package milvus

// ChunkEntity is one row for Insert/Upsert.
type ChunkEntity struct {
	ChunkID       string
	DocID         string
	Embedding     []float32
	SourceType    string
	Lang          string
	JobID         string
	TaskID        string
	ExtraInfoJSON string // compact JSON object string
	CreatedTime   int64  // Unix ms
	UpdatedTime   int64  // Unix ms
}

// VectorSearchParams configures ANN search over FieldEmbedding.
type VectorSearchParams struct {
	Vectors    [][]float32
	TopK       int
	Expr       string
	Partitions []string
}

// VectorMatch is one hit for one query vector.
type VectorMatch struct {
	ChunkID     string
	DocID       string
	Score       float32
	SourceType  string
	Lang        string
	JobID       string
	TaskID      string
	CreatedTime int64
	UpdatedTime int64
}

// ChunkRecord is a row returned by Query/Get on chunk_id PK.
type ChunkRecord struct {
	ChunkID       string
	DocID         string
	SourceType    string
	Lang          string
	JobID         string
	TaskID        string
	ExtraInfoJSON string
	CreatedTime   int64
	UpdatedTime   int64
	Embedding     []float32
}
