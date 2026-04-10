package config

import (
	"os"
	"strings"
)

// LoadIngestUseServerTimeFromEnv 为 true 时，NDJSON 入库忽略行内 created_time、update_time、ts，
// 写入 Milvus/ES 的统一使用入库时刻的 Unix 毫秒（UTC）。
//
// 环境变量：INGEST_USE_SERVER_TIME=true|1|yes|on
func LoadIngestUseServerTimeFromEnv() bool {
	s := strings.TrimSpace(strings.ToLower(os.Getenv("INGEST_USE_SERVER_TIME")))
	switch s {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
