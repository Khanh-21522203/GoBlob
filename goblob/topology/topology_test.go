package topology

import (
	"testing"
	"time"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/master_pb"
)

func TestNodeImpl(t *testing.T) {
	t.Run("new node has correct ID", func(t *testing.T) {
		node := NewNodeImpl("test-node")
		if node.GetId() != "test-node" {
			t.Errorf("expected id=test-node, got %s", node.GetId())
		}
	})

	t.Run("parent-child relationship", func(t *testing.T) {
		parent := NewNodeImpl("parent")
		child := NewNodeImpl("child")

		child.SetParent(parent)
		if child.GetParent() != parent {
			t.Error("failed to set parent")
		}

		// Add child to parent
		parent.GetOrCreateChild("child", func() Node { return child })

		if parent.ChildCount() != 1 {
			t.Errorf("expected 1 child, got %d", parent.ChildCount())
		}

		// Delete child
		parent.DeleteChild("child")
		if parent.ChildCount() != 0 {
			t.Errorf("expected 0 children after delete, got %d", parent.ChildCount())
		}
	})

	t.Run("is alive checks last seen", func(t *testing.T) {
		node := NewNodeImpl("test")

		// Initially alive
		if !node.IsAlive() {
			t.Error("expected node to be alive initially")
		}

		// Make node old (31 seconds ago)
		node.lastSeen = time.Now().Add(-31 * time.Second)

		if node.IsAlive() {
			t.Error("expected node to be dead after 31 seconds")
		}
	})
}

func TestDataCenter(t *testing.T) {
	t.Run("new data center", func(t *testing.T) {
		dc := NewDataCenter("dc1")
		if dc.GetId() != "dc1" {
			t.Errorf("expected id=dc1, got %s", dc.GetId())
		}
	})

	t.Run("rack management", func(t *testing.T) {
		dc := NewDataCenter("dc1")

		rack1 := dc.GetOrCreateRack("rack1")
		_ = dc.GetOrCreateRack("rack2")

		if dc.ChildCount() != 2 {
			t.Errorf("expected 2 racks, got %d", dc.ChildCount())
		}

		// Should return same rack for same ID
		rack1Again := dc.GetOrCreateRack("rack1")
		if rack1 != rack1Again {
			t.Error("expected same rack instance")
		}
	})

	t.Run("for each rack", func(t *testing.T) {
		dc := NewDataCenter("dc1")
		dc.GetOrCreateRack("rack1")
		dc.GetOrCreateRack("rack2")

		count := 0
		dc.ForEachRack(func(rack *Rack) bool {
			count++
			return true
		})

		if count != 2 {
			t.Errorf("expected 2 racks, got %d", count)
		}
	})
}

func TestRack(t *testing.T) {
	t.Run("new rack", func(t *testing.T) {
		rack := NewRack("rack1")
		if rack.GetId() != "rack1" {
			t.Errorf("expected id=rack1, got %s", rack.GetId())
		}
	})

	t.Run("data node management", func(t *testing.T) {
		rack := NewRack("rack1")

		dn1 := rack.GetOrCreateDataNode("dn1")
		_ = rack.GetOrCreateDataNode("dn2")

		if rack.ChildCount() != 2 {
			t.Errorf("expected 2 data nodes, got %d", rack.ChildCount())
		}

		// Should return same data node for same ID
		dn1Again := rack.GetOrCreateDataNode("dn1")
		if dn1 != dn1Again {
			t.Error("expected same data node instance")
		}
	})
}

func TestDataNode(t *testing.T) {
	t.Run("new data node", func(t *testing.T) {
		dn := NewDataNode("dn1")
		if dn.GetId() != "dn1" {
			t.Errorf("expected id=dn1, got %s", dn.GetId())
		}
	})

	t.Run("disk management", func(t *testing.T) {
		dn := NewDataNode("dn1")

		disk1 := dn.GetOrCreateDisk(types.HardDriveType)
		disk2 := dn.GetOrCreateDisk(types.SolidStateType)

		if disk1 == nil || disk2 == nil {
			t.Error("failed to create disks")
		}

		// Should return same disk for same type
		disk1Again := dn.GetOrCreateDisk(types.HardDriveType)
		if disk1 != disk1Again {
			t.Error("expected same disk instance")
		}
	})

	t.Run("volume management", func(t *testing.T) {
		dn := NewDataNode("dn1")

		volInfo := &master_pb.VolumeInformationMessage{
			Id:       1,
			Size:     1000,
			ReadOnly: false,
		}

		dn.AddOrUpdateVolume(volInfo)

		if dn.GetVolumeCount() != 1 {
			t.Errorf("expected 1 volume, got %d", dn.GetVolumeCount())
		}

		// Delete volume
		dn.DeleteVolume(1)
		if dn.GetVolumeCount() != 0 {
			t.Errorf("expected 0 volumes after delete, got %d", dn.GetVolumeCount())
		}
	})

	t.Run("free volume slots", func(t *testing.T) {
		dn := NewDataNode("dn1")

		// No max volume count set
		freeSlots := dn.GetFreeVolumeSlots()
		if len(freeSlots) != 0 {
			t.Errorf("expected no disk types, got %d", len(freeSlots))
		}

		// Set max volume count
		dn.UpdateMaxVolumeCounts([]*master_pb.MaxVolumeCounts{
			{DiskType: string(types.HardDriveType), MaxCount: 10},
		})

		// Add some volumes
		for i := 0; i < 3; i++ {
			dn.AddOrUpdateVolume(&master_pb.VolumeInformationMessage{
				Id:       uint32(i + 1),
				DiskType: string(types.HardDriveType),
			})
		}

		freeSlots = dn.GetFreeVolumeSlots()
		if freeSlots[types.HardDriveType] != 7 {
			t.Errorf("expected 7 free slots, got %d", freeSlots[types.HardDriveType])
		}
	})
}

