package topology

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/master_pb"
	"GoBlob/goblob/pb/volume_server_pb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// VolumeGrowth manages automatic volume creation based on capacity thresholds.
type VolumeGrowth struct {
	topology        *Topology
	replicateMaxVid func(uint32) error
	mu              sync.RWMutex
}

// NewVolumeGrowth creates a new VolumeGrowth instance.
func NewVolumeGrowth(topology *Topology) *VolumeGrowth {
	return &VolumeGrowth{
		topology: topology,
	}
}

// SetMaxVolumeIdReplicator sets a callback used to persist max volume id changes.
func (vg *VolumeGrowth) SetMaxVolumeIdReplicator(fn func(uint32) error) {
	vg.mu.Lock()
	defer vg.mu.Unlock()
	vg.replicateMaxVid = fn
}

// CheckAndGrow checks if volumes need to be grown and creates new volumes if needed.
// Returns the number of volumes created.
func (vg *VolumeGrowth) CheckAndGrow(ctx context.Context, collection string, replicaPlacement types.ReplicaPlacement, ttl string, diskType types.DiskType, count int) (int, error) {
	vg.mu.Lock()
	defer vg.mu.Unlock()

	if count <= 0 {
		return 0, nil
	}
	volumeLayout := vg.topology.GetOrCreateVolumeLayout(collection, replicaPlacement.String(), ttl, diskType)

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

	created := 0

	// Create volumes
	for i := 0; i < count; i++ {
		// Select nodes that satisfy replica placement constraints
		selectedNodes := vg.selectNodesForReplica(candidates, replicaPlacement, nil)
		if len(selectedNodes) < replicaPlacement.TotalCopies() {
			return created, fmt.Errorf("not enough nodes with capacity for volume %d", i)
		}

		// Reserve capacity on selected nodes.
		reservations := make([]*CapacityReservation, 0, len(selectedNodes))
		for _, node := range selectedNodes {
			res := vg.reserveCapacity(node, diskType)
			if res == nil {
				for _, allocated := range reservations {
					vg.releaseReservation(allocated)
				}
				return created, fmt.Errorf("failed to reserve capacity on node %s", node.GetId())
			}
			reservations = append(reservations, res)
		}

		volumeId, err := vg.topology.AllocateNextVolumeId(func(vid uint32) error {
			if vg.replicateMaxVid == nil {
				return nil
			}
			return vg.replicateMaxVid(vid)
		})
		if err != nil {
			for _, res := range reservations {
				vg.releaseReservation(res)
			}
			return created, fmt.Errorf("failed to persist max volume id: %w", err)
		}

		// Allocate volume on each selected node
		success := true
		for j, node := range selectedNodes {
			if err := vg.allocateVolumeOnNode(ctx, node, volumeId, collection, replicaPlacement, ttl, reservations[j].DiskType); err != nil {
				// Rollback: deallocate volumes from previous nodes
				for k := 0; k < j; k++ {
					_ = vg.deallocateVolumeOnNode(ctx, selectedNodes[k], volumeId)
				}
				for _, res := range reservations {
					vg.releaseReservation(res)
				}
				success = false
				break
			}
		}

		if success {
			for _, res := range reservations {
				vg.releaseReservation(res)
			}
			created++
		} else {
			return created, fmt.Errorf("failed to allocate volume %d", volumeId)
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
	if dataNode == nil {
		return nil
	}

	reservedDiskType := diskType
	if reservedDiskType == types.DefaultDiskType {
		// pickReservationDiskType returns (diskType, true) when a disk with free
		// slots is found. DefaultDiskType ("") is a valid disk type name, so we
		// must use the bool to distinguish "found DefaultDiskType" from "no disk
		// with free slots".
		dt, ok := pickReservationDiskType(dataNode)
		if !ok {
			return nil
		}
		reservedDiskType = dt
	}
	if !dataNode.ReserveSlot(reservedDiskType) {
		return nil
	}

	return &CapacityReservation{
		DataNode: dataNode,
		DiskType: reservedDiskType,
		Used:     true,
	}
}

// releaseReservation releases a capacity reservation.
func (vg *VolumeGrowth) releaseReservation(reservation *CapacityReservation) {
	if reservation == nil || reservation.DataNode == nil {
		return
	}
	reservation.DataNode.ReleaseSlot(reservation.DiskType)
	reservation.Used = false
}

// selectNodesForReplica selects nodes for volume replication based on placement constraints.
func (vg *VolumeGrowth) selectNodesForReplica(candidates []*DataNode, replicaPlacement types.ReplicaPlacement, reservations []*CapacityReservation) []*DataNode {
	_ = reservations

	totalCopies := replicaPlacement.TotalCopies()
	if totalCopies <= 0 || len(candidates) == 0 {
		return nil
	}

	selected := make([]*DataNode, 0, totalCopies)
	usedNodes := make(map[string]bool)

	primary := candidates[0]
	selected = append(selected, primary)
	usedNodes[primary.GetId()] = true

	primaryDC := primary.GetDataCenter()
	primaryRack := primary.GetRack()

	// Pick replicas in different data centers first.
	for _, node := range candidates[1:] {
		if len(selected) >= 1+int(replicaPlacement.DifferentDataCenterCount) {
			break
		}
		if usedNodes[node.GetId()] {
			continue
		}
		if node.GetDataCenter() == primaryDC {
			continue
		}
		selected = append(selected, node)
		usedNodes[node.GetId()] = true
	}

	// Pick replicas in different racks within the same data center as primary.
	usedRacks := map[string]bool{primaryRack: true}
	for _, node := range selected {
		if node.GetDataCenter() == primaryDC {
			usedRacks[node.GetRack()] = true
		}
	}

	targetRackReplicas := int(replicaPlacement.DifferentRackCount)
	for _, node := range candidates {
		if targetRackReplicas == 0 {
			break
		}
		if usedNodes[node.GetId()] || node.GetDataCenter() != primaryDC {
			continue
		}
		rack := node.GetRack()
		if usedRacks[rack] {
			continue
		}
		selected = append(selected, node)
		usedNodes[node.GetId()] = true
		usedRacks[rack] = true
		targetRackReplicas--
	}

	// Pick replicas in the same rack as primary.
	targetSameRack := int(replicaPlacement.SameRackCount)
	for _, node := range candidates {
		if targetSameRack == 0 {
			break
		}
		if usedNodes[node.GetId()] {
			continue
		}
		if node.GetDataCenter() == primaryDC && node.GetRack() == primaryRack {
			selected = append(selected, node)
			usedNodes[node.GetId()] = true
			targetSameRack--
		}
	}

	// Fill any remaining slots with any available candidates.
	for _, node := range candidates {
		if len(selected) >= totalCopies {
			break
		}
		if usedNodes[node.GetId()] {
			continue
		}
		selected = append(selected, node)
		usedNodes[node.GetId()] = true
	}

	if len(selected) > totalCopies {
		selected = selected[:totalCopies]
	}
	return selected
}

// allocateVolumeOnNode allocates a volume on a data node via gRPC.
func (vg *VolumeGrowth) allocateVolumeOnNode(ctx context.Context, node *DataNode, volumeId uint32, collection string, replicaPlacement types.ReplicaPlacement, ttl string, diskType types.DiskType) error {
	_ = ttl
	if node == nil {
		return fmt.Errorf("nil data node")
	}

	if grpcAddr := grpcAddressForDataNode(node); grpcAddr != "" {
		dialOpt := grpc.WithTransportCredentials(insecure.NewCredentials())
		err := pb.WithVolumeServerClient(grpcAddr, dialOpt, func(client volume_server_pb.VolumeServerClient) error {
			resp, err := client.AllocateVolume(ctx, &volume_server_pb.AllocateVolumeRequest{
				VolumeId:    volumeId,
				Collection:  collection,
				Replication: replicaPlacement.String(),
				Ttl:         ttl,
				DiskType:    string(diskType),
			})
			if err != nil {
				return err
			}
			if resp.GetError() != "" {
				return errors.New(resp.GetError())
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("allocate volume on %s failed: %w", grpcAddr, err)
		}
	}

	node.AddOrUpdateVolume(&master_pb.VolumeInformationMessage{
		Id:               volumeId,
		Collection:       collection,
		ReplicaPlacement: uint32(replicaPlacement.Byte()),
		DiskType:         string(diskType),
	})
	return nil
}

// deallocateVolumeOnNode deallocates a volume from a data node.
func (vg *VolumeGrowth) deallocateVolumeOnNode(ctx context.Context, node *DataNode, volumeId uint32) error {
	if node == nil {
		return nil
	}
	if grpcAddr := grpcAddressForDataNode(node); grpcAddr != "" {
		dialOpt := grpc.WithTransportCredentials(insecure.NewCredentials())
		_ = pb.WithVolumeServerClient(grpcAddr, dialOpt, func(client volume_server_pb.VolumeServerClient) error {
			_, err := client.VolumeDelete(ctx, &volume_server_pb.VolumeDeleteRequest{VolumeId: volumeId})
			return err
		})
	}
	node.DeleteVolume(volumeId)
	return nil
}

// pickReservationDiskType returns the disk type with free slots and true,
// or ("", false) if no disk with free slots exists.
func pickReservationDiskType(dataNode *DataNode) (types.DiskType, bool) {
	freeSlots := dataNode.GetFreeVolumeSlots()
	if len(freeSlots) == 0 {
		return "", false
	}

	diskTypes := make([]types.DiskType, 0, len(freeSlots))
	for dt := range freeSlots {
		diskTypes = append(diskTypes, dt)
	}
	sort.Slice(diskTypes, func(i, j int) bool { return string(diskTypes[i]) < string(diskTypes[j]) })
	for _, dt := range diskTypes {
		if freeSlots[dt] > 0 {
			return dt, true
		}
	}
	return "", false
}

func grpcAddressForDataNode(node *DataNode) string {
	ip := node.GetIp()
	port := node.GetGrpcPort()
	if ip != "" && port > 0 {
		return net.JoinHostPort(ip, strconv.FormatUint(uint64(port), 10))
	}
	id := node.GetId()
	if host, p, err := net.SplitHostPort(id); err == nil {
		return net.JoinHostPort(host, p)
	}
	return ""
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
