// Package chunk 存放导入链路里的 chunk 记录、NDJSON 解析，以及递归字符分块（对齐 LangChain RecursiveCharacterTextSplitter）。
package chunk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ai-search-v1/pkg/util"
)

// TextChunkLine 表示 importer 输入里的一行 NDJSON。
type TextChunkLine struct {
	ChunkID    string `json:"chunk_id"`
	Text       string `json:"text"`
	DocID      string `json:"doc_id"`
	PageNo     int    `json:"page_no"`
	ChunkNo    int    `json:"chunk_no"`
	EntityKeys []string `json:"entity_keys"`
	SourceType string `json:"source_type"`
	Lang       string `json:"lang"`
	// 兼容旧字段 ts（毫秒）；若仅有 ts 则同时填 CreatedMs/UpdatedMs。
	TsLegacy int64 `json:"ts"`
	JobID    string `json:"job_id"`
	TaskID   string `json:"task_id"`
	// ExtraInfo 原始 JSON 对象（字典）。
	ExtraInfo json.RawMessage `json:"extra_info"`
	// CreatedMs/UpdatedMs：行内 Unix 毫秒；均为 0 且无 ts 时用解析时刻的当前时间。
	// 若需「入库时刻」而非「文件里的时间」，在 pipeline 侧启用 NDJSONRunOptions.UseServerIngestTime 或环境变量 INGEST_USE_SERVER_TIME。
	CreatedMs int64 `json:"created_time"`
	UpdatedMs int64 `json:"update_time"`
}

// ParseTextChunkLine 解析一行 JSON，校验必填字段并填默认值。
func ParseTextChunkLine(line []byte) (TextChunkLine, error) {
	line = trimBOMSpace(line)
	if len(line) == 0 {
		return TextChunkLine{}, fmt.Errorf("empty line")
	}
	var r TextChunkLine
	if err := json.Unmarshal(line, &r); err != nil {
		return TextChunkLine{}, err
	}
	r.ChunkID = strings.TrimSpace(r.ChunkID)
	r.Text = strings.TrimSpace(r.Text)
	r.DocID = strings.TrimSpace(r.DocID)
	r.SourceType = strings.TrimSpace(r.SourceType)
	r.Lang = strings.TrimSpace(r.Lang)
	r.JobID = strings.TrimSpace(r.JobID)
	r.TaskID = strings.TrimSpace(r.TaskID)
	if len(r.EntityKeys) > 0 {
		keys := make([]string, 0, len(r.EntityKeys))
		for _, k := range r.EntityKeys {
			k = strings.TrimSpace(k)
			if k != "" {
				keys = append(keys, k)
			}
		}
		r.EntityKeys = keys
	}
	if len(r.EntityKeys) > 0 && r.DocID == "" {
		return TextChunkLine{}, fmt.Errorf("doc_id is required when entity_keys is set")
	}
	if r.ChunkID == "" {
		if r.DocID == "" {
			return TextChunkLine{}, fmt.Errorf("doc_id is required when chunk_id is omitted (chunk_id is derived as stable hash of doc_id, page_no, chunk_no)")
		}
		r.ChunkID = util.StableChunkID(r.DocID, r.PageNo, r.ChunkNo)
	}
	if r.Text == "" {
		return TextChunkLine{}, fmt.Errorf("chunk_id %q: text is required", r.ChunkID)
	}
	if r.SourceType == "" {
		r.SourceType = "default"
	}
	if r.Lang == "" {
		r.Lang = "und"
	}
	now := time.Now().UnixMilli()
	if r.CreatedMs == 0 && r.UpdatedMs == 0 && r.TsLegacy != 0 {
		r.CreatedMs = r.TsLegacy
		r.UpdatedMs = r.TsLegacy
	}
	if r.CreatedMs == 0 {
		r.CreatedMs = now
	}
	if r.UpdatedMs == 0 {
		r.UpdatedMs = r.CreatedMs
	}
	if len(bytes.TrimSpace(r.ExtraInfo)) == 0 {
		r.ExtraInfo = nil
	}
	return r, nil
}

// SplitRecord 将一条「长 text」按 RecursiveChunkOptions 切成多块。
func SplitRecord(rec TextChunkLine, opts RecursiveChunkOptions) ([]TextChunkLine, error) {
	parts, err := ChunkTextRecursively(rec.Text, opts)
	if err != nil {
		return nil, err
	}
	out := make([]TextChunkLine, 0, len(parts))
	for i, txt := range parts {
		var id string
		switch {
		case len(parts) == 1:
			id = rec.ChunkID
		case rec.DocID != "":
			id = util.StableChunkID(rec.DocID, rec.PageNo, rec.ChunkNo+i)
		default:
			id = fmt.Sprintf("%s#%d", rec.ChunkID, i)
		}
		out = append(out, TextChunkLine{
			ChunkID:    id,
			Text:       txt,
			DocID:      rec.DocID,
			PageNo:     rec.PageNo,
			ChunkNo:    rec.ChunkNo + i,
			EntityKeys: rec.EntityKeys,
			SourceType: rec.SourceType,
			Lang:       rec.Lang,
			TsLegacy:   rec.TsLegacy,
			JobID:      rec.JobID,
			TaskID:     rec.TaskID,
			ExtraInfo:  bytes.Clone(rec.ExtraInfo),
			CreatedMs:  rec.CreatedMs,
			UpdatedMs:  rec.UpdatedMs,
		})
	}
	return out, nil
}

func trimBOMSpace(b []byte) []byte {
	b = trimLeftSpace(b)
	if len(b) >= 3 && b[0] == 0xef && b[1] == 0xbb && b[2] == 0xbf {
		b = b[3:]
		b = trimLeftSpace(b)
	}
	return b
}

func trimLeftSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	return b
}
