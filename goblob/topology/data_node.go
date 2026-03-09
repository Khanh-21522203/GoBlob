package topology

import (
	"fmt"
	"sync"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/master_pb"
)

// DataNode represents a volume server in the cluster topology.
// It contains disk information and volumes.
type DataNode struct {
	*NodeImpl
	url       string
	publicUrl string
	ip        string
	port      uint32
	grpcPort  uint32
	disks     map[types.DiskType]*DiskInfo
	volumes   map[uint32]*master_pb.VolumeInformationMessage
	mu        sync.RWMutex
}

// NewDataNode creates a new DataNode.
func NewDataNode(id string) *DataNode {
	return &DataNode{
		NodeImpl: NewNodeImpl(id),
		disks:    make(map[types.DiskType]*DiskInfo),
		volumes:  make(map[uint32]*master_pb.VolumeInformationMessage),
	}
}

// ToDataCenter returns nil for a DataNode.
func (dn *DataNode) ToDataCenter() (*DataCenter, bool) {
	return nil, false
}

// ToRack returns nil for a DataNode.
func (dn *DataNode) ToRack() (*Rack, bool) {
	return nil, false
}

// ToDataNode returns this node as a DataNode.
func (dn *DataNode) ToDataNode() (*DataNode, bool) {
	return dn, true
}

// GetUrl returns the URL of this data node.
func (dn *DataNode) GetUrl() string {
	dn.mu.RLock()
	defer dn.mu.RUnlock()
	return dn.url
}

// SetUrl sets the URL of this data node.
func (dn *DataNode) SetUrl(url string) {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	dn.url = url
}

// GetPublicUrl returns the public URL of this data node.
func (dn *DataNode) GetPublicUrl() string {
	dn.mu.RLock()
	defer dn.mu.RUnlock()
	return dn.publicUrl
}

// SetPublicUrl sets the public URL of this data node.
func (dn *DataNode) SetPublicUrl(url string) {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	dn.publicUrl = url
}

// GetIp returns the IP address of this data node.
func (dn *DataNode) GetIp() string {
	dn.mu.RLock()
	defer dn.mu.RUnlock()
	return dn.ip
}

// SetIp sets the IP address of this data node.
func (dn *DataNode) SetIp(ip string) {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	dn.ip = ip
}

// GetPort returns the HTTP port of this data node.
func (dn *DataNode) GetPort() uint32 {
	dn.mu.RLock()
	defer dn.mu.RUnlock()
	return dn.port
}

// SetPort sets the HTTP port of this data node.
func (dn *DataNode) SetPort(port uint32) {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	dn.port = port
}

// GetGrpcPort returns the gRPC port of this data node.
func (dn *DataNode) GetGrpcPort() uint32 {
	dn.mu.RLock()
	defer dn.mu.RUnlock()
	return dn.grpcPort
}

// SetGrpcPort sets the gRPC port of this data node.
func (dn *DataNode) SetGrpcPort(port uint32) {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	dn.grpcPort = port
}

// GetOrCreateDisk gets or creates a disk info for the given disk type.
func (dn *DataNode) GetOrCreateDisk(diskType types.DiskType) *DiskInfo {
	dn.mu.Lock()
	defer dn.mu.Unlock()

	return dn.getOrCreateDiskUnsafe(diskType)
}

// getOrCreateDiskUnsafe gets or creates a disk info without taking the lock.
// Caller must hold dn.mu.Lock().
func (dn *DataNode) getOrCreateDiskUnsafe(diskType types.DiskType) *DiskInfo {
	if disk, ok := dn.disks[diskType]; ok {
		return disk
	}

	disk := NewDiskInfo(diskType)
	dn.disks[diskType] = disk
	return disk
}

// GetDisk returns the disk info for the given disk type, or nil if not found.
func (dn *DataNode) GetDisk(diskType types.DiskType) *DiskInfo {
	dn.mu.RLock()
	defer dn.mu.RUnlock()
	return dn.disks[diskType]
}

// UpdateMaxVolumeCounts updates the max volume counts for each disk type.
func (dn *DataNode) UpdateMaxVolumeCounts(maxCounts []*master_pb.MaxVolumeCounts) {
	dn.mu.Lock()
	defer dn.mu.Unlock()

	for _, mc := range maxCounts {
		diskType := types.DiskType(mc.DiskType)
		if diskType == "" {
			diskType = types.DefaultDiskType
		}

		disk := dn.getOrCreateDiskUnsafe(diskType)
		disk.maxVolumeCount = mc.MaxCount
	}
}

// AddOrUpdateVolume adds or updates a volume on this data node.
func (dn *DataNode) AddOrUpdateVolume(volInfo *master_pb.VolumeInformationMessage) {
	dn.mu.Lock()
	defer dn.mu.Unlock()

	dn.volumes[volInfo.Id] = volInfo

	// Update disk info
	diskType := types.DiskType(volInfo.DiskType)
	if diskType == "" {
		diskType = types.DefaultDiskType
	}

	disk := dn.getOrCreateDiskUnsafe(diskType)
	disk.AddVolume(volInfo.Id)
}

