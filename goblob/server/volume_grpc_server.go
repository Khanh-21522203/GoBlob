package server

import (
	"context"

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
	// For Phase 4, this is a placeholder
	// Full implementation requires store.AllocateVolume with correct parameters
	return &volume_server_pb.AllocateVolumeResponse{}, status.Error(codes.Unimplemented, "not implemented yet")
}

// VolumeDelete deletes a volume from the volume server.
func (s *VolumeGRPCServer) VolumeDelete(ctx context.Context, req *volume_server_pb.VolumeDeleteRequest) (*volume_server_pb.VolumeDeleteResponse, error) {
	// TODO: Implement volume deletion
	return &volume_server_pb.VolumeDeleteResponse{}, status.Error(codes.Unimplemented, "not implemented yet")
}

// VolumeCopy copies a volume from one volume server to another.
func (s *VolumeGRPCServer) VolumeCopy(ctx context.Context, req *volume_server_pb.VolumeCopyRequest) (*volume_server_pb.VolumeCopyResponse, error) {
	// TODO: Implement volume copy
	return &volume_server_pb.VolumeCopyResponse{}, status.Error(codes.Unimplemented, "not implemented yet")
}

// VolumeCompact compacts a volume to reclaim space.
func (s *VolumeGRPCServer) VolumeCompact(ctx context.Context, req *volume_server_pb.VolumeCompactRequest) (*volume_server_pb.VolumeCompactResponse, error) {
	// TODO: Implement volume compaction
	return &volume_server_pb.VolumeCompactResponse{}, status.Error(codes.Unimplemented, "not implemented yet")
}

// ReadAllNeedles reads all needles from a volume.
func (s *VolumeGRPCServer) ReadAllNeedles(ctx context.Context, req *volume_server_pb.ReadAllNeedlesRequest) (*volume_server_pb.ReadAllNeedlesResponse, error) {
	// TODO: Implement read all needles
	return &volume_server_pb.ReadAllNeedlesResponse{}, status.Error(codes.Unimplemented, "not implemented yet")
}

// ParseFileID parses a file ID string into its components.
func ParseFileID(fidStr string) (types.VolumeId, types.NeedleId, types.Cookie, error) {
	fid, err := types.ParseFileId(fidStr)
	if err != nil {
		return 0, 0, 0, err
	}
	return fid.VolumeId, fid.NeedleId, fid.Cookie, nil
}
