package recall

import (
	"strings"

	"ai-search-v1/internal/storage/es"
)

// EntityKeysFromQuery MVP：按空白分词，每段 NormalizeEntityKey，去重保序。
func EntityKeysFromQuery(query string) []string {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	raw := strings.Fields(q)
	seen := make(map[string]struct{})
	var out []string
	for _, t := range raw {
		k := es.NormalizeEntityKey(t)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}