func TestDiskInfo(t *testing.T) {
	t.Run("new disk info", func(t *testing.T) {
		disk := NewDiskInfo(types.HardDriveType)
		if disk.GetDiskType() != types.HardDriveType {
			t.Errorf("expected disk type=hdd, got %s", disk.GetDiskType())
		}
	})

	t.Run("volume counting", func(t *testing.T) {
		disk := NewDiskInfo(types.HardDriveType)
		disk.SetMaxVolumeCount(10)

		// Add volumes
		for i := 0; i < 3; i++ {
			disk.AddVolume(uint32(i + 1))
		}

		if disk.GetVolumeCount() != 3 {
			t.Errorf("expected 3 volumes, got %d", disk.GetVolumeCount())
		}

		if disk.GetFreeVolumeSlot() != 7 {
			t.Errorf("expected 7 free slots, got %d", disk.GetFreeVolumeSlot())
		}

		// Remove a volume
		disk.RemoveVolume(1)
		if disk.GetVolumeCount() != 2 {
			t.Errorf("expected 2 volumes after remove, got %d", disk.GetVolumeCount())
		}
	})

	t.Run("is full", func(t *testing.T) {
		disk := NewDiskInfo(types.HardDriveType)
		disk.SetMaxVolumeCount(5)

		if disk.IsFull() {
			t.Error("expected disk not to be full initially")
		}

		// Fill up the disk
		for i := 0; i < 5; i++ {
			disk.AddVolume(uint32(i + 1))
		}

		if !disk.IsFull() {
			t.Error("expected disk to be full")
		}
	})
}

func TestVolumeLayout(t *testing.T) {
	t.Run("new volume layout", func(t *testing.T) {
		vl := NewVolumeLayout("default", "000", "", types.HardDriveType)

		if vl.GetCollection() != "default" {
			t.Errorf("expected collection=default, got %s", vl.GetCollection())
		}

		if vl.GetReplication() != "000" {
			t.Errorf("expected replication=000, got %s", vl.GetReplication())
		}
	})

	t.Run("add and lookup volume", func(t *testing.T) {
		vl := NewVolumeLayout("default", "000", "", types.HardDriveType)

		dn := NewDataNode("dn1")
		loc := NewVolumeLocation(dn, 1)

		vl.AddVolumeLayout(loc)

		// Lookup volume — returns a slice of replica locations.
		foundLocs, ok := vl.LookupVolume(1)
		if !ok || len(foundLocs) == 0 {
			t.Fatal("failed to lookup volume")
		}

		if foundLocs[0].VolumeId != 1 {
			t.Errorf("expected volume id=1, got %d", foundLocs[0].VolumeId)
		}
	})

	t.Run("readonly volume", func(t *testing.T) {
		vl := NewVolumeLayout("default", "000", "", types.HardDriveType)

		dn := NewDataNode("dn1")
		loc := NewVolumeLocation(dn, 1)
		loc.SetReadOnly(true)

		vl.AddVolumeLayout(loc)

		if vl.GetReadonlyVolumeCount() != 1 {
			t.Errorf("expected 1 readonly volume, got %d", vl.GetReadonlyVolumeCount())
		}
	})
}

