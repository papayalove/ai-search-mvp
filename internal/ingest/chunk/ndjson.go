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
	Title      string `json:"title,omitempty"`
	URL        string `json:"url,omitempty"`
	PageNo     int    `json:"page_no"`
	// Offset 为当前 page 内 chunk 文本起始的字节偏移（UTF-8 字节）；切分后由 SplitRecord 写入。
	Offset int64 `json:"offset"`
	ChunkNo    int    `json:"chunk_no"`
	EntityKeys []string `json:"entity_keys"`
	SourceType string `json:"source_type"`
	Lang       string `json:"lang"`
	// TsLegacy 仅反序列化兼容；写入 Milvus/ES 前由 pipeline 统一改为入库时刻。
	TsLegacy int64 `json:"ts"`
	JobID    string `json:"job_id"`
	TaskID   string `json:"task_id"`
	// ExtraInfo 原始 JSON 对象（字典）。
	ExtraInfo json.RawMessage `json:"extra_info"`
	// CreatedMs/UpdatedMs：JSON 可带 created_time/update_time，但入库时由 pipeline 覆盖为当前时刻（见 RunNDJSON）。
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
	r.Title = strings.TrimSpace(r.Title)
	r.URL = strings.TrimSpace(r.URL)
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
	// source_type 空表示行内未写；RunNDJSON 再按文件后缀与 Job 级 source_type 补齐。
	if r.Lang == "" {
		r.Lang = "und"
	}
	// 解析阶段占位；真实写入前 RunNDJSON 会再次设为入库时刻。行内 ts/时间字段不参与存储。
	r.TsLegacy = 0
	now := time.Now().UnixMilli()
	r.CreatedMs = now
	r.UpdatedMs = now
	if len(bytes.TrimSpace(r.ExtraInfo)) == 0 {
		r.ExtraInfo = nil
	}
	if r.PageNo < 0 {
		r.PageNo = 0
	}
	if r.Offset < 0 {
		r.Offset = 0
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
	full := []byte(rec.Text)
	// 下一块可能在「上一块起始之后」的任意位置开始（chunk_overlap>0 时与上一块尾部重叠），
	// 不能假设各块在原文中首尾相接。
	prevStart := -1
	for i, txt := range parts {
		t := []byte(txt)
		if len(t) == 0 {
			return nil, fmt.Errorf("chunk split: empty text part %d", i)
		}
		minB := 0
		if i > 0 {
			minB = prevStart + 1
		}
		if minB > len(full)-len(t) {
			return nil, fmt.Errorf("chunk split: part %d not aligned with parent text", i)
		}
		idx := bytes.Index(full[minB:], t)
		if idx < 0 {
			return nil, fmt.Errorf("chunk split: part %d not aligned with parent text", i)
		}
		absByte := minB + idx
		prevStart = absByte
		byteOff := rec.Offset + int64(absByte)
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
			Title:      rec.Title,
			URL:        rec.URL,
			PageNo:     rec.PageNo,
			Offset:     byteOff,
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
