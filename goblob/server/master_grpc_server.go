package server

import (
	"context"
	"fmt"

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
	for {
		req, err := stream.Recv()
		if err != nil {
			return err
		}

		// Register cluster node
		if req.ClientType != "" && req.ClientAddress != "" {
			s.ms.Cluster.Register(req)
		}

		// Send response
		resp := &master_pb.KeepConnectedResponse{
			MetricsAddress: fmt.Sprintf("%s:%d", s.ms.option.Host, s.ms.option.Port),
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

// Assign handles gRPC assign requests.
func (s *MasterGRPCServer) Assign(ctx context.Context, req *master_pb.AssignRequest) (*master_pb.AssignResponse, error) {
	if !s.ms.isLeader.Load() {
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
	fid := types.FileId{
		VolumeId: types.VolumeId(vid),
		NeedleId: types.NeedleId(needleId),
		Cookie:   types.Cookie(0x12345678), // TODO: generate random cookie
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
	// For now, return empty response - full implementation in Phase 5
	return &master_pb.LookupVolumeResponse{
		// VolumeOrFileIds -> locations map would go here
	}, nil
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
	// For now, return empty response - full implementation in Phase 5
	return &master_pb.VolumeListResponse{
		VolumeSizeLimit: uint64(s.ms.option.VolumeSizeLimitMB) * 1024 * 1024,
	}, nil
}
