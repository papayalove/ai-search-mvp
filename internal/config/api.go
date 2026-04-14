package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"ai-search-v1/internal/ingest/chunk"
	"ai-search-v1/internal/storage/es"
	"ai-search-v1/internal/storage/milvus"

	"gopkg.in/yaml.v3"
)

// DefaultAPIConfigPath is the default api.yaml path when callers do not pass -config.
const DefaultAPIConfigPath = "configs/api.yaml"

// API holds settings loaded from configs/api.yaml.
type API struct {
	HTTP          HTTPConfig          `yaml:"http"`
	Milvus        MilvusConfig        `yaml:"milvus"`
	Elasticsearch ElasticsearchConfig `yaml:"elasticsearch"`
	Embedding     EmbeddingConfig     `yaml:"embedding"`
	Ingest        IngestConfig        `yaml:"ingest"`
}

// IngestConfig 控制 cmd/importer 在 -chunk 时的递归字符切分（与 LangChain RecursiveCharacterTextSplitter 同类）。
type IngestConfig struct {
	ChunkSize       int   `yaml:"chunk_size"`
	ChunkOverlap    int   `yaml:"chunk_overlap"`
	KeepSeparator   *bool `yaml:"keep_separator"` // 默认 true；对应 Python keep_separator=True
}

// ToRecursiveChunkOptions 将 YAML 映射为 ingest/chunk 选项；chunk_size/chunk_overlap 为 0 时由 ChunkTextRecursively 使用 768/128。
func (i IngestConfig) ToRecursiveChunkOptions() chunk.RecursiveChunkOptions {
	keep := true
	if i.KeepSeparator != nil {
		keep = *i.KeepSeparator
	}
	return chunk.RecursiveChunkOptions{
		ChunkSize:       i.ChunkSize,
		ChunkOverlap:    i.ChunkOverlap,
		KeepSeparator:   keep,
		Separators:      nil,
		Len:             nil,
	}
}

// HTTPConfig is HTTP server options.
type HTTPConfig struct {
	Addr string `yaml:"addr"`
}

