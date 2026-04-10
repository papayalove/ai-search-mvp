package ingestmeta

import (
	"github.com/google/uuid"
)

// S3TaskID 由 job_id 与对象键确定性生成，便于仅 prefix 入队时 Worker list 后与占位一致。
func S3TaskID(jobID, objectKey string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(jobID+"\x00"+objectKey)).String()
}
