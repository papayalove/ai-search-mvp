package config

import (
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	storages3 "ai-search-v1/internal/storage/s3"
)

const (
	defaultMultipartPayloadTTL = 3 * time.Hour
	defaultRemoteJobTTL        = 24 * time.Hour
)

// 入库队列 Redis 环境变量（优先）。旧名仍可读，便于迁移。
const (
	envRedisIngestURL              = "REDIS_INGEST_URL"
	envRedisIngestEnabled          = "REDIS_INGEST_ENABLED"
	envRedisIngestListKey        = "REDIS_INGEST_LIST_KEY"
	envRedisIngestPayloadTTL     = "REDIS_INGEST_PAYLOAD_TTL_SEC"
	envRedisIngestJobMetaTTL     = "REDIS_INGEST_JOB_META_TTL_SEC"
	envRedisIngestWorkerConc     = "REDIS_INGEST_WORKER_CONCURRENCY"
	envRedisIngestHost           = "REDIS_INGEST_HOST"
	envRedisIngestPort           = "REDIS_INGEST_PORT"
	envRedisIngestPassword       = "REDIS_INGEST_PASSWORD"
	envRedisIngestUsername       = "REDIS_INGEST_USERNAME"
	envRedisIngestDB             = "REDIS_INGEST_DB"
	envRedisIngestTLS            = "REDIS_INGEST_TLS"
	envLegacyQueueRedisURL       = "INGEST_QUEUE_REDIS_URL"
	envLegacyQueueEnabled        = "INGEST_QUEUE_ENABLED"
	envLegacyQueueListKey        = "INGEST_QUEUE_LIST_KEY"
	envLegacyMultipartPayloadTTL = "INGEST_MULTIPART_PAYLOAD_TTL_SEC"
	envLegacyRemoteJobTTL        = "INGEST_REMOTE_JOB_TTL_SEC"
	envLegacyWorkerConc          = "INGEST_WORKER_CONCURRENCY"
)

// IngestQueueFromEnv 入库队列与 S3 客户端相关配置（来自 .env / 环境变量）。
type IngestQueueFromEnv struct {
	RedisURL                  string
	Enabled                   bool
	MultipartPayloadTTL       time.Duration
	RemoteJobMetaTTL          time.Duration
	WorkerConcurrency         int
	QueueListKey              string
	S3                        storages3.Config
}

// LoadIngestQueueFromEnv 读取 REDIS_INGEST_*（及旧名 INGEST_QUEUE_* 等）与 S3；未解析出 Redis 连接时 Enabled 为 false。
// 连接来源优先级：1) REDIS_INGEST_URL / INGEST_QUEUE_REDIS_URL；2) REDIS_INGEST_HOST（+ PORT、PASSWORD、DB、TLS 等拼装为 redis URL）。
func LoadIngestQueueFromEnv() IngestQueueFromEnv {
	q := IngestQueueFromEnv{
		QueueListKey: getenvFirst(envRedisIngestListKey, envLegacyQueueListKey),
		S3:           storages3.LoadConfigFromEnv(),
	}
	if q.QueueListKey == "" {
		q.QueueListKey = "ingest:queue"
	}
	q.RedisURL = strings.TrimSpace(getenvFirst(envRedisIngestURL, envLegacyQueueRedisURL))
	if q.RedisURL == "" {
		q.RedisURL = redisIngestURLFromHostEnv()
	}
	if q.RedisURL == "" {
		q.Enabled = false
	} else {
		q.Enabled = parseBoolEnvFirst(true, envRedisIngestEnabled, envLegacyQueueEnabled)
	}
	if n, ok := parsePositiveIntSecFirst(envRedisIngestPayloadTTL, envLegacyMultipartPayloadTTL); ok {
		q.MultipartPayloadTTL = time.Duration(n) * time.Second
	}
	if q.MultipartPayloadTTL <= 0 {
		q.MultipartPayloadTTL = defaultMultipartPayloadTTL
	}
	if n, ok := parsePositiveIntSecFirst(envRedisIngestJobMetaTTL, envLegacyRemoteJobTTL); ok {
		q.RemoteJobMetaTTL = time.Duration(n) * time.Second
	}
	if q.RemoteJobMetaTTL <= 0 {
		q.RemoteJobMetaTTL = defaultRemoteJobTTL
	}
	if n, ok := parsePositiveIntSecFirst(envRedisIngestWorkerConc, envLegacyWorkerConc); ok {
		q.WorkerConcurrency = n
	}
	if q.WorkerConcurrency <= 0 {
		q.WorkerConcurrency = 1
	}
	return q
}

// redisIngestURLFromHostEnv 由 REDIS_INGEST_HOST、REDIS_INGEST_PORT 等拼装 go-redis 可解析的 URL；未配置 HOST 时返回空。
func redisIngestURLFromHostEnv() string {
	host := strings.TrimSpace(os.Getenv(envRedisIngestHost))
	if host == "" {
		return ""
	}
	port := strings.TrimSpace(os.Getenv(envRedisIngestPort))
	if port == "" {
		port = "6379"
	}
	db := strings.TrimSpace(os.Getenv(envRedisIngestDB))
	if db == "" {
		db = "0"
	}
	scheme := "redis"
	if parseBoolEnvFirst(false, envRedisIngestTLS) {
		scheme = "rediss"
	}
	u := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + db,
	}
	user := strings.TrimSpace(os.Getenv(envRedisIngestUsername))
	pass := strings.TrimSpace(os.Getenv(envRedisIngestPassword))
	if user != "" || pass != "" {
		u.User = url.UserPassword(user, pass)
	}
	return u.String()
}

func getenvFirst(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func parseBoolEnvFirst(defaultTrue bool, keys ...string) bool {
	for _, key := range keys {
		v, ok := os.LookupEnv(key)
		if !ok {
			continue
		}
		v = strings.ToLower(strings.TrimSpace(v))
		switch v {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		default:
			return defaultTrue
		}
	}
	return defaultTrue
}

func parsePositiveIntSecFirst(keys ...string) (int, bool) {
	for _, k := range keys {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n, true
		}
	}
	return 0, false
}
