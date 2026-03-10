package topology

import (
	"context"
	"fmt"
	"sync"

	"GoBlob/goblob/core/types"
)

// VolumeGrowth manages automatic volume creation based on capacity thresholds.
type VolumeGrowth struct {
	topology *Topology
	mu       sync.RWMutex
}

// NewVolumeGrowth creates a new VolumeGrowth instance.
func NewVolumeGrowth(topology *Topology) *VolumeGrowth {
	return &VolumeGrowth{
		topology: topology,
	}
}

// CheckAndGrow checks if volumes need to be grown and creates new volumes if needed.
// Returns the number of volumes created.
func (vg *VolumeGrowth) CheckAndGrow(ctx context.Context, collection string, replicaPlacement types.ReplicaPlacement, ttl string, diskType types.DiskType, count int) (int, error) {
	vg.mu.Lock()
	defer vg.mu.Unlock()

	volumeLayout := vg.topology.GetVolumeLayout(collection, replicaPlacement.String(), ttl, diskType)
	if volumeLayout == nil {
		return 0, fmt.Errorf("volume layout not found")
	}

	// Check if we need to grow (trigger at 90% threshold)
	shouldGrow := vg.shouldGrowVolumes(volumeLayout)
	if !shouldGrow {
		return 0, nil
	}

	// Find candidate data nodes for new volumes
	candidates, err := vg.topology.AllocateVolumeCandidates(collection, replicaPlacement, ttl, diskType)
	if err != nil {
		return 0, fmt.Errorf("failed to find candidates: %w", err)
	}

	if len(candidates) < replicaPlacement.TotalCopies() {
		return 0, fmt.Errorf("not enough candidates: need %d, found %d", replicaPlacement.TotalCopies(), len(candidates))
	}

	// Allocate volumes with capacity reservations
	created := 0
	reservations := make([]*CapacityReservation, 0, len(candidates))

	// Reserve capacity on candidates
	for _, candidate := range candidates {
		reservation := vg.reserveCapacity(candidate, diskType)
		if reservation != nil {
			reservations = append(reservations, reservation)
		}
	}

	// Clean up reservations on exit
	defer func() {
		for _, res := range reservations {
			if res != nil && !res.Used {
				vg.releaseReservation(res)
			}
		}
	}()

	// Create volumes
	for i := 0; i < count; i++ {
		// Select nodes that satisfy replica placement constraints
		selectedNodes := vg.selectNodesForReplica(candidates, replicaPlacement, reservations)
		if len(selectedNodes) < replicaPlacement.TotalCopies() {
			return created, fmt.Errorf("not enough nodes with capacity for volume %d", i)
		}

		// Generate volume ID (would come from sequencer in real implementation)
		volumeId := uint32(0) // TODO: Get from sequencer

		// Allocate volume on each selected node
		success := false
		for j, node := range selectedNodes {
			res := reservations[j]
			if err := vg.allocateVolumeOnNode(ctx, node, volumeId, collection, replicaPlacement, ttl, diskType); err != nil {
				// Rollback: deallocate volumes from previous nodes
				for k := 0; k < j; k++ {
					vg.deallocateVolumeOnNode(ctx, selectedNodes[k], volumeId)
				}
				success = false
				break
			}
			success = true
			res.Used = true
		}

		if success {
			created++
		}
	}

	return created, nil
}

// shouldGrowVolumes returns true if the writable volume ratio falls below 90%.
// Per spec: trigger growth when writableCount < 0.9 * totalCount.
func (vg *VolumeGrowth) shouldGrowVolumes(volumeLayout *VolumeLayout) bool {
	writableCount := volumeLayout.GetWritableVolumeCount()
	totalCount := volumeLayout.GetTotalVolumeCount()

	if totalCount == 0 {
		return true
	}

	writableRatio := float64(writableCount) / float64(totalCount)
	return writableRatio < 0.9
}

// CapacityReservation represents a reserved slot for a new volume.
type CapacityReservation struct {
	DataNode *DataNode
	DiskType types.DiskType
	Used     bool
}

// reserveCapacity reserves a volume slot on a data node.
func (vg *VolumeGrowth) reserveCapacity(dataNode *DataNode, diskType types.DiskType) *CapacityReservation {
	disk := dataNode.GetDisk(diskType)
	if disk == nil || disk.IsFull() {
		return nil
	}

	// Create reservation (doesn't actually allocate yet)
	return &CapacityReservation{
		DataNode: dataNode,
		DiskType: diskType,
		Used:     false,
	}
}

