package pipeline

import "strings"

// InferSourceTypeFromExt 根据入库支持的文件扩展名给出 Milvus source_type 语义标签；无法识别时返回空字符串。
func InferSourceTypeFromExt(ext string) string {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext != "" && ext[0] != '.' {
		ext = "." + ext
	}
	switch ext {
	case ".ndjson", ".jsonl":
		return "ndjson"
	case ".json":
		return "json"
	case ".txt":
		return "txt"
	case ".md", ".markdown":
		return "md"
	default:
		return ""
	}
}

// EffectiveIngestSourceType 写入 Milvus 的 source_type：先按文件后缀推断；推断不出再用任务级 source_type；仍空则为 default。
func EffectiveIngestSourceType(ext, jobSourceType string) string {
	if v := InferSourceTypeFromExt(ext); v != "" {
		return v
	}
	if s := strings.TrimSpace(jobSourceType); s != "" {
		return s
	}
	return "default"
}
