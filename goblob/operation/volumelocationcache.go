package operation

import (
	"sync"
	"time"

	"GoBlob/goblob/core/types"
)

const defaultVolumeLocationCacheTTL = 10 * time.Minute

type locationEntry struct {
	servers   []VolumeLocation
	expiresAt time.Time
}

// VolumeLocationCache stores volume lookups with TTL.
type VolumeLocationCache struct {
	locations map[types.VolumeId]*locationEntry
	mu        sync.RWMutex
	ttl       time.Duration
}

func NewVolumeLocationCache(ttl time.Duration) *VolumeLocationCache {
	if ttl <= 0 {
		ttl = defaultVolumeLocationCacheTTL
	}
	return &VolumeLocationCache{
		locations: make(map[types.VolumeId]*locationEntry),
		ttl:       ttl,
	}
}

func (c *VolumeLocationCache) Get(vid types.VolumeId) ([]VolumeLocation, bool) {
	c.mu.RLock()
	entry, ok := c.locations[vid]
	c.mu.RUnlock()
	if !ok || entry == nil {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		c.Invalidate(vid)
		return nil, false
	}
	out := make([]VolumeLocation, len(entry.servers))
	copy(out, entry.servers)
	return out, true
}

func (c *VolumeLocationCache) Set(vid types.VolumeId, locs []VolumeLocation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	copied := make([]VolumeLocation, len(locs))
	copy(copied, locs)
	c.locations[vid] = &locationEntry{
		servers:   copied,
		expiresAt: time.Now().Add(c.ttl),
	}
}

func (c *VolumeLocationCache) Invalidate(vid types.VolumeId) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.locations, vid)
}
