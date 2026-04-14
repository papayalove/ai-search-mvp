package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const singletonLockTTL = 90 * time.Second

// ImporterSingletonLockKey 与队列 list key 绑定，同一队列全局只允许一个 importer 进程持锁。
func ImporterSingletonLockKey(queueListKey string) string {
	return "ingest:importer:singleton:v1:" + queueListKey
}

func importerLockToken() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	host, _ := os.Hostname()
	return fmt.Sprintf("%s:%d:%s", host, os.Getpid(), hex.EncodeToString(b))
}

var refreshImporterLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`)

var releaseImporterLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

// HoldImporterSingletonLock 获取单例锁；同一 queueListKey 上第二个进程会失败。
// 持锁期间会续约 TTL，直到调用 release（须进程退出时调用，通常 defer）。
func HoldImporterSingletonLock(ctx context.Context, c *redis.Client, queueListKey string) (release func(), err error) {
	if c == nil {
		return nil, fmt.Errorf("queue: nil redis client")
	}
	key := ImporterSingletonLockKey(queueListKey)
	tok := importerLockToken()
	ok, err := c.SetNX(ctx, key, tok, singletonLockTTL).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("redis lock %q already held — another cmd/importer is running for this queue (or stale lock; wait up to %s or unset IMPORTER_REQUIRE_SINGLETON_LOCK)", key, singletonLockTTL)
	}

	refreshCtx, stopRefresh := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(singletonLockTTL / 3)
		defer tick.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-tick.C:
				if e := refreshImporterLockScript.Run(context.Background(), c, []string{key}, tok, singletonLockTTL.Milliseconds()).Err(); e != nil {
					// 续约失败不退出进程；锁过期后另一进程可接管
					_ = e
				}
			}
		}
	}()

	var once sync.Once
	release = func() {
		once.Do(func() {
			stopRefresh()
			wg.Wait()
			_, _ = releaseImporterLockScript.Run(context.Background(), c, []string{key}, tok).Int()
		})
	}
	return release, nil
}
