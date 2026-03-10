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

	t.Run("should grow at 90% threshold (89% writable)", func(t *testing.T) {
		strategy := NewDefaultGrowthStrategy(0.9)
		vl := NewVolumeLayout("default", "000", "", types.HardDriveType)

		// 89 writable + 11 readonly = 100 total → 89% writable < 90% threshold → grow
		for i := 0; i < 89; i++ {
			vl.writableVolumes.Set(uint32(i+1), []*VolumeLocation{{}})
		}
		for i := 0; i < 11; i++ {
			loc := &VolumeLocation{}
			loc.SetReadOnly(true)
			vl.readonlyVolumes.Set(uint32(90+i), []*VolumeLocation{loc})
		}

		if !strategy.ShouldGrow(vl) {
			t.Error("expected should grow at 90% threshold (89% writable)")
		}
	})

	t.Run("should not grow when writable ratio >= threshold", func(t *testing.T) {
		strategy := NewDefaultGrowthStrategy(0.9)
		vl := NewVolumeLayout("default", "000", "", types.HardDriveType)

		// 50 writable + 0 readonly = 100% writable → above 90% → no grow
		for i := 0; i < 50; i++ {
			vl.writableVolumes.Set(uint32(i+1), []*VolumeLocation{{}})
		}

		if strategy.ShouldGrow(vl) {
			t.Error("expected should not grow when all volumes are writable")
		}
	})

	t.Run("select volume count", func(t *testing.T) {
		strategy := NewDefaultGrowthStrategy(0.9)

		tests := []struct {
			totalVolumes  int
			expectedCount int
		}{
			{0, 1},   // Minimum 1
			{10, 1},  // 10% of 10 = 1
			{100, 10}, // 10% of 100 = 10
			{5, 1},   // 10% of 5 = 0.5 → min 1
		}

		for _, tt := range tests {
			vl := NewVolumeLayout("default", "000", "", types.HardDriveType)
			for i := 0; i < tt.totalVolumes; i++ {
				vl.writableVolumes.Set(uint32(i+1), []*VolumeLocation{{}})
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

	t.Run("should grow at 5% writable (below 90% threshold)", func(t *testing.T) {
		topo := NewTopology()
		vg := NewVolumeGrowth(topo)

		vl := topo.GetOrCreateVolumeLayout("default", "000", "", types.HardDriveType)
		// 5 writable + 95 readonly = 5% writable → below 90% → should grow
		for i := 0; i < 95; i++ {
			loc := &VolumeLocation{}
			loc.SetReadOnly(true)
			vl.readonlyVolumes.Set(uint32(i+1), []*VolumeLocation{loc})
		}
		for i := 0; i < 5; i++ {
			vl.writableVolumes.Set(uint32(96+i), []*VolumeLocation{{}})
		}

		if !vg.shouldGrowVolumes(vl) {
			t.Error("expected should grow at 5% writable ratio (below 90% threshold)")
		}
	})

	t.Run("should not grow when all volumes are writable", func(t *testing.T) {
		topo := NewTopology()
		vg := NewVolumeGrowth(topo)

		vl := topo.GetOrCreateVolumeLayout("default", "000", "", types.HardDriveType)
		// 50 writable + 0 readonly = 100% writable → above 90% → no grow
		for i := 0; i < 50; i++ {
			vl.writableVolumes.Set(uint32(i+1), []*VolumeLocation{{}})
		}

		if vg.shouldGrowVolumes(vl) {
			t.Error("expected should not grow when all volumes are writable")
		}
	})

	t.Run("reserve capacity on data node with free slots", func(t *testing.T) {
		topo := NewTopology()
		vg := NewVolumeGrowth(topo)

		hb := &master_pb.Heartbeat{
			Ip:       "127.0.0.1",
			Port:     8080,
			GrpcPort: 18080,
			DataCenter: "dc1",
			Rack:       "rack1",
			Volumes:    []*master_pb.VolumeInformationMessage{},
			MaxVolumeCounts: []*master_pb.MaxVolumeCounts{
				{DiskType: "hdd", MaxCount: 10},
			},
		}
		topo.ProcessJoinMessage(hb)

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

		dn := NewDataNode("dn1")
		disk := dn.GetOrCreateDisk(types.HardDriveType)
		disk.SetMaxVolumeCount(5)
		for i := 0; i < 5; i++ {
			disk.AddVolume(uint32(i + 1))
		}

		reservation := vg.reserveCapacity(dn, types.HardDriveType)
		if reservation != nil {
			t.Error("expected reservation to fail when disk is full")
		}
	})

	t.Run("select nodes for replica placement", func(t *testing.T) {
		topo := NewTopology()
		vg := NewVolumeGrowth(topo)

		candidates := []*DataNode{
			{NodeImpl: NewNodeImpl("dn1")},
			{NodeImpl: NewNodeImpl("dn2")},
			{NodeImpl: NewNodeImpl("dn3")},
		}
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
	t.Run("new reservation is unused", func(t *testing.T) {
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
