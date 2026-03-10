package cache

import (
	"context"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// RedisCache stores cache entries in Redis with TTL.
type RedisCache struct {
	client *redis.Client
	ttl    time.Duration
	prefix string
}

func NewRedisCache(addr string, ttl time.Duration) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{Addr: addr})
	return &RedisCache{client: client, ttl: ttl, prefix: "cache:"}, nil
}

func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := c.client.Get(ctx, c.prefix+key).Bytes()
	if err == redis.Nil {
		return nil, ErrCacheMiss
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (c *RedisCache) Put(ctx context.Context, key string, value []byte) error {
	return c.client.Set(ctx, c.prefix+key, value, c.ttl).Err()
}

func (c *RedisCache) Invalidate(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.prefix+key).Err()
}

func (c *RedisCache) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}
