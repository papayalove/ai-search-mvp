package config

import (
	"os"
	"strings"
)

const (
	defaultIngestTaskESIndex = "agentic_search_ingest_task_v1"
)

// IngestMetaFromEnv 入库 Job/Task 元数据（MySQL + ES Task 索引）。
type IngestMetaFromEnv struct {
	Enabled     bool
	MySQLDSN  string
	TaskESIndex string
}

// LoadIngestMetaFromEnv INGEST_META_ENABLED=true 且 MYSQL_DSN 非空时启用。
func LoadIngestMetaFromEnv() IngestMetaFromEnv {
	en := strings.EqualFold(strings.TrimSpace(os.Getenv("INGEST_META_ENABLED")), "true") ||
		strings.TrimSpace(os.Getenv("INGEST_META_ENABLED")) == "1"
	dsn := strings.TrimSpace(os.Getenv("MYSQL_DSN"))
	idx := strings.TrimSpace(os.Getenv("INGEST_ES_TASK_INDEX"))
	if idx == "" {
		idx = defaultIngestTaskESIndex
	}
	return IngestMetaFromEnv{
		Enabled:     en && dsn != "",
		MySQLDSN:    dsn,
		TaskESIndex: idx,
	}
}
