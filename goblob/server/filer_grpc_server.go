package server

import (
	"context"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/filer"
	"GoBlob/goblob/operation"
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
	if err := s.requireFiler(); err != nil {
		return nil, err
	}

	fullPath := joinDirectoryAndName(req.GetDirectory(), req.GetName())
	entry, err := s.fs.filer.FindEntry(ctx, fullPath)
	if err != nil {
		if err == filer.ErrNotFound {
			return nil, status.Error(codes.NotFound, "entry not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to find entry: %v", err)
	}

	pbEntry, err := entryToProto(entry)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert entry: %v", err)
	}

	return &filer_pb.LookupDirectoryEntryResponse{Entry: pbEntry}, nil
}

// CreateEntry creates a new entry.
func (s *FilerGRPCServer) CreateEntry(ctx context.Context, req *filer_pb.CreateEntryRequest) (*filer_pb.CreateEntryResponse, error) {
	if err := s.requireFiler(); err != nil {
		return nil, err
	}

	entry, err := protoToEntry(req.GetDirectory(), req.GetEntry())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid entry: %v", err)
	}

	if err := s.fs.filer.CreateEntry(ctx, entry); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create entry: %v", err)
	}

	s.fs.logBuffer.AppendEntry([]byte("create:" + string(entry.FullPath)))
	return &filer_pb.CreateEntryResponse{}, nil
}

// UpdateEntry updates an existing entry.
func (s *FilerGRPCServer) UpdateEntry(ctx context.Context, req *filer_pb.UpdateEntryRequest) (*filer_pb.UpdateEntryResponse, error) {
	if err := s.requireFiler(); err != nil {
		return nil, err
	}

	entry, err := protoToEntry(req.GetDirectory(), req.GetEntry())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid entry: %v", err)
	}

	if err := s.fs.filer.UpdateEntry(ctx, entry); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update entry: %v", err)
	}

	return &filer_pb.UpdateEntryResponse{}, nil
}

