package config

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"ai-search-v1/internal/model/rewrite"
)

const (
	defaultRewriteBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	defaultRewriteModel   = "qwen-turbo"
)

// LoadRewriterFromEnv 按环境变量构造 Rewriter；未启用或缺少密钥时返回 nil（调用方走单路召回）。
//
//	REWRITE_ENABLED=true|1 时启用
//	DASHSCOPE_API_KEY 或 REWRITE_API_KEY（二选一，前者优先）
//	REWRITE_BASE_URL 默认阿里云 compatible-mode 根 URL
//	REWRITE_MODEL 默认 qwen-turbo
//	REWRITE_TIMEOUT_SEC 可选，默认 60
func LoadRewriterFromEnv() rewrite.Rewriter {
	en := envTruthy(os.Getenv("REWRITE_ENABLED"))
	if !en {
		return nil
	}
	key := strings.TrimSpace(os.Getenv("DASHSCOPE_API_KEY"))
	if key == "" {
		key = strings.TrimSpace(os.Getenv("REWRITE_API_KEY"))
	}
	if key == "" {
		log.Print("rewrite: REWRITE_ENABLED but DASHSCOPE_API_KEY / REWRITE_API_KEY empty; rewriter disabled")
		return nil
	}
	base := strings.TrimSpace(os.Getenv("REWRITE_BASE_URL"))
	if base == "" {
		base = defaultRewriteBaseURL
	}
	model := strings.TrimSpace(os.Getenv("REWRITE_MODEL"))
	if model == "" {
		model = defaultRewriteModel
	}
	sec := 60
	if v := strings.TrimSpace(os.Getenv("REWRITE_TIMEOUT_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			sec = n
		}
	}
	temp := 0.3
	if v := strings.TrimSpace(os.Getenv("REWRITE_TEMPERATURE")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 2 {
			temp = f
		}
	}
	maxTok := 512
	if v := strings.TrimSpace(os.Getenv("REWRITE_MAX_TOKENS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxTok = n
		}
	}
	log.Printf("rewrite: enabled (base_url=%s model=%s timeout=%ds)", base, model, sec)
	return &rewrite.ChatClient{
		HTTPClient:  &http.Client{Timeout: time.Duration(sec) * time.Second},
		BaseURL:     base,
		APIKey:      key,
		Model:       model,
		Temperature: temp,
		MaxTokens:   maxTok,
	}
}

func envTruthy(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "1" || s == "true" || s == "yes" || s == "on"
}