// MilvusConfig mirrors internal/storage/milvus.Config for YAML.
type MilvusConfig struct {
	// Enabled 为 nil 或未写时视为 true；仅当显式写 enabled: false 时关闭连接。
	Enabled       *bool  `yaml:"enabled"`
	Address       string `yaml:"address"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
	APIKey        string `yaml:"api_key"`
	EnableTLS     bool   `yaml:"enable_tls"`
	DBName        string `yaml:"db_name"`
	Collection    string `yaml:"collection"`
	VectorDim     int    `yaml:"vector_dim"`
	MaxChunkIDLen int    `yaml:"max_chunk_id_len"`
	MaxDocIDLen   int    `yaml:"max_doc_id_len"`
	ShardNum      int32  `yaml:"shard_num"`
	InsertBatch   int `yaml:"insert_batch"`
	MaxJobIDLen   int `yaml:"max_job_id_len"`
	MaxTaskIDLen  int `yaml:"max_task_id_len"`
	MaxExtraInfoLen int `yaml:"max_extra_info_len"`
	MaxTitleLen     int `yaml:"max_title_len"`
	MaxURLLen       int `yaml:"max_url_len"`

	// 向量索引：autoindex（默认）或 hnsw；修改已存在 collection 需删原向量索引或重建 collection。
	IndexType           string `yaml:"index_type"`
	HNSW_M              int    `yaml:"hnsw_m"`
	HNSW_EfConstruction int    `yaml:"hnsw_ef_construction"`
	HNSW_EF             int    `yaml:"hnsw_ef"`
}

// MilvusEnabled reports whether to connect to Milvus (default true).
func (m MilvusConfig) MilvusEnabled() bool {
	if m.Enabled == nil {
		return true
	}
	return *m.Enabled
}

// ElasticsearchConfig 与 internal/storage/es.Config 对应；密码建议用环境变量 ES_PASSWORD。
type ElasticsearchConfig struct {
	Enabled              *bool    `yaml:"enabled"`
	Addresses            []string `yaml:"addresses"`
	Address              string   `yaml:"address"`
	Username             string   `yaml:"username"`
	Password             string   `yaml:"password"`
	Index                string   `yaml:"index"`
	RequestTimeoutSeconds int     `yaml:"request_timeout_seconds"`
}

// ElasticsearchEnabled 仅当显式 enabled: true 时启用（默认不连 ES）。
func (e ElasticsearchConfig) ElasticsearchEnabled() bool {
	if e.Enabled == nil {
		return false
	}
	return *e.Enabled
}

// ToElasticsearch 合并 YAML 与环境变量：ES_URL、ES_ADDRESSES、ES_USERNAME、ES_PASSWORD、ES_INDEX。
func (e ElasticsearchConfig) ToElasticsearch() es.Config {
	addrs := append([]string(nil), e.Addresses...)
	if v := strings.TrimSpace(e.Address); v != "" {
		addrs = append([]string{v}, addrs...)
	}
	if v := strings.TrimSpace(os.Getenv("ES_URL")); v != "" {
		addrs = []string{v}
	}
	if v := strings.TrimSpace(os.Getenv("ES_ADDRESSES")); v != "" {
		parts := strings.Split(v, ",")
		addrs = addrs[:0]
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				addrs = append(addrs, p)
			}
		}
	}
	username := strings.TrimSpace(e.Username)
	if v := strings.TrimSpace(os.Getenv("ES_USERNAME")); v != "" {
		username = v
	}
	password := strings.TrimSpace(e.Password)
	if v, ok := os.LookupEnv("ES_PASSWORD"); ok {
		password = v
	}
	index := strings.TrimSpace(e.Index)
	if v := strings.TrimSpace(os.Getenv("ES_INDEX")); v != "" {
		index = v
	}
	if index == "" {
		index = es.DefaultIndexName
	}
	to := time.Duration(e.RequestTimeoutSeconds) * time.Second
	if v := strings.TrimSpace(os.Getenv("ES_REQUEST_TIMEOUT_SECONDS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			to = time.Duration(n) * time.Second
		}
	}
	return es.Config{
		Addresses:      addrs,
		Username:       username,
		Password:       password,
		Index:          index,
		RequestTimeout: to,
	}
}

// ToMilvus maps YAML config to the milvus client config.
// 非空环境变量覆盖文件：MILVUS_ADDRESS、MILVUS_USERNAME、MILVUS_PASSWORD、MILVUS_API_KEY、MILVUS_DB_NAME、
// MILVUS_INDEX_TYPE、MILVUS_HNSW_M、MILVUS_HNSW_EF_CONSTRUCTION、MILVUS_HNSW_EF。
// MILVUS_ADDRESS 支持 tcp://host:port（与 pymilvus MilvusClient(uri=...) 写法对齐，仅取 host:port）。
func (m MilvusConfig) ToMilvus() milvus.Config {
	addr := NormalizeMilvusAddress(m.Address)
	if v := strings.TrimSpace(os.Getenv("MILVUS_ADDRESS")); v != "" {
		addr = NormalizeMilvusAddress(v)
	}
	username := m.Username
	if v := strings.TrimSpace(os.Getenv("MILVUS_USERNAME")); v != "" {
		username = v
	}
	password := m.Password
	if v, ok := os.LookupEnv("MILVUS_PASSWORD"); ok {
		password = v
	}
	apiKey := m.APIKey
	if v := strings.TrimSpace(os.Getenv("MILVUS_API_KEY")); v != "" {
		apiKey = v
	}
	dbName := strings.TrimSpace(m.DBName)
	if v := strings.TrimSpace(os.Getenv("MILVUS_DB_NAME")); v != "" {
		dbName = v
	}
	maxDoc := m.MaxDocIDLen
	if v := strings.TrimSpace(os.Getenv("MILVUS_MAX_DOC_ID_LEN")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxDoc = n
		}
	}
	indexType := strings.TrimSpace(m.IndexType)
	if v := strings.TrimSpace(os.Getenv("MILVUS_INDEX_TYPE")); v != "" {
		indexType = v
	}
	hnswM := m.HNSW_M
	if v := strings.TrimSpace(os.Getenv("MILVUS_HNSW_M")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			hnswM = n
		}
	}
	hnswEfConstruction := m.HNSW_EfConstruction
	if v := strings.TrimSpace(os.Getenv("MILVUS_HNSW_EF_CONSTRUCTION")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			hnswEfConstruction = n
		}
	}
	hnswEf := m.HNSW_EF
	if v := strings.TrimSpace(os.Getenv("MILVUS_HNSW_EF")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			hnswEf = n
		}
	}
	return milvus.Config{
		Address:             addr,
		Username:            username,
		Password:            password,
		APIKey:              apiKey,
		EnableTLS:           m.EnableTLS,
		DBName:              dbName,
		Collection:          strings.TrimSpace(m.Collection),
		VectorDim:           m.VectorDim,
		MaxChunkIDLen:       m.MaxChunkIDLen,
		MaxDocIDLen:         maxDoc,
		ShardNum:            m.ShardNum,
		InsertBatch:         m.InsertBatch,
		MaxJobIDLen:         m.MaxJobIDLen,
		MaxTaskIDLen:        m.MaxTaskIDLen,
		MaxExtraInfoLen:     m.MaxExtraInfoLen,
		MaxTitleLen:         m.MaxTitleLen,
		MaxURLLen:           m.MaxURLLen,
		IndexType:           indexType,
		HNSW_M:              hnswM,
		HNSW_EfConstruction: hnswEfConstruction,
		HNSW_EF:             hnswEf,
	}
}

// LoadAPI reads and parses the API config file at path.
func LoadAPI(path string) (*API, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c API
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.HTTP.Addr) == "" {
		c.HTTP.Addr = ":8080"
	}
	return &c, nil
}
