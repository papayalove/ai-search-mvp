package s3

import (
	"os"
	"strings"
)

// Config 应用层 S3 选项（endpoint 等）；凭证与 region 走 AWS 默认链。
type Config struct {
	Endpoint string // 可选，如 MinIO
	Region   string // 可选覆盖 AWS_REGION
}

// LoadConfigFromEnv 读取 S3_ENDPOINT、AWS_REGION（仅显式项）。
func LoadConfigFromEnv() Config {
	return Config{
		Endpoint: strings.TrimSpace(os.Getenv("S3_ENDPOINT")),
		Region:   strings.TrimSpace(os.Getenv("AWS_REGION")),
	}
}
