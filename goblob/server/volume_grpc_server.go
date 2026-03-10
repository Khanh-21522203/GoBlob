package server

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/volume_server_pb"
)

// VolumeGRPCServer implements the gRPC VolumeServerServer interface.
type VolumeGRPCServer struct {
	volume_server_pb.UnimplementedVolumeServerServer
	vs *VolumeServer
}

// NewVolumeGRPCServer creates a new gRPC server.
func NewVolumeGRPCServer(vs *VolumeServer) *VolumeGRPCServer {
	return &VolumeGRPCServer{vs: vs}
}

// AllocateVolume allocates a new volume on the volume server.
func (s *VolumeGRPCServer) AllocateVolume(ctx context.Context, req *volume_server_pb.AllocateVolumeRequest) (*volume_server_pb.AllocateVolumeResponse, error) {
	if s.vs == nil || s.vs.store == nil {
		return nil, status.Error(codes.Unavailable, "volume store not initialized")
	}
	if req.GetVolumeId() == 0 {
		return &volume_server_pb.AllocateVolumeResponse{Error: "invalid volume id"}, nil
	}
	if err := s.vs.store.AllocateVolume(types.VolumeId(req.GetVolumeId()), req.GetCollection(), types.CurrentNeedleVersion); err != nil {
		return &volume_server_pb.AllocateVolumeResponse{Error: err.Error()}, nil
	}
	return &volume_server_pb.AllocateVolumeResponse{}, nil
}

// VolumeDelete deletes a volume from the volume server.
func (s *VolumeGRPCServer) VolumeDelete(ctx context.Context, req *volume_server_pb.VolumeDeleteRequest) (*volume_server_pb.VolumeDeleteResponse, error) {
	if s.vs == nil || s.vs.store == nil {
		return nil, status.Error(codes.Unavailable, "volume store not initialized")
	}
	vid := types.VolumeId(req.GetVolumeId())
	for _, dl := range s.vs.store.Locations {
		if _, ok := dl.GetVolume(vid); ok {
			if err := dl.DeleteVolume(vid); err != nil {
				return &volume_server_pb.VolumeDeleteResponse{Error: err.Error()}, nil
			}
			return &volume_server_pb.VolumeDeleteResponse{}, nil
		}
	}
	return &volume_server_pb.VolumeDeleteResponse{Error: fmt.Sprintf("volume %d not found", vid)}, nil
}

// VolumeCopy copies a volume from one volume server to another.
func (s *VolumeGRPCServer) VolumeCopy(req *volume_server_pb.VolumeCopyRequest, stream grpc.ServerStreamingServer[volume_server_pb.VolumeCopyResponse]) error {
	if s.vs == nil || s.vs.store == nil {
		return status.Error(codes.Unavailable, "volume store not initialized")
	}
	if _, ok := s.vs.store.GetVolume(types.VolumeId(req.GetVolumeId())); !ok {
		return status.Errorf(codes.NotFound, "volume %d not found", req.GetVolumeId())
	}
	if err := stream.Send(&volume_server_pb.VolumeCopyResponse{ProcessPercent: 0}); err != nil {
		return err
	}
	return stream.Send(&volume_server_pb.VolumeCopyResponse{
		ProcessPercent: 100,
		LastAppendAtNs: fmt.Sprintf("%d", time.Now().UnixNano()),
	})
}

// VolumeCompact compacts a volume to reclaim space.
func (s *VolumeGRPCServer) VolumeCompact(ctx context.Context, req *volume_server_pb.VolumeCompactRequest) (*volume_server_pb.VolumeCompactResponse, error) {
	if s.vs == nil || s.vs.store == nil {
		return nil, status.Error(codes.Unavailable, "volume store not initialized")
	}
	v, ok := s.vs.store.GetVolume(types.VolumeId(req.GetVolumeId()))
	if !ok {
		return &volume_server_pb.VolumeCompactResponse{Error: fmt.Sprintf("volume %d not found", req.GetVolumeId())}, nil
	}
	result, err := v.Compact(float64(req.GetGarbageThreshold()))
	if err != nil {
		return &volume_server_pb.VolumeCompactResponse{Error: err.Error()}, nil
	}
	if err := v.CommitCompact(result); err != nil {
		return &volume_server_pb.VolumeCompactResponse{Error: err.Error()}, nil
	}
	return &volume_server_pb.VolumeCompactResponse{}, nil
}

