package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrDequeueIdle 表示 BRPop 在 timeout 内未等到任务（非错误，Worker 应继续循环以便响应 ctx 取消）。
var ErrDequeueIdle = errors.New("queue: dequeue idle timeout")

// RedisBroker 入库队列（Redis List）。
type RedisBroker struct {
	Client  *redis.Client
	ListKey string
}

// NewRedisBroker 由 Redis URL 创建客户端。
func NewRedisBroker(redisURL, listKey string) (*RedisBroker, error) {
	if redisURL == "" {
		return nil, fmt.Errorf("queue: empty redis url")
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("queue: parse redis url: %w", err)
	}
	c := redis.NewClient(opt)
	if listKey == "" {
		listKey = "ingest:queue"
	}
	return &RedisBroker{Client: c, ListKey: listKey}, nil
}

// Close 关闭连接。
func (b *RedisBroker) Close() error {
	if b == nil || b.Client == nil {
		return nil
	}
	return b.Client.Close()
}

// Ping 检测连通性。
func (b *RedisBroker) Ping(ctx context.Context) error {
	if b == nil || b.Client == nil {
		return fmt.Errorf("queue: nil broker")
	}
	return b.Client.Ping(ctx).Err()
}

// SetPayload 写入 multipart 正文并设置 TTL。
func (b *RedisBroker) SetPayload(ctx context.Context, key string, body []byte, ttl time.Duration) error {
	return b.Client.Set(ctx, key, body, ttl).Err()
}

// GetPayload 读取正文。
func (b *RedisBroker) GetPayload(ctx context.Context, key string) ([]byte, error) {
	return b.Client.Get(ctx, key).Bytes()
}

// DelPayload 处理完后删除正文。
func (b *RedisBroker) DelPayload(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return b.Client.Del(ctx, keys...).Err()
}

// Enqueue 将 Job 推入队列左侧；metaTTL>0 时写入 ingest:j:{id} 占位并设过期。
func (b *RedisBroker) Enqueue(ctx context.Context, j Job, metaTTL time.Duration) error {
	raw, err := j.Marshal()
	if err != nil {
		return err
	}
	if metaTTL > 0 {
		metaKey := JobMetaRedisKey(j.JobID)
		if err := b.Client.HSet(ctx, metaKey, "status", "queued", "kind", j.PayloadKind).Err(); err != nil {
			return err
		}
		if err := b.Client.Expire(ctx, metaKey, metaTTL).Err(); err != nil {
			return err
		}
	}
	return b.Client.LPush(ctx, b.ListKey, raw).Err()
}

// Dequeue 阻塞右侧弹出一条任务。timeout<=0 时为无限等待（不推荐 Worker 使用：Windows 上 Ctrl+C 可能长时间无法打断）。
// timeout>0 时若在时限内无任务则返回 ErrDequeueIdle。
func (b *RedisBroker) Dequeue(ctx context.Context, timeout time.Duration) (Job, error) {
	var sl []string
	var err error
	if timeout <= 0 {
		sl, err = b.Client.BRPop(ctx, 0, b.ListKey).Result()
	} else {
		sl, err = b.Client.BRPop(ctx, timeout, b.ListKey).Result()
	}
	if err != nil {
		if errors.Is(err, redis.Nil) || err == redis.Nil {
			return Job{}, ErrDequeueIdle
		}
		return Job{}, err
	}
	if len(sl) != 2 {
		return Job{}, fmt.Errorf("queue: unexpected brpop result")
	}
	return UnmarshalJob([]byte(sl[1]))
}

// SetJobStatus 更新元数据 hash（需 key 仍存在）。
func (b *RedisBroker) SetJobStatus(ctx context.Context, jobID, status, errMsg string) error {
	key := JobMetaRedisKey(jobID)
	if errMsg != "" {
		return b.Client.HSet(ctx, key, "status", status, "error", errMsg).Err()
	}
	return b.Client.HSet(ctx, key, "status", status).Err()
}
