package recall

import "strings"

// MergeHitsDedupeSequential 按列表顺序合并多路命中，按 chunk_id 去重；maxTotal>0 时截断长度。
func MergeHitsDedupeSequential(lists [][]Hit, maxTotal int) []Hit {
	seen := make(map[string]struct{})
	out := make([]Hit, 0, 64)
	for _, list := range lists {
		for i := range list {
			h := list[i]
			id := strings.TrimSpace(h.ChunkID)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, h)
			if maxTotal > 0 && len(out) >= maxTotal {
				return out
			}
		}
	}
	return out
}
