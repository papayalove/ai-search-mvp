package milvus

import (
	"fmt"
	"strings"
)

const (
	defaultMaxChunkIDLen   = 512
	defaultMaxJobIDLen     = 128
	defaultMaxTaskIDLen    = 256
	defaultMaxExtraInfoLen = 8192
	defaultMaxTitleLen     = 512
	defaultMaxURLLen       = 2048
	defaultShardNum        int32 = 2
	defaultInsertBatch     = 512
	defaultHNSWM              = 16
	defaultHNSWEfConstruction = 200
	defaultHNSWEf             = 64
)

// Config drives client connection and collection layout.
type Config struct {
	Address   string // host:port, e.g. localhost:19530
	Username  string
	Password  string
	APIKey    string
	EnableTLS bool

	DBName     string
	Collection string

	VectorDim     int
	MaxChunkIDLen int
	// MaxDocIDLen 为 doc_id VarChar 最大长度；0 表示与 defaultMaxChunkIDLen 相同。
	MaxDocIDLen int
	ShardNum    int32

	// InsertBatch caps rows per single Insert/Upsert RPC to limit message size.
	InsertBatch int

	// IndexType：空或 autoindex 使用 Milvus AUTOINDEX；hnsw 使用 HNSW（COSINE）。
	// 修改已存在 collection 的索引类型需先在 Milvus 侧删除原向量索引或重建 collection。
	IndexType string
	HNSW_M              int // HNSW M，常用 8–32，默认 16
	HNSW_EfConstruction int // 建索引宽度，默认 200
	HNSW_EF             int // 检索 ef，须 ≥ topK；默认 64，检索时自动与 TopK 取较大值

	MaxJobIDLen     int // VarChar job_id，0 用 defaultMaxJobIDLen
	MaxTaskIDLen    int
	MaxExtraInfoLen int // extra_info JSON 字符串上限，0 用 defaultMaxExtraInfoLen
	MaxTitleLen     int // VarChar title，0 用 defaultMaxTitleLen
	MaxURLLen       int // VarChar url，0 用 defaultMaxURLLen
}

func (c Config) withDefaults() Config {
	c.Collection = strings.TrimSpace(c.Collection)
	if c.Collection == "" {
		c.Collection = defaultCollectionName
	}
	if c.ShardNum <= 0 {
		c.ShardNum = defaultShardNum
	}
	if c.InsertBatch <= 0 {
		c.InsertBatch = defaultInsertBatch
	}
	it := strings.ToLower(strings.TrimSpace(c.IndexType))
	switch {
	case it == "" || it == "auto" || it == "autoindex" || it == "auto_index":
		c.IndexType = "autoindex"
	case it == "hnsw":
		c.IndexType = "hnsw"
		if c.HNSW_M <= 0 {
			c.HNSW_M = defaultHNSWM
		}
		if c.HNSW_EfConstruction <= 0 {
			c.HNSW_EfConstruction = defaultHNSWEfConstruction
		}
		if c.HNSW_EF <= 0 {
			c.HNSW_EF = defaultHNSWEf
		}
	default:
		c.IndexType = it
	}
	return c
}

// Validate checks required fields before connecting or creating a collection.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Address) == "" {
		return fmt.Errorf("milvus address is required")
	}
	if c.VectorDim <= 0 {
		return fmt.Errorf("vector dim must be positive (embedding model output size)")
	}
	switch c.IndexType {
	case "autoindex":
	case "hnsw":
		if c.HNSW_M < 4 || c.HNSW_M > 64 {
			return fmt.Errorf("hnsw: M must be between 4 and 64, got %d", c.HNSW_M)
		}
		if c.HNSW_EfConstruction < 8 || c.HNSW_EfConstruction > 512 {
			return fmt.Errorf("hnsw: ef_construction must be between 8 and 512, got %d", c.HNSW_EfConstruction)
		}
		if c.HNSW_EF < 8 || c.HNSW_EF > 512 {
			return fmt.Errorf("hnsw: ef must be between 8 and 512, got %d", c.HNSW_EF)
		}
	default:
		return fmt.Errorf("milvus: index_type must be autoindex or hnsw, got %q", c.IndexType)
	}
	return nil
}
