package wdclient

import (
	"sync"
	"time"

	"GoBlob/goblob/core/types"
)

// Location represents a volume server location.
type Location struct {
	Url       string
	PublicUrl string
}

type vidEntry struct {
	locations []Location
	expiresAt time.Time
}

// VidCache is a thread-safe TTL cache mapping VolumeId to a list of Locations.
type VidCache struct {
	mu    sync.RWMutex
	cache map[types.VolumeId]*vidEntry
	ttl   time.Duration
}

// NewVidCache creates a new VidCache with the given TTL.
func NewVidCache(ttl time.Duration) *VidCache {
	return &VidCache{
		cache: make(map[types.VolumeId]*vidEntry),
		ttl:   ttl,
	}
}

// Get returns cached locations for vid. Returns (nil, false) if not found or expired.
func (c *VidCache) Get(vid types.VolumeId) ([]Location, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.cache[vid]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.locations, true
}

// Set stores locations for vid with the configured TTL.
func (c *VidCache) Set(vid types.VolumeId, locs []Location) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[vid] = &vidEntry{
		locations: locs,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Invalidate removes the cache entry for vid.
func (c *VidCache) Invalidate(vid types.VolumeId) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.cache, vid)
}