// DeleteVolume removes a volume from this data node.
func (dn *DataNode) DeleteVolume(vid uint32) {
	dn.mu.Lock()
	defer dn.mu.Unlock()

	if volInfo, ok := dn.volumes[vid]; ok {
		delete(dn.volumes, vid)

		// Update disk info
		diskType := types.DiskType(volInfo.DiskType)
		if diskType == "" {
			diskType = types.DefaultDiskType
		}

		if disk, ok := dn.disks[diskType]; ok {
			disk.RemoveVolume(vid)
		}
	}
}

// GetVolumes returns all volumes on this data node.
func (dn *DataNode) GetVolumes() []*master_pb.VolumeInformationMessage {
	dn.mu.RLock()
	defer dn.mu.RUnlock()

	result := make([]*master_pb.VolumeInformationMessage, 0, len(dn.volumes))
	for _, vol := range dn.volumes {
		result = append(result, vol)
	}
	return result
}

// GetVolumeCount returns the number of volumes on this data node.
func (dn *DataNode) GetVolumeCount() int {
	dn.mu.RLock()
	defer dn.mu.RUnlock()
	return len(dn.volumes)
}

// GetFreeVolumeSlots returns the number of free volume slots for each disk type.
func (dn *DataNode) GetFreeVolumeSlots() map[types.DiskType]int {
	dn.mu.RLock()
	defer dn.mu.RUnlock()

	result := make(map[types.DiskType]int)
	for diskType, disk := range dn.disks {
		result[diskType] = disk.GetFreeVolumeSlot()
	}
	return result
}

// GetDataCenter returns the parent data center ID.
func (dn *DataNode) GetDataCenter() string {
	parent := dn.GetParent()
	if parent == nil {
		return ""
	}
	parentParent := parent.GetParent()
	if parentParent == nil {
		return ""
	}
	return parentParent.GetId()
}

// GetRack returns the parent rack ID.
func (dn *DataNode) GetRack() string {
	parent := dn.GetParent()
	if parent == nil {
		return ""
	}
	return parent.GetId()
}

// DiskInfo represents disk information for a specific disk type on a data node.
type DiskInfo struct {
	diskType       types.DiskType
	volumeCount    int
	maxVolumeCount uint32
	mu             sync.RWMutex
}

// NewDiskInfo creates a new DiskInfo.
func NewDiskInfo(diskType types.DiskType) *DiskInfo {
	return &DiskInfo{
		diskType: diskType,
	}
}

// GetDiskType returns the disk type.
func (di *DiskInfo) GetDiskType() types.DiskType {
	return di.diskType
}

// GetVolumeCount returns the current number of volumes on this disk.
func (di *DiskInfo) GetVolumeCount() int {
	di.mu.RLock()
	defer di.mu.RUnlock()
	return di.volumeCount
}

// GetMaxVolumeCount returns the maximum number of volumes allowed on this disk.
func (di *DiskInfo) GetMaxVolumeCount() uint32 {
	di.mu.RLock()
	defer di.mu.RUnlock()
	return di.maxVolumeCount
}

// SetMaxVolumeCount sets the maximum number of volumes allowed on this disk.
func (di *DiskInfo) SetMaxVolumeCount(max uint32) {
	di.mu.Lock()
	defer di.mu.Unlock()
	di.maxVolumeCount = max
}

// AddVolume increments the volume count.
func (di *DiskInfo) AddVolume(vid uint32) {
	di.mu.Lock()
	defer di.mu.Unlock()
	di.volumeCount++
}

// RemoveVolume decrements the volume count.
func (di *DiskInfo) RemoveVolume(vid uint32) {
	di.mu.Lock()
	defer di.mu.Unlock()
	if di.volumeCount > 0 {
		di.volumeCount--
	}
}

// GetFreeVolumeSlot returns the number of free volume slots.
func (di *DiskInfo) GetFreeVolumeSlot() int {
	di.mu.RLock()
	defer di.mu.RUnlock()

	if di.maxVolumeCount == 0 {
		return 0 // No limit configured
	}

	free := int(di.maxVolumeCount) - di.volumeCount
	if free < 0 {
		return 0
	}
	return free
}

// IsFull returns true if the disk is at capacity.
func (di *DiskInfo) IsFull() bool {
	di.mu.RLock()
	defer di.mu.RUnlock()

	if di.maxVolumeCount == 0 {
		return false // No limit configured
	}

	return di.volumeCount >= int(di.maxVolumeCount)
}

// ToProto converts the disk info to protobuf format.
func (di *DiskInfo) ToProto() *master_pb.MaxVolumeCounts {
	di.mu.RLock()
	defer di.mu.RUnlock()

	return &master_pb.MaxVolumeCounts{
		DiskType: string(di.diskType),
		MaxCount: di.maxVolumeCount,
	}
}

// UpdateFromHeartbeat updates the data node from a heartbeat message.
func (dn *DataNode) UpdateFromHeartbeat(hb *master_pb.Heartbeat) error {
	if hb == nil {
		return fmt.Errorf("heartbeat is nil")
	}

	dn.SetIp(hb.Ip)
	dn.SetPort(hb.Port)
	dn.SetPublicUrl(hb.PublicUrl)
	dn.SetGrpcPort(hb.GrpcPort)

	// Build URL
	if dn.GetUrl() == "" {
		url := fmt.Sprintf("http://%s:%d", hb.Ip, hb.Port)
		dn.SetUrl(url)
	}

	dn.UpdateMaxVolumeCounts(hb.MaxVolumeCounts)

	return nil
}
