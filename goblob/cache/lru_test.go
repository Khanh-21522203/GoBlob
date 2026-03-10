package cache

import (
	"context"
	"errors"
	"testing"
)

func TestLRUCachePutGetInvalidate(t *testing.T) {
	c := NewLRUCache(1024)
	ctx := context.Background()

	if err := c.Put(ctx, "a", []byte("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := c.Get(ctx, "a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("Get value=%q want %q", got, "hello")
	}

	if err := c.Invalidate(ctx, "a"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := c.Get(ctx, "a"); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("expected ErrCacheMiss, got %v", err)
	}
}

func TestLRUCacheEviction(t *testing.T) {
	c := NewLRUCache(5)
	ctx := context.Background()

	_ = c.Put(ctx, "a", []byte("aa"))
	_ = c.Put(ctx, "b", []byte("bb"))
	_ = c.Put(ctx, "c", []byte("cc"))

	if _, err := c.Get(ctx, "a"); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("expected key a to be evicted, err=%v", err)
	}
	if c.Len() != 2 {
		t.Fatalf("Len=%d want 2", c.Len())
	}
}

func TestLRUCacheRejectLargeItem(t *testing.T) {
	c := NewLRUCache(3)
	ctx := context.Background()
	if err := c.Put(ctx, "x", []byte("toolarge")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := c.Get(ctx, "x"); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("expected miss for oversized item, got %v", err)
	}
}