// DeleteEntry deletes an entry.
func (s *FilerGRPCServer) DeleteEntry(ctx context.Context, req *filer_pb.DeleteEntryRequest) (*filer_pb.DeleteEntryResponse, error) {
	if err := s.requireFiler(); err != nil {
		return nil, err
	}

	fullPath := joinDirectoryAndName(req.GetDirectory(), req.GetName())
	if err := s.fs.filer.DeleteEntry(ctx, fullPath); err != nil {
		if err == filer.ErrNotFound {
			return nil, status.Error(codes.NotFound, "entry not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to delete entry: %v", err)
	}

	s.fs.logBuffer.AppendEntry([]byte("delete:" + string(fullPath)))
	return &filer_pb.DeleteEntryResponse{}, nil
}

// AppendToEntry appends chunks metadata to an existing entry.
func (s *FilerGRPCServer) AppendToEntry(ctx context.Context, req *filer_pb.AppendToEntryRequest) (*filer_pb.AppendToEntryResponse, error) {
	if err := s.requireFiler(); err != nil {
		return nil, err
	}
	fullPath := joinDirectoryAndName(req.GetDirectory(), req.GetEntryName())
	entry, err := s.fs.filer.FindEntry(ctx, fullPath)
	if err != nil {
		if err == filer.ErrNotFound {
			return nil, status.Error(codes.NotFound, "entry not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to find entry: %v", err)
	}

	for _, c := range req.GetChunks() {
		if c == nil {
			continue
		}
		entry.Chunks = append(entry.Chunks, &filer.FileChunk{
			FileId:       c.GetFileId(),
			Offset:       c.GetOffset(),
			Size:         int64(c.GetSize()),
			ModifiedTsNs: c.GetModifiedTsNs(),
			ETag:         c.GetETag(),
			IsCompressed: c.GetIsCompressed(),
		})
	}

	if err := s.fs.filer.UpdateEntry(ctx, entry); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to append chunks: %v", err)
	}
	return &filer_pb.AppendToEntryResponse{}, nil
}

// AtomicRenameEntry renames an entry atomically from old path to new path.
func (s *FilerGRPCServer) AtomicRenameEntry(ctx context.Context, req *filer_pb.AtomicRenameEntryRequest) (*filer_pb.AtomicRenameEntryResponse, error) {
	if err := s.requireFiler(); err != nil {
		return nil, err
	}

	oldPath := joinDirectoryAndName(req.GetOldDirectory(), req.GetOldName())
	newPath := joinDirectoryAndName(req.GetNewDirectory(), req.GetNewName())

	entry, err := s.fs.filer.FindEntry(ctx, oldPath)
	if err != nil {
		if err == filer.ErrNotFound {
			return nil, status.Error(codes.NotFound, "entry not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to find source entry: %v", err)
	}

	entry.FullPath = newPath
	if err := s.fs.filer.UpdateEntry(ctx, entry); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to write destination entry: %v", err)
	}
	if err := s.fs.filer.DeleteEntry(ctx, oldPath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete source entry: %v", err)
	}

	s.fs.logBuffer.AppendEntry([]byte("rename:" + string(oldPath) + "->" + string(newPath)))
	return &filer_pb.AtomicRenameEntryResponse{}, nil
}

// StreamRenameEntry performs rename and streams resulting metadata event(s).
func (s *FilerGRPCServer) StreamRenameEntry(req *filer_pb.StreamRenameEntryRequest, stream filer_pb.FilerService_StreamRenameEntryServer) error {
	if _, err := s.AtomicRenameEntry(stream.Context(), &filer_pb.AtomicRenameEntryRequest{
		OldDirectory: req.GetOldDirectory(),
		OldName:      req.GetOldName(),
		NewDirectory: req.GetNewDirectory(),
		NewName:      req.GetNewName(),
	}); err != nil {
		return err
	}

	newPath := joinDirectoryAndName(req.GetNewDirectory(), req.GetNewName())
	entry, err := s.fs.filer.FindEntry(stream.Context(), newPath)
	if err != nil {
		return err
	}
	pbEntry, err := entryToProto(entry)
	if err != nil {
		return err
	}
	return stream.Send(&filer_pb.StreamRenameEntryResponse{
		Entry:             pbEntry,
		EventNotification: "rename",
		NewParentPath:     req.GetNewDirectory(),
	})
}

// ListEntries streams entries in a directory.
func (s *FilerGRPCServer) ListEntries(req *filer_pb.ListEntriesRequest, stream filer_pb.FilerService_ListEntriesServer) error {
	if err := s.requireFiler(); err != nil {
		return err
	}

	directory := joinDirectoryAndName(req.GetDirectory(), "")
	limit := int64(req.GetLimit())
	if limit <= 0 {
		limit = 10000
	}

	var sendErr error
	var listErr error
	if req.GetPrefix() != "" {
		_, listErr = s.fs.filer.Store.ListDirectoryPrefixedEntries(
			stream.Context(),
			directory,
			req.GetStartFromFileName(),
			req.GetInclusiveStartFrom(),
			limit,
			req.GetPrefix(),
			func(entry *filer.Entry) bool {
				if sendErr != nil {
					return false
				}
				sendErr = s.sendListEntry(stream, entry)
				return sendErr == nil
			},
		)
	} else {
		_, listErr = s.fs.filer.Store.ListDirectoryEntries(
			stream.Context(),
			directory,
			req.GetStartFromFileName(),
			req.GetInclusiveStartFrom(),
			limit,
			func(entry *filer.Entry) bool {
				if sendErr != nil {
					return false
				}
				sendErr = s.sendListEntry(stream, entry)
				return sendErr == nil
			},
		)
	}
	if sendErr != nil {
		return sendErr
	}
	if listErr != nil {
		return status.Errorf(codes.Internal, "failed to list entries: %v", listErr)
	}
	return nil
}

func (s *FilerGRPCServer) sendListEntry(stream filer_pb.FilerService_ListEntriesServer, entry *filer.Entry) error {
	pbEntry, err := entryToProto(entry)
	if err != nil {
		return nil
	}
	return stream.Send(&filer_pb.ListEntriesResponse{Entry: pbEntry})
}

// AssignVolume assigns a file ID from the master server.
func (s *FilerGRPCServer) AssignVolume(ctx context.Context, req *filer_pb.AssignVolumeRequest) (*filer_pb.AssignVolumeResponse, error) {
	if err := s.requireFiler(); err != nil {
		return nil, err
	}

	fid, err := s.fs.filer.AssignVolume(ctx, req.GetCollection(), req.GetReplication(), req.GetTtl(), req.GetDataCenter(), req.GetRack())
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
	if err := s.requireFiler(); err != nil {
		return nil, err
	}
	if len(s.fs.filer.MasterAddresses) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "no master configured")
	}

	resp := &filer_pb.LookupVolumeResponse{
		LocationsMap: make(map[string]*filer_pb.Locations, len(req.GetVolumeIds())),
	}

	masterAddr := s.fs.filer.MasterAddresses[0]
	for _, token := range req.GetVolumeIds() {
		vid, err := parseLookupVolumeToken(token)
		if err != nil {
			resp.LocationsMap[token] = &filer_pb.Locations{Error: err.Error()}
			continue
		}

		locs, err := operation.LookupVolumeId(ctx, masterAddr, vid)
		if err != nil {
			resp.LocationsMap[token] = &filer_pb.Locations{Error: err.Error()}
			continue
		}

		pbLocs := make([]*filer_pb.Location, 0, len(locs))
		for _, l := range locs {
			pbLocs = append(pbLocs, &filer_pb.Location{
				Url:       l.Url,
				PublicUrl: l.PublicUrl,
			})
		}
		resp.LocationsMap[token] = &filer_pb.Locations{Locations: pbLocs}
	}
	return resp, nil
}

