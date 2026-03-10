package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"GoBlob/goblob/filer"
	"GoBlob/goblob/pb/filer_pb"
)

// FilerGRPCServer implements the gRPC FilerServiceServer interface.
type FilerGRPCServer struct {
	filer_pb.UnimplementedFilerServiceServer
	fs *FilerServer
}

// NewFilerGRPCServer creates a new gRPC server.
func NewFilerGRPCServer(fs *FilerServer) *FilerGRPCServer {
	return &FilerGRPCServer{fs: fs}
}

// LookupDirectoryEntry looks up a directory entry by path.
func (s *FilerGRPCServer) LookupDirectoryEntry(ctx context.Context, req *filer_pb.LookupDirectoryEntryRequest) (*filer_pb.LookupDirectoryEntryResponse, error) {
	if s.fs.filer == nil {
		return nil, status.Error(codes.Unavailable, "filer not initialized")
	}

	entry, err := s.fs.filer.FindEntry(ctx, filer.FullPath(req.Directory))
	if err != nil {
		if err == filer.ErrNotFound {
			return nil, status.Error(codes.NotFound, "entry not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to find entry: %v", err)
	}

	// Convert entry to proto
	pbEntry, err := entryToProto(entry)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert entry: %v", err)
	}

	return &filer_pb.LookupDirectoryEntryResponse{
		Entry: pbEntry,
	}, nil
}

// CreateEntry creates a new entry.
func (s *FilerGRPCServer) CreateEntry(ctx context.Context, req *filer_pb.CreateEntryRequest) (*filer_pb.CreateEntryResponse, error) {
	if s.fs.filer == nil {
		return nil, status.Error(codes.Unavailable, "filer not initialized")
	}

	// Convert proto to entry
	entry, err := protoToEntry(req.Directory, req.Entry)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid entry: %v", err)
	}

	err = s.fs.filer.CreateEntry(ctx, entry)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create entry: %v", err)
	}

	// Append to log buffer
	s.fs.logBuffer.AppendEntry([]byte("create:" + req.Directory))

	return &filer_pb.CreateEntryResponse{}, nil
}

// UpdateEntry updates an existing entry.
func (s *FilerGRPCServer) UpdateEntry(ctx context.Context, req *filer_pb.UpdateEntryRequest) (*filer_pb.UpdateEntryResponse, error) {
	if s.fs.filer == nil {
		return nil, status.Error(codes.Unavailable, "filer not initialized")
	}

	// Convert proto to entry
	entry, err := protoToEntry(req.Directory, req.Entry)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid entry: %v", err)
	}

	err = s.fs.filer.UpdateEntry(ctx, entry)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update entry: %v", err)
	}

	return &filer_pb.UpdateEntryResponse{}, nil
}

// DeleteEntry deletes an entry.
func (s *FilerGRPCServer) DeleteEntry(ctx context.Context, req *filer_pb.DeleteEntryRequest) (*filer_pb.DeleteEntryResponse, error) {
	if s.fs.filer == nil {
		return nil, status.Error(codes.Unavailable, "filer not initialized")
	}

	err := s.fs.filer.DeleteEntry(ctx, filer.FullPath(req.Directory))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete entry: %v", err)
	}

	// Append to log buffer
	s.fs.logBuffer.AppendEntry([]byte("delete:" + req.Directory))

	return &filer_pb.DeleteEntryResponse{}, nil
}

// ListEntries streams entries in a directory.
func (s *FilerGRPCServer) ListEntries(req *filer_pb.ListEntriesRequest, stream filer_pb.FilerService_ListEntriesServer) error {
	if s.fs.filer == nil {
		return status.Error(codes.Unavailable, "filer not initialized")
	}

	entries, _, err := s.fs.filer.ListDirectoryEntries(stream.Context(), filer.FullPath(req.Directory), "", 0)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to list entries: %v", err)
	}

	for _, entry := range entries {
		pbEntry, err := entryToProto(entry)
		if err != nil {
			continue
		}

		resp := &filer_pb.ListEntriesResponse{
			Entry: pbEntry,
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}

	return nil
}

// AssignVolume assigns a file ID from the master server.
func (s *FilerGRPCServer) AssignVolume(ctx context.Context, req *filer_pb.AssignVolumeRequest) (*filer_pb.AssignVolumeResponse, error) {
	if s.fs.filer == nil {
		return nil, status.Error(codes.Unavailable, "filer not initialized")
	}

	fid, err := s.fs.filer.AssignVolume(ctx, req.Collection, req.Replication, req.Ttl, req.DataCenter, req.Rack)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to assign volume: %v", err)
	}

	return &filer_pb.AssignVolumeResponse{
		FileId: fid.Fid,
		Url:    fid.Url,
		Count:  int32(fid.Count),
	}, nil
}

// LookupVolume looks up volume locations from the master server.
func (s *FilerGRPCServer) LookupVolume(ctx context.Context, req *filer_pb.LookupVolumeRequest) (*filer_pb.LookupVolumeResponse, error) {
	// For Phase 4, return empty response
	// Full implementation in Phase 5 with master client
	return &filer_pb.LookupVolumeResponse{}, nil
}

// entryToProto converts a filer.Entry to proto.
func entryToProto(entry *filer.Entry) (*filer_pb.Entry, error) {
	// TODO: Implement full conversion
	return &filer_pb.Entry{}, nil
}

// protoToEntry converts proto to filer.Entry.
func protoToEntry(directory string, pbEntry *filer_pb.Entry) (*filer.Entry, error) {
	// TODO: Implement full conversion
	return &filer.Entry{
		FullPath: filer.FullPath(directory),
	}, nil
}
