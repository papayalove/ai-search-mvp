package util

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// StableChunkID 由 doc_id、page_no、chunk_no 生成全局稳定的 chunk 标识（与设计文档一致：对三者做稳定哈希）。
// 输出为 64 字符十六进制（SHA-256），不含分隔符碰撞风险。
func StableChunkID(docID string, pageNo, chunkNo int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", docID, pageNo, chunkNo)))
	return hex.EncodeToString(h[:])
}

// StableDocIDFromS3Object 将对象规范为 s3://bucket/key 后做 SHA-256 十六进制，用作无表单 doc_id 时的稳定文档 ID（与 RunPlain 派生 chunk_id 一致）。
func StableDocIDFromS3Object(bucket, key string) string {
	b := strings.TrimSpace(bucket)
	k := strings.TrimSpace(key)
	k = strings.TrimPrefix(k, "/")
	ref := "s3://" + b + "/" + k
	h := sha256.Sum256([]byte(ref))
	return hex.EncodeToString(h[:])
}