// SubscribeMetadata streams metadata events from the local log buffer.
func (s *FilerGRPCServer) SubscribeMetadata(req *filer_pb.SubscribeMetadataRequest, stream filer_pb.FilerService_SubscribeMetadataServer) error {
	if err := s.requireFiler(); err != nil {
		return err
	}
	sinceNs := req.GetSinceNs()
	pathPrefix := req.GetPathPrefix()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		default:
		}

		result := s.fs.logBuffer.ReadFromBuffer(sinceNs)
		if len(result.Events) == 0 {
			// Wait for a notify from the log buffer instead of busy-sleeping.
			// listenersCond is broadcast by notifyFn whenever a new entry is appended.
			s.fs.listenersMu.Lock()
			// Re-check after acquiring the lock to avoid missing a broadcast that
			// arrived between the ReadFromBuffer call and the Wait below.
			if len(s.fs.logBuffer.ReadFromBuffer(sinceNs).Events) == 0 {
				s.fs.listenersCond.Wait()
			}
			s.fs.listenersMu.Unlock()
			continue
		}
		for _, evt := range result.Events {
			resp := eventBytesToSubscribeResponse(evt)
			if resp == nil {
				continue
			}
			if pathPrefix != "" && !strings.HasPrefix(resp.GetDirectory(), pathPrefix) {
				continue
			}
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
		if result.NextTs > sinceNs {
			sinceNs = result.NextTs
		}
	}
}

// SubscribeLocalMetadata uses the same implementation as SubscribeMetadata.
func (s *FilerGRPCServer) SubscribeLocalMetadata(req *filer_pb.SubscribeMetadataRequest, stream filer_pb.FilerService_SubscribeLocalMetadataServer) error {
	return s.SubscribeMetadata(req, stream)
}

// KvGet gets a value from filer KV store.
func (s *FilerGRPCServer) KvGet(ctx context.Context, req *filer_pb.KvGetRequest) (*filer_pb.KvGetResponse, error) {
	if err := s.requireFiler(); err != nil {
		return nil, err
	}
	value, err := s.fs.filer.Store.KvGet(ctx, req.GetKey())
	if err != nil {
		return &filer_pb.KvGetResponse{Error: err.Error()}, nil
	}
	return &filer_pb.KvGetResponse{Value: value}, nil
}

