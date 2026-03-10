package wdclient

import (
	"testing"
	"time"

	"GoBlob/goblob/core/types"
)

func TestVidCacheGetMiss(t *testing.T) {
	c := NewVidCache(10 * time.Minute)
	_, ok := c.Get(types.VolumeId(1))
	if ok {
		t.Fatal("expected cache miss on empty cache, got hit")
	}
}

func TestVidCacheSetGet(t *testing.T) {
	c := NewVidCache(10 * time.Minute)
	vid := types.VolumeId(42)
	locs := []Location{
		{Url: "localhost:8080", PublicUrl: "localhost:8080"},
		{Url: "localhost:8081", PublicUrl: "localhost:8081"},
	}

	c.Set(vid, locs)

	got, ok := c.Get(vid)
	if !ok {
		t.Fatal("expected cache hit after Set, got miss")
	}
	if len(got) != len(locs) {
		t.Fatalf("expected %d locations, got %d", len(locs), len(got))
	}
	for i, l := range got {
		if l.Url != locs[i].Url || l.PublicUrl != locs[i].PublicUrl {
			t.Errorf("location[%d] mismatch: got %+v, want %+v", i, l, locs[i])
		}
	}
}

func TestVidCacheExpiry(t *testing.T) {
	ttl := 50 * time.Millisecond
	c := NewVidCache(ttl)
	vid := types.VolumeId(7)
	locs := []Location{{Url: "localhost:9090"}}

	c.Set(vid, locs)

	// Should be present immediately.
	if _, ok := c.Get(vid); !ok {
		t.Fatal("expected hit immediately after Set")
	}

	// Wait for TTL to expire.
	time.Sleep(ttl + 10*time.Millisecond)

	if _, ok := c.Get(vid); ok {
		t.Fatal("expected cache miss after TTL expiry, got hit")
	}
}
