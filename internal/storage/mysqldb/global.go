package mysqldb

import (
	"strings"
	"sync"
)

var (
	globalMu   sync.RWMutex
	globalRepo *IngestJobRepository
)

// Init 进程启动时调用一次：dsn 为空则跳过；非空则创建连接池并 Ping，失败返回错误。
// 已成功初始化后再次调用为无操作（幂等）。
func Init(dsn string) error {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil
	}
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalRepo != nil {
		return nil
	}
	r, err := Open(dsn)
	if err != nil {
		return err
	}
	globalRepo = r
	return nil
}

// Repo 返回 Init 成功后的仓库；未配置或未 Init 时为 nil。
func Repo() *IngestJobRepository {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalRepo
}

// CloseGlobal 释放全局连接（优雅退出时调用）；未初始化则无操作。
func CloseGlobal() error {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalRepo == nil {
		return nil
	}
	err := globalRepo.Close()
	globalRepo = nil
	return err
}