// KvPut puts a value into filer KV store.
func (s *FilerGRPCServer) KvPut(ctx context.Context, req *filer_pb.KvPutRequest) (*filer_pb.KvPutResponse, error) {
	if err := s.requireFiler(); err != nil {
		return nil, err
	}
	if err := s.fs.filer.Store.KvPut(ctx, req.GetKey(), req.GetValue()); err != nil {
		return &filer_pb.KvPutResponse{Error: err.Error()}, nil
	}
	return &filer_pb.KvPutResponse{}, nil
}

// KvDelete deletes a key from filer KV store.
func (s *FilerGRPCServer) KvDelete(ctx context.Context, req *filer_pb.KvDeleteRequest) (*filer_pb.KvDeleteResponse, error) {
	if err := s.requireFiler(); err != nil {
		return nil, err
	}
	if err := s.fs.filer.Store.KvDelete(ctx, req.GetKey()); err != nil {
		return &filer_pb.KvDeleteResponse{Error: err.Error()}, nil
	}
	return &filer_pb.KvDeleteResponse{}, nil
}

// GetFilerConfiguration returns current filer runtime configuration.
func (s *FilerGRPCServer) GetFilerConfiguration(ctx context.Context, req *filer_pb.GetFilerConfigurationRequest) (*filer_pb.GetFilerConfigurationResponse, error) {
	_ = ctx
	_ = req
	masters := strings.Join(s.fs.option.Masters, ",")
	return &filer_pb.GetFilerConfigurationResponse{
		Masters:        masters,
		Replication:    s.fs.option.DefaultReplication,
		Collection:     s.fs.option.DefaultCollection,
		MaxMb:          uint32(s.fs.option.MaxFileSizeMB),
		DirBuckets:     s.fs.option.BucketsFolder,
		DataCenter:     s.fs.option.DataCenter,
		Rack:           s.fs.option.Rack,
		MetricsAddress: fmt.Sprintf("%s:%d", s.fs.option.Host, s.fs.option.Port),
	}, nil
}

// DistributedLock acquires/renews a named lock.
func (s *FilerGRPCServer) DistributedLock(ctx context.Context, req *filer_pb.DistributedLockRequest) (*filer_pb.DistributedLockResponse, error) {
	if err := s.requireFiler(); err != nil {
		return nil, err
	}
	if s.fs.dlm == nil {
		s.fs.dlm = filer.NewDistributedLockManager(s.fs.filer.Store, s.fs.logger)
	}

	result, err := s.fs.dlm.TryLock(ctx, req.GetName(), req.GetOwner(), req.GetExpireNs(), req.GetRenewToken())
	if err != nil {
		return &filer_pb.DistributedLockResponse{Error: err.Error()}, nil
	}
	if !result.Acquired {
		return &filer_pb.DistributedLockResponse{Error: "lock held by " + result.CurrentOwner}, nil
	}
	return &filer_pb.DistributedLockResponse{RenewToken: result.RenewToken}, nil
}

// DistributedUnlock releases a named lock.
func (s *FilerGRPCServer) DistributedUnlock(ctx context.Context, req *filer_pb.DistributedUnlockRequest) (*filer_pb.DistributedUnlockResponse, error) {
	if err := s.requireFiler(); err != nil {
		return nil, err
	}
	if s.fs.dlm == nil {
		s.fs.dlm = filer.NewDistributedLockManager(s.fs.filer.Store, s.fs.logger)
	}
	if err := s.fs.dlm.Unlock(ctx, req.GetName(), req.GetRenewToken()); err != nil {
		return &filer_pb.DistributedUnlockResponse{Error: err.Error()}, nil
	}
	return &filer_pb.DistributedUnlockResponse{}, nil
}

func parseLookupVolumeToken(value string) (types.VolumeId, error) {
	if value == "" {
		return 0, fmt.Errorf("empty volume id")
	}
	if fid, err := types.ParseFileId(value); err == nil {
		return fid.VolumeId, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid volume id %q", value)
	}
	return types.VolumeId(parsed), nil
}

