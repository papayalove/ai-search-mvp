package milvus

import (
	"context"
	"sync"
)

var (
	mu       sync.Mutex
	instance *Repository
)

// Get returns the package-level singleton repository.
// If it does not exist yet, it connects with cfg and caches the result.
// If it already exists, cfg is ignored and the existing instance is returned.
func Get(ctx context.Context, cfg Config) (*Repository, error) {
	mu.Lock()
	defer mu.Unlock()
	if instance != nil {
		return instance, nil
	}
	repo, err := NewRepository(ctx, cfg)
	if err != nil {
		return nil, err
	}
	instance = repo
	return instance, nil
}

// Init is a convenience wrapper: same as Get but discards the *Repository and only returns an error.
func Init(ctx context.Context, cfg Config) error {
	_, err := Get(ctx, cfg)
	return err
}

// Close closes the singleton connection and clears the cached instance.
func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if instance == nil {
		return nil
	}
	err := instance.Close()
	instance = nil
	return err
}
