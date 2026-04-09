package es

import (
	"fmt"
	"strings"
	"time"
)

// Config 连接 Elasticsearch（与 design 中 entity_postings_v1 一致）。
// 密码建议仅通过环境变量 ES_PASSWORD 注入（与 Milvus 一致用 LookupEnv，允许空密码）。
type Config struct {
	Addresses       []string
	Username        string
	Password        string
	Index           string
	RequestTimeout  time.Duration
	MaxIdleConns    int
	DisableCompress bool
}

// DefaultIndexName 与设计文档索引名一致。
const DefaultIndexName = "entity_postings_v1"

// Validate 校验启用连接时的必填项。
func (c Config) Validate() error {
	if len(c.Addresses) == 0 {
		return fmt.Errorf("es: no addresses")
	}
	for _, a := range c.Addresses {
		if strings.TrimSpace(a) == "" {
			return fmt.Errorf("es: empty address in list")
		}
	}
	if strings.TrimSpace(c.Index) == "" {
		return fmt.Errorf("es: index name is required")
	}
	return nil
}

// NormalizeAddress 去掉末尾斜杠，便于拼接路径。
func NormalizeAddress(u string) string {
	return strings.TrimRight(strings.TrimSpace(u), "/")
}