func (s *FilerGRPCServer) requireFiler() error {
	if s.fs != nil && s.fs.filer != nil && s.fs.filer.Store != nil {
		return nil
	}
	return status.Error(codes.Unavailable, "filer not initialized")
}

// entryToProto converts a filer.Entry to proto.
func entryToProto(entry *filer.Entry) (*filer_pb.Entry, error) {
	if entry == nil {
		return nil, fmt.Errorf("entry is nil")
	}

	_, name := entry.FullPath.DirAndName()
	pbChunks := make([]*filer_pb.FileChunk, 0, len(entry.Chunks))
	for _, c := range entry.Chunks {
		if c == nil {
			continue
		}
		size := c.Size
		if size < 0 {
			size = 0
		}
		pbChunks = append(pbChunks, &filer_pb.FileChunk{
			FileId:       c.FileId,
			Offset:       c.Offset,
			Size:         uint64(size),
			ModifiedTsNs: c.ModifiedTsNs,
			ETag:         c.ETag,
			IsCompressed: c.IsCompressed,
			Fid:          c.FileId,
		})
	}

	extended := make(map[string][]byte, len(entry.Extended))
	for k, v := range entry.Extended {
		copied := make([]byte, len(v))
		copy(copied, v)
		extended[k] = copied
	}

	var remote *filer_pb.RemoteEntry
	if entry.Remote != nil {
		remote = &filer_pb.RemoteEntry{
			StorageName: entry.Remote.StorageName,
			RemotePath:  entry.Remote.Key,
			RemoteETag:  entry.Remote.ETag,
		}
	}

	content := make([]byte, len(entry.Content))
	copy(content, entry.Content)
	hlID := make([]byte, len(entry.HardLinkId))
	copy(hlID, entry.HardLinkId)

	return &filer_pb.Entry{
		Name:            name,
		IsDirectory:     entry.IsDirectory(),
		Chunks:          pbChunks,
		Attributes:      entryAttrToProto(entry.Attr),
		Extended:        extended,
		Content:         content,
		HardLinkId:      hlID,
		HardLinkCounter: entry.HardLinkCounter,
		Remote:          remote,
	}, nil
}

// protoToEntry converts proto to filer.Entry.
func protoToEntry(directory string, pbEntry *filer_pb.Entry) (*filer.Entry, error) {
	if pbEntry == nil {
		return nil, fmt.Errorf("entry is nil")
	}

	attr := protoAttrToEntry(pbEntry.GetAttributes(), pbEntry.GetIsDirectory())
	chunks := make([]*filer.FileChunk, 0, len(pbEntry.GetChunks()))
	for _, c := range pbEntry.GetChunks() {
		if c == nil {
			continue
		}
		chunks = append(chunks, &filer.FileChunk{
			FileId:       c.GetFileId(),
			Offset:       c.GetOffset(),
			Size:         int64(c.GetSize()),
			ModifiedTsNs: c.GetModifiedTsNs(),
			ETag:         c.GetETag(),
			IsCompressed: c.GetIsCompressed(),
		})
	}

	extended := make(map[string][]byte, len(pbEntry.GetExtended()))
	for k, v := range pbEntry.GetExtended() {
		copied := make([]byte, len(v))
		copy(copied, v)
		extended[k] = copied
	}

	var remote *filer.RemoteEntry
	if pbRemote := pbEntry.GetRemote(); pbRemote != nil {
		remote = &filer.RemoteEntry{
			StorageName: pbRemote.GetStorageName(),
			Key:         pbRemote.GetRemotePath(),
			ETag:        pbRemote.GetRemoteETag(),
		}
	}

	content := make([]byte, len(pbEntry.GetContent()))
	copy(content, pbEntry.GetContent())
	hlID := make([]byte, len(pbEntry.GetHardLinkId()))
	copy(hlID, pbEntry.GetHardLinkId())

	return &filer.Entry{
		FullPath:        joinDirectoryAndName(directory, pbEntry.GetName()),
		Attr:            attr,
		Chunks:          chunks,
		Content:         content,
		Extended:        extended,
		HardLinkId:      hlID,
		HardLinkCounter: pbEntry.GetHardLinkCounter(),
		Remote:          remote,
	}, nil
}

