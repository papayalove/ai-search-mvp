package recall

// Hit 单条召回结果（与 pipeline.SearchHit 字段对齐，避免 recall 依赖 pipeline 造成循环 import）。
type Hit struct {
	ChunkID      string
	DocID        string
	Snippet      string
	Score        float64
	SourceType   string
	Lang         string
	Ts           int64 // 更新时间 ms
	CreatedTs    int64 // 创建时间 ms；0 表示未知
	URLOrDocID   string
	PDFPage      *int
	Title        string
	Offset       int64
	PageNo       int
	Source       string
	RecallSource string // milvus | es | both
}
