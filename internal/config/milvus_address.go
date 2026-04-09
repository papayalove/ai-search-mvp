package config

import "strings"

// NormalizeMilvusAddress strips tcp/grpc/http(s) scheme and path so Milvus Go SDK gets "host:port".
func NormalizeMilvusAddress(s string) string {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, p := range []string{"tcp://", "grpc://", "http://", "https://"} {
		if strings.HasPrefix(lower, p) {
			s = s[len(p):]
			break
		}
	}
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
