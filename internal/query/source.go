package query

import "strings"

// ContentFetchSource 返回可用于 GET /v1/content 的源地址（http(s) 或 s3://）；否则空字符串。
func ContentFetchSource(milvusURL, urlOrDocID string) string {
	for _, u := range []string{strings.TrimSpace(milvusURL), strings.TrimSpace(urlOrDocID)} {
		lu := strings.ToLower(u)
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") || strings.HasPrefix(lu, "s3://") {
			return u
		}
	}
	return ""
}
