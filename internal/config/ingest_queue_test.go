package config

import (
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestRedisIngestURLFromHostEnv(t *testing.T) {
	t.Setenv("REDIS_INGEST_HOST", "127.0.0.1")
	t.Setenv("REDIS_INGEST_PORT", "6380")
	t.Setenv("REDIS_INGEST_PASSWORD", "p@ss:w#rd")
	t.Setenv("REDIS_INGEST_DB", "2")
	t.Setenv("REDIS_INGEST_TLS", "0")

	got := redisIngestURLFromHostEnv()
	opt, err := redis.ParseURL(got)
	if err != nil {
		t.Fatalf("ParseURL %q: %v", got, err)
	}
	if opt.Addr != "127.0.0.1:6380" {
		t.Fatalf("addr: got %q", opt.Addr)
	}
	if opt.Password != "p@ss:w#rd" {
		t.Fatalf("password not preserved")
	}
	if opt.DB != 2 {
		t.Fatalf("db: got %d", opt.DB)
	}
}

func TestRedisIngestURLFromHostEnv_Empty(t *testing.T) {
	t.Setenv("REDIS_INGEST_HOST", "")
	if s := redisIngestURLFromHostEnv(); s != "" {
		t.Fatalf("want empty, got %q", s)
	}
}

func TestRedisIngestURLFromHostEnv_TLS(t *testing.T) {
	t.Setenv("REDIS_INGEST_HOST", "cache.example.com")
	t.Setenv("REDIS_INGEST_PORT", "6379")
	t.Setenv("REDIS_INGEST_TLS", "true")

	got := redisIngestURLFromHostEnv()
	if !strings.HasPrefix(got, "rediss://") {
		t.Fatalf("want rediss scheme, got %q", got)
	}
}

func TestLoadIngestQueueFromEnv_URLPrecedenceOverHost(t *testing.T) {
	t.Setenv("REDIS_INGEST_URL", "redis://explicit:6379/0")
	t.Setenv("REDIS_INGEST_HOST", "ignored")
	q := LoadIngestQueueFromEnv()
	if q.RedisURL != "redis://explicit:6379/0" {
		t.Fatalf("got %q", q.RedisURL)
	}
}