// releaseReservation releases a capacity reservation.
func (vg *VolumeGrowth) releaseReservation(reservation *CapacityReservation) {
	if reservation == nil || reservation.Used {
		return
	}
	// Nothing to do - the reservation was just a placeholder
}

// selectNodesForReplica selects nodes for volume replication based on placement constraints.
func (vg *VolumeGrowth) selectNodesForReplica(candidates []*DataNode, replicaPlacement types.ReplicaPlacement, reservations []*CapacityReservation) []*DataNode {
	var selected []*DataNode

	// We need to satisfy: different data centers, different racks, same rack
	dcNeeded := int(replicaPlacement.DifferentDataCenterCount) + 1
	rackNeeded := int(replicaPlacement.DifferentRackCount) + 1

	// Track used DCs
	usedDCs := make(map[string]bool)

	// First, try to place across different data centers
	for i := 0; i < dcNeeded && i < len(candidates); i++ {
		node := candidates[i]
		dc := node.GetDataCenter()

		if !usedDCs[dc] {
			selected = append(selected, node)
			usedDCs[dc] = true
		}
	}

	// If we still need more nodes, try different racks within same DC
	// (This would need more sophisticated logic for real multi-DC placement)
	for len(selected) < dcNeeded+rackNeeded && len(selected) < len(candidates) {
		node := candidates[len(selected)]
		selected = append(selected, node)
	}

	// Finally, add nodes from same rack if needed
	for len(selected) < replicaPlacement.TotalCopies() && len(selected) < len(candidates) {
		node := candidates[len(selected)]
		selected = append(selected, node)
	}

	return selected
}

// allocateVolumeOnNode allocates a volume on a data node via gRPC.
func (vg *VolumeGrowth) allocateVolumeOnNode(ctx context.Context, node *DataNode, volumeId uint32, collection string, replicaPlacement types.ReplicaPlacement, ttl string, diskType types.DiskType) error {
	// In a real implementation, this would call the volume server's AllocateVolume RPC
	// For now, we just update the topology

	// Simulate successful allocation
	return nil
}

// deallocateVolumeOnNode deallocates a volume from a data node.
func (vg *VolumeGrowth) deallocateVolumeOnNode(ctx context.Context, node *DataNode, volumeId uint32) error {
	// In a real implementation, this would call the volume server's DeleteVolume RPC
	// For now, we just update the topology
	node.DeleteVolume(volumeId)
	return nil
}

// GrowthStrategy defines how volumes should be grown.
type GrowthStrategy interface {
	// ShouldGrow returns true if volumes should be grown.
	ShouldGrow(volumeLayout *VolumeLayout) bool

	// SelectVolumeCount returns how many volumes to create.
	SelectVolumeCount(volumeLayout *VolumeLayout) int
}

// DefaultGrowthStrategy is the default growth strategy.
type DefaultGrowthStrategy struct {
	Threshold float64 // Trigger growth when free ratio falls below this (0.9 = 90%)
}

// NewDefaultGrowthStrategy creates a new DefaultGrowthStrategy.
func NewDefaultGrowthStrategy(threshold float64) *DefaultGrowthStrategy {
	return &DefaultGrowthStrategy{
		Threshold: threshold,
	}
}

// ShouldGrow returns true if volumes should be grown.
func (s *DefaultGrowthStrategy) ShouldGrow(volumeLayout *VolumeLayout) bool {
	writableCount := volumeLayout.GetWritableVolumeCount()
	totalCount := volumeLayout.GetTotalVolumeCount()

	if totalCount == 0 {
		return true
	}

	freeRatio := float64(writableCount) / float64(totalCount)
	return freeRatio < s.Threshold
}

// SelectVolumeCount returns how many volumes to create.
func (s *DefaultGrowthStrategy) SelectVolumeCount(volumeLayout *VolumeLayout) int {
	// Grow by 10% or at least 1 volume
	growthCount := int(float64(volumeLayout.GetTotalVolumeCount()) * 0.1)
	if growthCount < 1 {
		growthCount = 1
	}
	return growthCount
}
