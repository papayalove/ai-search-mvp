package es

import "time"

// ChunkEntityDoc 每个 chunk_id 一条 ES 文档，entity_keys 为 keyword 数组。
type ChunkEntityDoc struct {
	ChunkID     string
	EntityKeys  []string
	DocID       string
	SourceType  string
	Lang        string
	JobID       string
	TaskID      string
	ExtraInfo   map[string]any
	CreatedTime time.Time
	UpdatedTime time.Time
	Offset  int64 // 当前 page 内字节起始
	PageNo  int64 // 页号，默认 0
}

// EntityRecallHit 实体召回单条命中。
type EntityRecallHit struct {
	ChunkID     string
	EntityKey   string
	DocID       string
	SourceType  string
	Lang        string
	JobID       string
	TaskID      string
	Score       float64
	CreatedTime time.Time
	UpdatedTime time.Time
	Offset  int64
	PageNo  int64
}
