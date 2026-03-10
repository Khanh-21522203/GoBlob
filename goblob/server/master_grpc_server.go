package server

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/master_pb"
	"GoBlob/goblob/security"
	"GoBlob/goblob/topology"
)

// MasterGRPCServer implements the gRPC MasterServiceServer interface.
type MasterGRPCServer struct {
	master_pb.UnimplementedMasterServiceServer
	ms *MasterServer
}

// NewMasterGRPCServer creates a new gRPC server.
func NewMasterGRPCServer(ms *MasterServer) *MasterGRPCServer {
	return &MasterGRPCServer{ms: ms}
}

// SendHeartbeat handles bidirectional heartbeats from volume servers.
func (s *MasterGRPCServer) SendHeartbeat(stream master_pb.MasterService_SendHeartbeatServer) error {
	for {
		hb, err := stream.Recv()
		if err != nil {
			return err
		}

		// Process heartbeat
		if err := s.ms.Topo.ProcessJoinMessage(hb); err != nil {
			s.ms.logger.Warn("failed to process heartbeat", "error", err)
		}

		// Send response
		resp := &master_pb.HeartbeatResponse{
			VolumeSizeLimit: uint64(s.ms.option.VolumeSizeLimitMB) * 1024 * 1024,
			Leader:          s.ms.Raft.LeaderAddress(),
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

// KeepConnected handles bidirectional connections from filer/S3 servers.
func (s *MasterGRPCServer) KeepConnected(stream master_pb.MasterService_KeepConnectedServer) error {
	var clientAddr string
	for {
		req, err := stream.Recv()
		if err != nil {
			if clientAddr != "" {
				s.ms.Cluster.Unregister(clientAddr)
			}
			if err == io.EOF {
				return nil
			}
			return err
		}

		// Register cluster node
		if req.ClientType != "" && req.ClientAddress != "" {
			clientAddr = req.ClientAddress
			_, _ = s.ms.Cluster.Register(req)
		}

		// Send response
		resp := &master_pb.KeepConnectedResponse{
			MetricsAddress: fmt.Sprintf("%s:%d", s.ms.option.Host, s.ms.option.Port),
			VolumeLocations: []*master_pb.VolumeLocation{
				{Leader: s.ms.Raft.LeaderAddress()},
			},
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

// Assign handles gRPC assign requests.
func (s *MasterGRPCServer) Assign(ctx context.Context, req *master_pb.AssignRequest) (*master_pb.AssignResponse, error) {
	if s.ms.Raft == nil || !s.ms.Raft.IsLeader() {
		return nil, status.Error(codes.FailedPrecondition, "not leader")
	}

	// Parse replica placement
	replication := req.Replication
	if replication == "" {
		replication = s.ms.option.DefaultReplication
	}

	// Get or create volume layout
	volumeLayout := s.ms.Topo.GetOrCreateVolumeLayout(req.Collection, replication, req.Ttl, types.DiskType(req.DiskType))

	// Pick for write
	vid, locations, err := volumeLayout.PickForWrite(&topology.VolumeGrowOption{
		Collection:       req.Collection,
		ReplicaPlacement: types.ParseReplicaPlacementString(replication),
		Ttl:              req.Ttl,
		DiskType:         types.DiskType(req.DiskType),
	})
	if err != nil || len(locations) == 0 {
		return &master_pb.AssignResponse{
			Error: "no writable volumes available",
		}, nil
	}

	// Generate needle ID
	needleId := s.ms.Sequencer.NextFileId(req.Count)

	// Create file ID
	var cookieBytes [4]byte
	if _, err := rand.Read(cookieBytes[:]); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate cookie: %v", err)
	}
	fid := types.FileId{
		VolumeId: types.VolumeId(vid),
		NeedleId: types.NeedleId(needleId),
		Cookie:   types.Cookie(binary.BigEndian.Uint32(cookieBytes[:])),
	}

	// Get URL from DataNode
	url := ""
	publicUrl := ""
	if len(locations) > 0 && locations[0].DataNode != nil {
		url = locations[0].DataNode.GetUrl()
		publicUrl = locations[0].DataNode.GetPublicUrl()
		if publicUrl == "" {
			publicUrl = url
		}
	}

	// Generate auth token
	auth := ""
	if s.ms.Guard.HasJWTSigningKey() {
		token, _ := security.SignJWT(s.ms.Guard.SigningKey(), s.ms.option.JwtExpireSeconds)
		auth = token
	}

	return &master_pb.AssignResponse{
		Fid:       fid.String(),
		Url:       url,
		PublicUrl: publicUrl,
		Count:     req.Count,
		Auth:      auth,
	}, nil
}

// LookupVolume handles gRPC lookup volume requests.
func (s *MasterGRPCServer) LookupVolume(ctx context.Context, req *master_pb.LookupVolumeRequest) (*master_pb.LookupVolumeResponse, error) {
	locationsMap := make(map[string]*master_pb.VolumeIdLocations, len(req.VolumeOrFileIds))
	for _, item := range req.VolumeOrFileIds {
		vid, err := parseVolumeOrFileID(item)
		if err != nil {
			locationsMap[item] = &master_pb.VolumeIdLocations{Error: err.Error()}
			continue
		}

		locs := s.ms.Topo.LookupVolumeLocation(uint32(vid))
		if len(locs) == 0 {
			locationsMap[item] = &master_pb.VolumeIdLocations{Error: "volume not found"}
			continue
		}

		pbLocs := make([]*master_pb.Location, 0, len(locs))
		for _, l := range locs {
			pbLocs = append(pbLocs, &master_pb.Location{
				Url:        l.Url,
				PublicUrl:  l.PublicUrl,
				DataCenter: l.DataCenter,
			})
		}
		locationsMap[item] = &master_pb.VolumeIdLocations{Locations: pbLocs}
	}
	return &master_pb.LookupVolumeResponse{LocationsMap: locationsMap}, nil
}

// GetMasterConfiguration returns master server configuration.
func (s *MasterGRPCServer) GetMasterConfiguration(ctx context.Context, req *master_pb.GetMasterConfigurationRequest) (*master_pb.GetMasterConfigurationResponse, error) {
	return &master_pb.GetMasterConfigurationResponse{
		MetricsAddress:     fmt.Sprintf("%s:%d", s.ms.option.Host, s.ms.option.Port),
		VolumeSizeLimit:    uint64(s.ms.option.VolumeSizeLimitMB) * 1024 * 1024,
		DefaultReplication: s.ms.option.DefaultReplication,
		Leader:             s.ms.Raft.LeaderAddress(),
	}, nil
}

// VolumeList returns list of all volumes.
func (s *MasterGRPCServer) VolumeList(ctx context.Context, req *master_pb.VolumeListRequest) (*master_pb.VolumeListResponse, error) {
	_ = ctx
	_ = req

	topoInfo := &master_pb.TopologyInfo{
		Id: s.ms.Topo.GetId(),
	}

	var dataCenters []*master_pb.DataCenterInfo
	s.ms.Topo.ForEachDataCenter(func(dc *topology.DataCenter) bool {
		dcInfo := &master_pb.DataCenterInfo{Id: dc.GetId()}

		var racks []*master_pb.RackInfo
		dc.ForEachRack(func(rack *topology.Rack) bool {
			rackInfo := &master_pb.RackInfo{Id: rack.GetId()}

			var dataNodes []*master_pb.DataNodeInfo
			rack.ForEachDataNode(func(dn *topology.DataNode) bool {
				volumes := dn.GetVolumes()
				freeSlots := dn.GetFreeVolumeSlots()

				diskVolumeMap := make(map[types.DiskType][]*master_pb.VolumeInformationMessage)
				diskTypes := make(map[types.DiskType]struct{})
				for _, vol := range volumes {
					dt := types.DiskType(vol.DiskType)
					if dt == "" {
						dt = types.DefaultDiskType
					}
					diskTypes[dt] = struct{}{}
					diskVolumeMap[dt] = append(diskVolumeMap[dt], vol)
				}
				for dt := range freeSlots {
					diskTypes[dt] = struct{}{}
				}

				var diskKeys []types.DiskType
				for dt := range diskTypes {
					diskKeys = append(diskKeys, dt)
				}
				sort.Slice(diskKeys, func(i, j int) bool { return string(diskKeys[i]) < string(diskKeys[j]) })

				var diskInfos []*master_pb.DiskInfo
				for _, dt := range diskKeys {
					var maxCount uint64
					if disk := dn.GetDisk(dt); disk != nil {
						maxCount = uint64(disk.GetMaxVolumeCount())
					}

					freeCount := uint64(0)
					if slots, ok := freeSlots[dt]; ok && slots > 0 {
						freeCount = uint64(slots)
					}

					diskInfos = append(diskInfos, &master_pb.DiskInfo{
						Type:            string(dt),
						VolumeInfos:     diskVolumeMap[dt],
						MaxVolumeCount:  maxCount,
						FreeVolumeCount: freeCount,
					})
				}

				totalFree := uint64(0)
				for _, slots := range freeSlots {
					if slots > 0 {
						totalFree += uint64(slots)
					}
				}

				dataNodes = append(dataNodes, &master_pb.DataNodeInfo{
					Id:                dn.GetId(),
					PublicUrl:         dn.GetPublicUrl(),
					FreeVolumeCount:   totalFree,
					ActiveVolumeCount: uint64(len(volumes)),
					RemoteVolumeCount: 0,
					DiskInfos:         diskInfos,
				})
				return true
			})
			sort.Slice(dataNodes, func(i, j int) bool { return dataNodes[i].Id < dataNodes[j].Id })
			rackInfo.DataNodeInfos = dataNodes
			racks = append(racks, rackInfo)
			return true
		})
		sort.Slice(racks, func(i, j int) bool { return racks[i].Id < racks[j].Id })
		dcInfo.RackInfos = racks
		dataCenters = append(dataCenters, dcInfo)
		return true
	})

	sort.Slice(dataCenters, func(i, j int) bool { return dataCenters[i].Id < dataCenters[j].Id })
	topoInfo.DataCenterInfos = dataCenters

	return &master_pb.VolumeListResponse{
		TopologyInfo:    topoInfo,
		VolumeSizeLimit: uint64(s.ms.option.VolumeSizeLimitMB) * 1024 * 1024,
	}, nil
}

func parseVolumeOrFileID(value string) (types.VolumeId, error) {
	if value == "" {
		return 0, fmt.Errorf("empty volumeOrFileId")
	}
	if fid, err := types.ParseFileId(value); err == nil {
		return fid.VolumeId, nil
	}
	vid, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid volume id %q", value)
	}
	return types.VolumeId(vid), nil
}
