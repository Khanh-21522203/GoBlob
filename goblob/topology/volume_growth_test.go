package topology

import (
	"testing"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/master_pb"
)

func TestDefaultGrowthStrategy(t *testing.T) {
	t.Run("should grow when no volumes", func(t *testing.T) {
		strategy := NewDefaultGrowthStrategy(0.9)
		vl := NewVolumeLayout("default", "000", "", types.HardDriveType)

		if !strategy.ShouldGrow(vl) {
			t.Error("expected should grow when no volumes")
		}
	})

	t.Run("should grow at 90% threshold", func(t *testing.T) {
		strategy := NewDefaultGrowthStrategy(0.9)
		vl := NewVolumeLayout("default", "000", "", types.HardDriveType)

		// Add 89 writable volumes (89% writable)
		for i := 0; i < 89; i++ {
			vl.writableVolumes.Set(uint32(i+1), &VolumeLocation{})
		}

		// Add 11 readonly volumes
		for i := 0; i < 11; i++ {
			loc := &VolumeLocation{}
			loc.SetReadOnly(true)
			vl.readonlyVolumes.Set(uint32(90+i), loc)
		}

		if !strategy.ShouldGrow(vl) {
			t.Error("expected should grow at 90% threshold (89% writable)")
		}
	})

	t.Run("should not grow when under threshold", func(t *testing.T) {
		strategy := NewDefaultGrowthStrategy(0.9)
		vl := NewVolumeLayout("default", "000", "", types.HardDriveType)

		// Add 50 writable volumes (50% full)
		for i := 0; i < 50; i++ {
			vl.writableVolumes.Set(uint32(i+1), &VolumeLocation{})
		}

		if strategy.ShouldGrow(vl) {
			t.Error("expected should not grow under threshold")
		}
	})

	t.Run("select volume count", func(t *testing.T) {
		strategy := NewDefaultGrowthStrategy(0.9)

		tests := []struct {
			totalVolumes  int
			expectedCount int
		}{
			{0, 1},      // Minimum 1
			{10, 1},     // 10% of 10 = 1
			{100, 10},   // 10% of 100 = 10
			{5, 1},      // 10% of 5 = 0.5 -> min 1
		}

		for _, tt := range tests {
			vl := NewVolumeLayout("default", "000", "", types.HardDriveType)

			// Add volumes
			for i := 0; i < tt.totalVolumes; i++ {
				vl.writableVolumes.Set(uint32(i+1), &VolumeLocation{})
			}

			count := strategy.SelectVolumeCount(vl)
			if count != tt.expectedCount {
				t.Errorf("expected count=%d for %d volumes, got %d", tt.expectedCount, tt.totalVolumes, count)
			}
		}
	})
}

