package s3

import (
	"os"
	"strings"
)

const defaultSDKRegion = "us-east-1"

// Config 应用层 S3 选项（endpoint 等）；凭证与 region 走 AWS 默认链。
type Config struct {
	Endpoint string // 可选，如 MinIO
	Region   string // 可选；来自 AWS_REGION，空则见 EffectiveRegion
	// UsePathStyle path 式寻址（等价 boto addressing_style=path / endpoint URL 含 bucket 在 path）。未显式设置 S3_ADDRESSING_STYLE 且配置了 S3_ENDPOINT 时默认为 true。
	UsePathStyle bool
}

// EffectiveRegion 供 AWS SDK 使用的非空 region。未配置 AWS_REGION 时用占位，满足 SDK 要求、可不配 region 先试；
// 若公有云签名校验失败，再在环境中设置与云厂商一致的 AWS_REGION。
func EffectiveRegion(c Config) string {
	if s := strings.TrimSpace(c.Region); s != "" {
		return s
	}
	return defaultSDKRegion
}

const envS3AddressingStyle = "S3_ADDRESSING_STYLE"

// usePathStyleFromEnv 解析 S3_ADDRESSING_STYLE：path / virtual；空则「有自定义 endpoint → path」否则 false。
func usePathStyleFromEnv(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	s := strings.TrimSpace(strings.ToLower(os.Getenv(envS3AddressingStyle)))
	switch s {
	case "path", "true", "1", "on", "yes":
		return true
	case "virtual", "false", "0", "off", "no":
		return false
	default:
		return endpoint != ""
	}
}

// LoadConfigFromEnv 读取 S3_ENDPOINT、AWS_REGION、S3_ADDRESSING_STYLE（未设 region 时由 EffectiveRegion 在客户端侧补默认）。
func LoadConfigFromEnv() Config {
	endpoint := strings.TrimSpace(os.Getenv("S3_ENDPOINT"))
	return Config{
		Endpoint:     endpoint,
		Region:       strings.TrimSpace(os.Getenv("AWS_REGION")),
		UsePathStyle: usePathStyleFromEnv(endpoint),
	}
}
