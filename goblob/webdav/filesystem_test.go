package webdav

import (
	"context"
	"io"
	"os"
	"path"
	"sort"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"GoBlob/goblob/pb/filer_pb"
)

func TestFilerFileSystemReadWrite(t *testing.T) {
	fs := NewFilerFileSystem(t.TempDir())
	ctx := context.Background()

	if err := fs.Mkdir(ctx, "/docs", 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	f, err := fs.OpenFile(ctx, "/docs/hello.txt", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenFile create: %v", err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close write: %v", err)
	}

	f, err = fs.OpenFile(ctx, "/docs/hello.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile read: %v", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close read: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("read=%q want hello", data)
	}
}

func TestAddress(t *testing.T) {
	if got := Address("", 4333); got != ":4333" {
		t.Fatalf("Address empty host=%q", got)
	}
	if got := Address("127.0.0.1", 4333); got != "127.0.0.1:4333" {
		t.Fatalf("Address=%q", got)
	}
}

type fakeFiler struct {
	entries map[string]*filer_pb.Entry
}

func newFakeFiler() *fakeFiler {
	ff := &fakeFiler{entries: map[string]*filer_pb.Entry{}}
	ff.entries["/"] = &filer_pb.Entry{Name: "", IsDirectory: true, Attributes: &filer_pb.FuseAttributes{FileMode: uint32(os.ModeDir | 0o755)}}
	return ff
}

func (f *fakeFiler) LookupDirectoryEntry(ctx context.Context, in *filer_pb.LookupDirectoryEntryRequest, opts ...grpc.CallOption) (*filer_pb.LookupDirectoryEntryResponse, error) {
	_ = ctx
	_ = opts
	full := path.Join(in.GetDirectory(), in.GetName())
	entry, ok := f.entries[full]
	if !ok {
		return nil, status.Error(codes.NotFound, "not found")
	}
	return &filer_pb.LookupDirectoryEntryResponse{Entry: clonePB(entry)}, nil
}

func (f *fakeFiler) CreateEntry(ctx context.Context, in *filer_pb.CreateEntryRequest, opts ...grpc.CallOption) (*filer_pb.CreateEntryResponse, error) {
	_ = ctx
	_ = opts
	full := path.Join(in.GetDirectory(), in.GetEntry().GetName())
	if _, ok := f.entries[full]; ok {
		return nil, status.Error(codes.AlreadyExists, "exists")
	}
	f.entries[full] = clonePB(in.GetEntry())
	return &filer_pb.CreateEntryResponse{}, nil
}

func (f *fakeFiler) UpdateEntry(ctx context.Context, in *filer_pb.UpdateEntryRequest, opts ...grpc.CallOption) (*filer_pb.UpdateEntryResponse, error) {
	_ = ctx
	_ = opts
	full := path.Join(in.GetDirectory(), in.GetEntry().GetName())
	f.entries[full] = clonePB(in.GetEntry())
	return &filer_pb.UpdateEntryResponse{}, nil
}

func (f *fakeFiler) DeleteEntry(ctx context.Context, in *filer_pb.DeleteEntryRequest, opts ...grpc.CallOption) (*filer_pb.DeleteEntryResponse, error) {
	_ = ctx
	_ = opts
	delete(f.entries, path.Join(in.GetDirectory(), in.GetName()))
	return &filer_pb.DeleteEntryResponse{}, nil
}

func (f *fakeFiler) ListEntries(ctx context.Context, in *filer_pb.ListEntriesRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[filer_pb.ListEntriesResponse], error) {
	_ = ctx
	_ = opts
	dir := path.Clean(in.GetDirectory())
	var list []*filer_pb.ListEntriesResponse
	keys := make([]string, 0)
	for k := range f.entries {
		if k == dir || path.Dir(k) != dir {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		list = append(list, &filer_pb.ListEntriesResponse{Entry: clonePB(f.entries[k])})
	}
	return &fakeListStream{items: list}, nil
}

func (f *fakeFiler) AtomicRenameEntry(ctx context.Context, in *filer_pb.AtomicRenameEntryRequest, opts ...grpc.CallOption) (*filer_pb.AtomicRenameEntryResponse, error) {
	_ = ctx
	_ = opts
	oldFull := path.Join(in.GetOldDirectory(), in.GetOldName())
	entry, ok := f.entries[oldFull]
	if !ok {
		return nil, status.Error(codes.NotFound, "not found")
	}
	delete(f.entries, oldFull)
	entry.Name = in.GetNewName()
	f.entries[path.Join(in.GetNewDirectory(), in.GetNewName())] = entry
	return &filer_pb.AtomicRenameEntryResponse{}, nil
}

type fakeListStream struct {
	grpc.ServerStreamingClient[filer_pb.ListEntriesResponse]
	items []*filer_pb.ListEntriesResponse
	idx   int
}

func (s *fakeListStream) Recv() (*filer_pb.ListEntriesResponse, error) {
	if s.idx >= len(s.items) {
		return nil, io.EOF
	}
	item := s.items[s.idx]
	s.idx++
	return item, nil
}

func clonePB(entry *filer_pb.Entry) *filer_pb.Entry {
	if entry == nil {
		return nil
	}
	cp := *entry //nolint:govet
	cp.Content = append([]byte(nil), entry.GetContent()...)
	return &cp
}

func TestFilerFileSystemWithFilerClient(t *testing.T) {
	client := newFakeFiler()
	fs := NewFilerFileSystemWithClient("/webdav", client)
	if err := fs.ensureRoot(context.Background()); err != nil {
		t.Fatalf("ensureRoot: %v", err)
	}

	ctx := context.Background()
	if err := fs.Mkdir(ctx, "/docs", 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	f, err := fs.OpenFile(ctx, "/docs/a.txt", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("OpenFile create: %v", err)
	}
	if _, err := f.Write([]byte("abc")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rf, err := fs.OpenFile(ctx, "/docs/a.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile read: %v", err)
	}
	data, err := io.ReadAll(rf)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "abc" {
		t.Fatalf("data=%q want abc", data)
	}
	if err := rf.Close(); err != nil {
		t.Fatalf("Close read: %v", err)
	}
}