func TestVolumeGrowth(t *testing.T) {
	t.Run("new volume growth", func(t *testing.T) {
		topo := NewTopology()
		vg := NewVolumeGrowth(topo)

		if vg == nil {
			t.Error("failed to create volume growth")
		}
	})

	t.Run("should grow volumes", func(t *testing.T) {
		topo := NewTopology()
		vg := NewVolumeGrowth(topo)

		// Create volume layout with 5% writable volumes
		vl := topo.GetOrCreateVolumeLayout("default", "000", "", types.HardDriveType)
		// Add 95 readonly volumes
		for i := 0; i < 95; i++ {
			loc := &VolumeLocation{}
			loc.SetReadOnly(true)
			vl.readonlyVolumes.Set(uint32(i+1), loc)
		}
		// Add only 5 writable volumes (5% writable ratio, below 10% threshold)
		for i := 0; i < 5; i++ {
			loc := &VolumeLocation{}
			vl.writableVolumes.Set(uint32(96+i), loc)
		}

		shouldGrow := vg.shouldGrowVolumes(vl)
		if !shouldGrow {
			t.Error("expected should grow at 5% writable ratio (below 10% threshold)")
		}
	})

	t.Run("should not grow volumes when under threshold", func(t *testing.T) {
		topo := NewTopology()
		vg := NewVolumeGrowth(topo)

		// Create volume layout at 50% capacity
		vl := topo.GetOrCreateVolumeLayout("default", "000", "", types.HardDriveType)
		for i := 0; i < 50; i++ {
			loc := &VolumeLocation{}
			vl.writableVolumes.Set(uint32(i+1), loc)
		}

		shouldGrow := vg.shouldGrowVolumes(vl)
		if shouldGrow {
			t.Error("expected should not grow under threshold")
		}
	})

	t.Run("reserve capacity", func(t *testing.T) {
		topo := NewTopology()
		vg := NewVolumeGrowth(topo)

		// Create data node with free slots
		hb := &master_pb.Heartbeat{
			Ip:           "127.0.0.1",
			Port:         8080,
			GrpcPort:     18080,
			DataCenter:   "dc1",
			Rack:         "rack1",
			Volumes:      []*master_pb.VolumeInformationMessage{},
			MaxVolumeCounts: []*master_pb.MaxVolumeCounts{
				{DiskType: "hdd", MaxCount: 10},
			},
		}

		topo.ProcessJoinMessage(hb)

		// Get the data node
		dn := topo.GetDataCenter("dc1").GetOrCreateRack("rack1").GetOrCreateDataNode("127.0.0.1:18080")

		reservation := vg.reserveCapacity(dn, types.HardDriveType)
		if reservation == nil {
			t.Error("expected successful reservation")
		}

		if reservation.DataNode != dn {
			t.Error("reservation data node mismatch")
		}
	})

	t.Run("reserve capacity fails when disk is full", func(t *testing.T) {
		topo := NewTopology()
		vg := NewVolumeGrowth(topo)

		// Create data node with no free slots
		dn := NewDataNode("dn1")
		disk := dn.GetOrCreateDisk(types.HardDriveType)
		disk.SetMaxVolumeCount(5)

		// Fill up the disk
		for i := 0; i < 5; i++ {
			disk.AddVolume(uint32(i + 1))
		}

		reservation := vg.reserveCapacity(dn, types.HardDriveType)
		if reservation != nil {
			t.Error("expected reservation to fail when disk is full")
		}
	})

	t.Run("select nodes for replica", func(t *testing.T) {
		topo := NewTopology()
		vg := NewVolumeGrowth(topo)

		// Create multiple data nodes
		candidates := []*DataNode{
			{NodeImpl: NewNodeImpl("dn1")},
			{NodeImpl: NewNodeImpl("dn2")},
			{NodeImpl: NewNodeImpl("dn3")},
		}

		// Set up data centers and racks
		candidates[0].SetParent(NewNodeImpl("rack1"))
		candidates[1].SetParent(NewNodeImpl("rack2"))
		candidates[2].SetParent(NewNodeImpl("rack1"))

		replicaPlacement := types.ReplicaPlacement{
			DifferentDataCenterCount: 0,
			DifferentRackCount:       1,
			SameRackCount:            1,
		}

		reservations := make([]*CapacityReservation, len(candidates))
		for i := range candidates {
			reservations[i] = &CapacityReservation{DataNode: candidates[i]}
		}

		selected := vg.selectNodesForReplica(candidates, replicaPlacement, reservations)

		if len(selected) != replicaPlacement.TotalCopies() {
			t.Errorf("expected %d selected nodes, got %d", replicaPlacement.TotalCopies(), len(selected))
		}
	})
}

func TestCapacityReservation(t *testing.T) {
	t.Run("new reservation", func(t *testing.T) {
		dn := NewDataNode("dn1")
		reservation := &CapacityReservation{
			DataNode: dn,
			DiskType: types.HardDriveType,
			Used:     false,
		}

		if reservation.DataNode != dn {
			t.Error("data node mismatch")
		}

		if reservation.Used {
			t.Error("expected reservation to be unused")
		}
	})
}