func entryAttrToProto(attr filer.Attr) *filer_pb.FuseAttributes {
	return &filer_pb.FuseAttributes{
		FileSize:      attr.FileSize,
		Mtime:         attr.Mtime.Unix(),
		FileMode:      uint32(attr.Mode),
		Uid:           attr.Uid,
		Gid:           attr.Gid,
		Crtime:        attr.Crtime.Unix(),
		Mime:          attr.Mime,
		Replication:   attr.Replication,
		Collection:    attr.Collection,
		TtlSec:        attr.TtlSec,
		SymlinkTarget: attr.SymlinkTarget,
		DiskType:      string(attr.DiskType),
		Inode:         attr.INode,
	}
}

func protoAttrToEntry(pbAttr *filer_pb.FuseAttributes, isDir bool) filer.Attr {
	attr := filer.Attr{
		Mode: 0644,
	}
	if pbAttr != nil {
		attr = filer.Attr{
			Mode:          os.FileMode(pbAttr.GetFileMode()),
			Uid:           pbAttr.GetUid(),
			Gid:           pbAttr.GetGid(),
			Mtime:         time.Unix(pbAttr.GetMtime(), 0),
			Crtime:        time.Unix(pbAttr.GetCrtime(), 0),
			Mime:          pbAttr.GetMime(),
			Replication:   pbAttr.GetReplication(),
			Collection:    pbAttr.GetCollection(),
			TtlSec:        pbAttr.GetTtlSec(),
			DiskType:      types.DiskType(pbAttr.GetDiskType()),
			FileSize:      pbAttr.GetFileSize(),
			INode:         pbAttr.GetInode(),
			SymlinkTarget: pbAttr.GetSymlinkTarget(),
		}
	}
	if isDir {
		attr.Mode |= os.ModeDir
	}
	return attr
}

func joinDirectoryAndName(directory, name string) filer.FullPath {
	dir := directory
	if dir == "" {
		dir = "/"
	}
	if dir[0] != '/' {
		dir = "/" + dir
	}
	if name == "" {
		return filer.FullPath(path.Clean(dir))
	}
	if dir == "/" {
		return filer.FullPath(path.Clean("/" + name))
	}
	return filer.FullPath(path.Clean(dir + "/" + name))
}

func eventBytesToSubscribeResponse(evt []byte) *filer_pb.SubscribeMetadataResponse {
	raw := string(evt)
	op, target, ok := strings.Cut(raw, ":")
	if !ok || target == "" {
		return nil
	}
	full := filer.FullPath(target)
	dir, name := full.DirAndName()
	ts := time.Now().UnixNano()

	resp := &filer_pb.SubscribeMetadataResponse{
		Directory:         string(dir),
		TsNs:              ts,
		EventNotification: &filer_pb.EventNotification{},
	}

	switch op {
	case "create":
		resp.EventNotification.NewEntry = &filer_pb.Entry{Name: name}
	case "delete":
		resp.EventNotification.OldEntry = &filer_pb.Entry{Name: name}
	case "rename":
		parts := strings.SplitN(target, "->", 2)
		if len(parts) == 2 {
			oldFp := filer.FullPath(parts[0])
			newFp := filer.FullPath(parts[1])
			oldDir, oldName := oldFp.DirAndName()
			newDir, newName := newFp.DirAndName()
			resp.Directory = string(oldDir)
			resp.EventNotification.OldEntry = &filer_pb.Entry{Name: oldName}
			resp.EventNotification.NewEntry = &filer_pb.Entry{Name: newName}
			resp.EventNotification.NewParentPath = string(newDir)
		}
	default:
		return nil
	}
	return resp
}
