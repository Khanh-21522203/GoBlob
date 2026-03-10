package replication

import (
	"sync"

	"GoBlob/goblob/core/types"
)

// ReplicaLocations maps VolumeId → slice of server addresses holding replicas.
type ReplicaLocations struct {
	mu        sync.RWMutex
	locations map[types.VolumeId][]types.ServerAddress
}

func NewReplicaLocations() *ReplicaLocations {
	return &ReplicaLocations{
		locations: make(map[types.VolumeId][]types.ServerAddress),
	}
}

// Update replaces the replica list for a volume.
func (rl *ReplicaLocations) Update(vid types.VolumeId, addrs []types.ServerAddress) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.locations[vid] = addrs
}

// Get returns replica addresses for a volume (nil if not found).
func (rl *ReplicaLocations) Get(vid types.VolumeId) []types.ServerAddress {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.locations[vid]
}
