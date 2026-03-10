package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestRedisCachePutGetInvalidate(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()

	c, err := NewRedisCache(mr.Addr(), time.Minute)
	if err != nil {
		t.Fatalf("NewRedisCache: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	if err := c.Put(ctx, "fid", []byte("payload")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := c.Get(ctx, "fid")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("Get value=%q want payload", got)
	}

	if err := c.Invalidate(ctx, "fid"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := c.Get(ctx, "fid"); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("expected ErrCacheMiss, got %v", err)
	}
}
