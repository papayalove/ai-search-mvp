package ingestmeta

import (
	"fmt"
	"sort"
	"strings"

	storages3 "ai-search-v1/internal/storage/s3"
)

// ResolveS3EnqueueKeys 与 Worker 一致：合并 s3_uris 与 bucket+keys，去重保序。
func ResolveS3EnqueueKeys(bucket string, s3URIs, keys []string) (outBucket string, outKeys []string, err error) {
	bucket = strings.TrimSpace(bucket)
	var b string
	seen := map[string]struct{}{}
	for _, u := range s3URIs {
		buck, k, ok := storages3.ParseS3URI(u)
		if !ok {
			return "", nil, fmt.Errorf("invalid s3_uri %q", u)
		}
		if b == "" {
			b = buck
		} else if b != buck {
			return "", nil, fmt.Errorf("mixed buckets in one job not supported")
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		outKeys = append(outKeys, k)
	}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		outKeys = append(outKeys, k)
	}
	if b != "" {
		outBucket = b
	} else {
		outBucket = bucket
	}
	if outBucket == "" && len(outKeys) > 0 {
		return "", nil, fmt.Errorf("s3 bucket is required when using keys")
	}
	sort.Strings(outKeys)
	return outBucket, outKeys, nil
}
