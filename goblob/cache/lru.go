package cache

import (
	"container/list"
	"context"
	"sync"
)

type lruEntry struct {
	key   string
	value []byte
}

// LRUCache is a memory-bounded LRU cache for blob payloads.
type LRUCache struct {
	mu       sync.Mutex
	capacity int64
	used     int64
	ll       *list.List
	cache    map[string]*list.Element
}

func NewLRUCache(capacity int64) *LRUCache {
	if capacity < 0 {
		capacity = 0
	}
	return &LRUCache{
		capacity: capacity,
		ll:       list.New(),
		cache:    make(map[string]*list.Element),
	}
}

func (c *LRUCache) Get(ctx context.Context, key string) ([]byte, error) {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, hit := c.cache[key]
	if !hit {
		return nil, ErrCacheMiss
	}
	c.ll.MoveToFront(elem)
	value := elem.Value.(*lruEntry).value
	out := make([]byte, len(value))
	copy(out, value)
	return out, nil
}

func (c *LRUCache) Put(ctx context.Context, key string, value []byte) error {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.capacity == 0 {
		return nil
	}
	v := make([]byte, len(value))
	copy(v, value)
	valueSize := int64(len(v))
	if valueSize > c.capacity {
		// Single value cannot fit; evict existing key if present and skip storing.
		if elem, ok := c.cache[key]; ok {
			c.removeElement(elem)
		}
		return nil
	}

	if elem, ok := c.cache[key]; ok {
		ent := elem.Value.(*lruEntry)
		c.used -= int64(len(ent.value))
		ent.value = v
		c.used += valueSize
		c.ll.MoveToFront(elem)
	} else {
		elem := c.ll.PushFront(&lruEntry{key: key, value: v})
		c.cache[key] = elem
		c.used += valueSize
	}

	for c.used > c.capacity {
		c.removeOldest()
	}
	return nil
}

func (c *LRUCache) Invalidate(ctx context.Context, key string) error {
	_ = ctx
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.cache[key]; ok {
		c.removeElement(elem)
	}
	return nil
}

func (c *LRUCache) removeOldest() {
	elem := c.ll.Back()
	if elem == nil {
		return
	}
	c.removeElement(elem)
}

func (c *LRUCache) removeElement(elem *list.Element) {
	if elem == nil {
		return
	}
	c.ll.Remove(elem)
	ent := elem.Value.(*lruEntry)
	delete(c.cache, ent.key)
	c.used -= int64(len(ent.value))
	if c.used < 0 {
		c.used = 0
	}
}

func (c *LRUCache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

func (c *LRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
