package server

import (
	"context"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/volume_server_pb"
	"GoBlob/goblob/storage/needle"
)

// VolumeGRPCServer implements the gRPC VolumeServerServer interface.
type VolumeGRPCServer struct {
	volume_server_pb.UnimplementedVolumeServerServer
	vs *VolumeServer
}

var withVolumeServerClient = pb.WithVolumeServerClient

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
	if err := s.deleteVolume(vid); err != nil {
		return &volume_server_pb.VolumeDeleteResponse{Error: err.Error()}, nil
	}
	return &volume_server_pb.VolumeDeleteResponse{}, nil
}

// VolumeCopy copies a volume from one volume server to another.
func (s *VolumeGRPCServer) VolumeCopy(req *volume_server_pb.VolumeCopyRequest, stream grpc.ServerStreamingServer[volume_server_pb.VolumeCopyResponse]) error {
	if s.vs == nil || s.vs.store == nil {
		return status.Error(codes.Unavailable, "volume store not initialized")
	}
	if req.GetVolumeId() == 0 {
		return status.Error(codes.InvalidArgument, "invalid volume id")
	}
	if req.GetSourceDataNode() == "" {
		return status.Error(codes.InvalidArgument, "missing source data node")
	}

	vid := types.VolumeId(req.GetVolumeId())
	if _, ok := s.vs.store.GetVolume(vid); ok {
		return status.Errorf(codes.AlreadyExists, "volume %d already exists on target", req.GetVolumeId())
	}

	if err := s.vs.store.AllocateVolume(vid, req.GetCollection(), types.CurrentNeedleVersion); err != nil {
		return status.Errorf(codes.ResourceExhausted, "allocate target volume %d: %v", req.GetVolumeId(), err)
	}

	cleanupNeeded := true
	defer func() {
		if cleanupNeeded {
			_ = s.deleteVolume(vid)
		}
	}()

	if err := stream.Send(&volume_server_pb.VolumeCopyResponse{ProcessPercent: 0}); err != nil {
		return err
	}

	var sourceVolumeSize uint64
	dialOpt := grpc.WithTransportCredentials(insecure.NewCredentials())
	_ = withVolumeServerClient(req.GetSourceDataNode(), dialOpt, func(client volume_server_pb.VolumeServerClient) error {
		resp, err := client.VolumeStatus(stream.Context(), &volume_server_pb.VolumeStatusRequest{VolumeId: req.GetVolumeId()})
		if err != nil {
			return nil
		}
		sourceVolumeSize = resp.GetVolumeSize()
		return nil
	})

	processedBytes := uint64(0)
	lastProgress := float32(0)
	err := withVolumeServerClient(req.GetSourceDataNode(), dialOpt, func(client volume_server_pb.VolumeServerClient) error {
		readStream, err := client.ReadAllNeedles(stream.Context(), &volume_server_pb.ReadAllNeedlesRequest{VolumeId: req.GetVolumeId()})
		if err != nil {
			return err
		}
		for {
			item, recvErr := readStream.Recv()
			if recvErr == io.EOF {
				return nil
			}
			if recvErr != nil {
				return recvErr
			}

			if item.GetIsDeleted() {
				if delErr := s.vs.store.DeleteVolumeNeedle(vid, types.NeedleId(item.GetNeedleId())); delErr != nil {
					continue
				}
				continue
			}

			n := &needle.Needle{
				Id:       types.NeedleId(item.GetNeedleId()),
				Cookie:   types.Cookie(item.GetCookie()),
				Data:     append([]byte(nil), item.GetData()...),
				DataSize: uint32(len(item.GetData())),
			}
			if item.GetFileName() != "" {
				n.SetName(item.GetFileName())
			}
			if item.GetMime() != "" {
				n.SetMime(item.GetMime())
			}
			if _, _, writeErr := s.vs.store.WriteVolumeNeedle(vid, n); writeErr != nil {
				return writeErr
			}

			processedBytes += uint64(item.GetSize())
			progress := copyProgressPercent(processedBytes, sourceVolumeSize)
			if progress >= lastProgress+5 || progress == 100 {
				lastProgress = progress
				if sendErr := stream.Send(&volume_server_pb.VolumeCopyResponse{
					ProcessedBytes: processedBytes,
					ProcessPercent: progress,
				}); sendErr != nil {
					return sendErr
				}
			}
		}
	})
	if err != nil {
		return status.Errorf(codes.Internal, "volume copy failed: %v", err)
	}

	cleanupNeeded = false
	return stream.Send(&volume_server_pb.VolumeCopyResponse{
		ProcessedBytes: processedBytes,
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
	v, ok := s.vs.store.GetVolume(types.VolumeId(req.GetVolumeId()))
	if !ok {
		return status.Errorf(codes.NotFound, "volume %d not found", req.GetVolumeId())
	}

	for _, entry := range v.SnapshotLiveNeedleEntries() {
		n, err := v.GetNeedle(entry.NeedleId)
		if err != nil {
			return status.Errorf(codes.Internal, "read needle %d: %v", entry.NeedleId, err)
		}

		if err := stream.Send(&volume_server_pb.ReadAllNeedlesResponse{
			NeedleId:  uint64(entry.NeedleId),
			Cookie:    uint32(n.GetCookie()),
			Offset:    uint64(entry.Offset),
			Size:      uint32(entry.Size),
			Data:      append([]byte(nil), n.Data...),
			Mime:      n.GetMime(),
			FileName:  n.GetName(),
			IsDeleted: n.IsDeleted(),
		}); err != nil {
			return err
		}
	}

	return nil
}

func copyProgressPercent(processed, total uint64) float32 {
	if total == 0 {
		return 0
	}
	pct := float32(processed) * 100 / float32(total)
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

func (s *VolumeGRPCServer) deleteVolume(vid types.VolumeId) error {
	for _, dl := range s.vs.store.GetLocations() {
		if _, ok := dl.GetVolume(vid); ok {
			return dl.DeleteVolume(vid)
		}
	}
	return fmt.Errorf("volume %d not found", vid)
}

// ParseFileID parses a file ID string into its components.
func ParseFileID(fidStr string) (types.VolumeId, types.NeedleId, types.Cookie, error) {
	fid, err := types.ParseFileId(fidStr)
	if err != nil {
		return 0, 0, 0, err
	}
	return fid.VolumeId, fid.NeedleId, fid.Cookie, nil
}