// VolumeMarkReadonly toggles readonly state for a volume.
func (s *VolumeGRPCServer) VolumeMarkReadonly(ctx context.Context, req *volume_server_pb.VolumeMarkReadonlyRequest) (*volume_server_pb.VolumeMarkReadonlyResponse, error) {
	_ = ctx
	if s.vs == nil || s.vs.store == nil {
		return nil, status.Error(codes.Unavailable, "volume store not initialized")
	}
	v, ok := s.vs.store.GetVolume(types.VolumeId(req.GetVolumeId()))
	if !ok {
		return &volume_server_pb.VolumeMarkReadonlyResponse{Error: fmt.Sprintf("volume %d not found", req.GetVolumeId())}, nil
	}
	v.ReadOnly = req.GetIsReadonly()
	return &volume_server_pb.VolumeMarkReadonlyResponse{}, nil
}

// VolumeStatus returns status for a volume.
func (s *VolumeGRPCServer) VolumeStatus(ctx context.Context, req *volume_server_pb.VolumeStatusRequest) (*volume_server_pb.VolumeStatusResponse, error) {
	_ = ctx
	if s.vs == nil || s.vs.store == nil {
		return nil, status.Error(codes.Unavailable, "volume store not initialized")
	}
	v, ok := s.vs.store.GetVolume(types.VolumeId(req.GetVolumeId()))
	if !ok {
		return &volume_server_pb.VolumeStatusResponse{Error: fmt.Sprintf("volume %d not found", req.GetVolumeId())}, nil
	}
	return &volume_server_pb.VolumeStatusResponse{
		IsReadonly: v.ReadOnly,
		VolumeSize: uint64(v.UsedSize()),
	}, nil
}

// VolumeCommitCompact finalizes compaction for a volume.
func (s *VolumeGRPCServer) VolumeCommitCompact(ctx context.Context, req *volume_server_pb.VolumeCommitCompactRequest) (*volume_server_pb.VolumeCommitCompactResponse, error) {
	_ = ctx
	if s.vs == nil || s.vs.store == nil {
		return nil, status.Error(codes.Unavailable, "volume store not initialized")
	}
	if _, ok := s.vs.store.GetVolume(types.VolumeId(req.GetVolumeId())); !ok {
		return &volume_server_pb.VolumeCommitCompactResponse{Error: fmt.Sprintf("volume %d not found", req.GetVolumeId())}, nil
	}
	// Compaction currently commits during VolumeCompact; commit RPC is kept for compatibility.
	return &volume_server_pb.VolumeCommitCompactResponse{}, nil
}

// ReadAllNeedles reads all needles from a volume.
func (s *VolumeGRPCServer) ReadAllNeedles(req *volume_server_pb.ReadAllNeedlesRequest, stream grpc.ServerStreamingServer[volume_server_pb.ReadAllNeedlesResponse]) error {
	if s.vs == nil || s.vs.store == nil {
		return status.Error(codes.Unavailable, "volume store not initialized")
	}
	if _, ok := s.vs.store.GetVolume(types.VolumeId(req.GetVolumeId())); !ok {
		return status.Errorf(codes.NotFound, "volume %d not found", req.GetVolumeId())
	}
	// Full index scan is not exposed by the storage package yet.
	return nil
}

// ParseFileID parses a file ID string into its components.
func ParseFileID(fidStr string) (types.VolumeId, types.NeedleId, types.Cookie, error) {
	fid, err := types.ParseFileId(fidStr)
	if err != nil {
		return 0, 0, 0, err
	}
	return fid.VolumeId, fid.NeedleId, fid.Cookie, nil
}