func TestTopology(t *testing.T) {
	t.Run("new topology", func(t *testing.T) {
		topo := NewTopology()
		if topo.GetId() != "root" {
			t.Errorf("expected id=root, got %s", topo.GetId())
		}
	})

	t.Run("process join message", func(t *testing.T) {
		topo := NewTopology()

		hb := &master_pb.Heartbeat{
			Ip:         "127.0.0.1",
			Port:       8080,
			GrpcPort:   18080,
			DataCenter: "dc1",
			Rack:       "rack1",
			PublicUrl:  "http://127.0.0.1:8080",
			MaxFileKey: 0,
			Volumes: []*master_pb.VolumeInformationMessage{
				{
					Id:               1,
					Size:             1000,
					Collection:       "default",
					ReplicaPlacement: 0,
					DiskType:         "hdd",
				},
			},
			MaxVolumeCounts: []*master_pb.MaxVolumeCounts{
				{DiskType: "hdd", MaxCount: 10},
			},
		}

		if err := topo.ProcessJoinMessage(hb); err != nil {
			t.Fatalf("failed to process join message: %v", err)
		}

		// Check data center was created
		dc := topo.GetDataCenter("dc1")
		if dc == nil {
			t.Fatal("data center not created")
		}

		// Check rack was created
		rack := dc.GetOrCreateRack("rack1")
		if rack == nil {
			t.Fatal("rack not created")
		}

		// Check data node was created
		if rack.ChildCount() != 1 {
			t.Errorf("expected 1 data node, got %d", rack.ChildCount())
		}
	})

	t.Run("lookup volume location", func(t *testing.T) {
		topo := NewTopology()

		hb := &master_pb.Heartbeat{
			Ip:         "127.0.0.1",
			Port:       8080,
			GrpcPort:   18080,
			DataCenter: "dc1",
			Rack:       "rack1",
			PublicUrl:  "http://127.0.0.1:8080",
			Volumes: []*master_pb.VolumeInformationMessage{
				{
					Id:         1,
					Size:       1000,
					Collection: "default",
					DiskType:   "hdd",
				},
			},
		}

		topo.ProcessJoinMessage(hb)

		// Lookup volume
		locations := topo.LookupVolumeLocation(1)
		if len(locations) == 0 {
			t.Fatal("volume location not found")
		}

		if locations[0].Url != "http://127.0.0.1:8080" {
			t.Errorf("expected url=http://127.0.0.1:8080, got %s", locations[0].Url)
		}
	})

	t.Run("removes stale volume when heartbeat no longer reports it", func(t *testing.T) {
		topo := NewTopology()

		first := &master_pb.Heartbeat{
			Ip:         "127.0.0.1",
			Port:       8080,
			GrpcPort:   18080,
			DataCenter: "dc1",
			Rack:       "rack1",
			PublicUrl:  "http://127.0.0.1:8080",
			Volumes: []*master_pb.VolumeInformationMessage{
				{Id: 11, Collection: "default", DiskType: "hdd"},
				{Id: 12, Collection: "default", DiskType: "hdd"},
			},
		}
		if err := topo.ProcessJoinMessage(first); err != nil {
			t.Fatalf("first heartbeat failed: %v", err)
		}
		if got := len(topo.LookupVolumeLocation(12)); got == 0 {
			t.Fatalf("expected volume 12 to exist after first heartbeat")
		}

		second := &master_pb.Heartbeat{
			Ip:         "127.0.0.1",
			Port:       8080,
			GrpcPort:   18080,
			DataCenter: "dc1",
			Rack:       "rack1",
			PublicUrl:  "http://127.0.0.1:8080",
			Volumes: []*master_pb.VolumeInformationMessage{
				{Id: 11, Collection: "default", DiskType: "hdd"},
			},
		}
		if err := topo.ProcessJoinMessage(second); err != nil {
			t.Fatalf("second heartbeat failed: %v", err)
		}
		if got := len(topo.LookupVolumeLocation(12)); got != 0 {
			t.Fatalf("expected volume 12 removed after second heartbeat, still has %d locations", got)
		}
	})
}

func TestVolumeLayoutCollection(t *testing.T) {
	t.Run("get or create layout", func(t *testing.T) {
		collection := NewVolumeLayoutCollection()

		vl1 := collection.GetOrCreate("default", "000", "", types.HardDriveType)
		vl2 := collection.GetOrCreate("default", "000", "", types.HardDriveType)

		if vl1 != vl2 {
			t.Error("expected same layout instance")
		}

		if collection.GetLayoutCount() != 1 {
			t.Errorf("expected 1 layout, got %d", collection.GetLayoutCount())
		}
	})

	t.Run("different keys create different layouts", func(t *testing.T) {
		collection := NewVolumeLayoutCollection()

		vl1 := collection.GetOrCreate("default", "000", "", types.HardDriveType)
		vl2 := collection.GetOrCreate("photos", "000", "", types.HardDriveType)

		if vl1 == vl2 {
			t.Error("expected different layout instances")
		}

		if collection.GetLayoutCount() != 2 {
			t.Errorf("expected 2 layouts, got %d", collection.GetLayoutCount())
		}
	})
}

func TestGetOrCreateChildFromVolumeLocation(t *testing.T) {
	t.Run("create full path", func(t *testing.T) {
		topo := NewTopology()

		node := GetOrCreateChildFromVolumeLocation(topo, "dc1", "rack1", "dn1")

		// Should have created the full hierarchy
		dc := topo.GetDataCenter("dc1")
		if dc == nil {
			t.Fatal("data center not created")
		}

		rack := dc.GetOrCreateRack("rack1")
		if rack == nil {
			t.Fatal("rack not created")
		}

		dn := rack.GetOrCreateDataNode("dn1")
		if dn == nil {
			t.Fatal("data node not created")
		}

		if node != dn {
			t.Error("expected returned node to be the data node")
		}
	})

	t.Run("partial path", func(t *testing.T) {
		topo := NewTopology()

		// Only data center
		node := GetOrCreateChildFromVolumeLocation(topo, "dc1", "", "")

		dc, ok := node.ToDataCenter()
		if !ok {
			t.Error("expected data center")
		}

		if dc.GetId() != "dc1" {
			t.Errorf("expected id=dc1, got %s", dc.GetId())
		}
	})
}
