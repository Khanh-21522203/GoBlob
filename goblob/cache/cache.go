package cache

import (
	"context"
	"errors"
)

var ErrCacheMiss = errors.New("cache miss")

// Cache is the distributed cache contract used by higher-level services.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, value []byte) error
	Invalidate(ctx context.Context, key string) error
}
