package async

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/filer_pb"
)

type fakeClient struct {
	entries   map[string]*filer_pb.Entry
	stream    [](*filer_pb.SubscribeMetadataResponse)
	lookupURL string
	assignURL string
	assignSeq uint64
}

func newFakeClient() *fakeClient {
	return &fakeClient{entries: map[string]*filer_pb.Entry{}}
}

func key(directory, name string) string { return cleanDir(directory) + "/" + name }

func (c *fakeClient) SubscribeMetadata(ctx context.Context, in *filer_pb.SubscribeMetadataRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[filer_pb.SubscribeMetadataResponse], error) {
	_ = ctx
	_ = in
	_ = opts
	return &fakeStream{events: c.stream}, nil
}

func (c *fakeClient) LookupDirectoryEntry(ctx context.Context, in *filer_pb.LookupDirectoryEntryRequest, opts ...grpc.CallOption) (*filer_pb.LookupDirectoryEntryResponse, error) {
	_ = ctx
	_ = opts
	entry, ok := c.entries[key(in.GetDirectory(), in.GetName())]
	if !ok {
		return nil, status.Error(codes.NotFound, "entry not found")
	}
	return &filer_pb.LookupDirectoryEntryResponse{Entry: cloneEntry(entry)}, nil
}

func (c *fakeClient) CreateEntry(ctx context.Context, in *filer_pb.CreateEntryRequest, opts ...grpc.CallOption) (*filer_pb.CreateEntryResponse, error) {
	_ = ctx
	_ = opts
	k := key(in.GetDirectory(), in.GetEntry().GetName())
	if _, ok := c.entries[k]; ok {
		return nil, status.Error(codes.AlreadyExists, "exists")
	}
	c.entries[k] = cloneEntry(in.GetEntry())
	return &filer_pb.CreateEntryResponse{}, nil
}

func (c *fakeClient) UpdateEntry(ctx context.Context, in *filer_pb.UpdateEntryRequest, opts ...grpc.CallOption) (*filer_pb.UpdateEntryResponse, error) {
	_ = ctx
	_ = opts
	k := key(in.GetDirectory(), in.GetEntry().GetName())
	c.entries[k] = cloneEntry(in.GetEntry())
	return &filer_pb.UpdateEntryResponse{}, nil
}

func (c *fakeClient) DeleteEntry(ctx context.Context, in *filer_pb.DeleteEntryRequest, opts ...grpc.CallOption) (*filer_pb.DeleteEntryResponse, error) {
	_ = ctx
	_ = opts
	k := key(in.GetDirectory(), in.GetName())
	delete(c.entries, k)
	return &filer_pb.DeleteEntryResponse{}, nil
}

func (c *fakeClient) LookupVolume(ctx context.Context, in *filer_pb.LookupVolumeRequest, opts ...grpc.CallOption) (*filer_pb.LookupVolumeResponse, error) {
	_ = ctx
	_ = opts
	out := &filer_pb.LookupVolumeResponse{LocationsMap: map[string]*filer_pb.Locations{}}
	for _, token := range in.GetVolumeIds() {
		if c.lookupURL == "" {
			out.LocationsMap[token] = &filer_pb.Locations{}
			continue
		}
		out.LocationsMap[token] = &filer_pb.Locations{
			Locations: []*filer_pb.Location{{Url: c.lookupURL}},
		}
	}
	return out, nil
}

func (c *fakeClient) AssignVolume(ctx context.Context, in *filer_pb.AssignVolumeRequest, opts ...grpc.CallOption) (*filer_pb.AssignVolumeResponse, error) {
	_ = ctx
	_ = in
	_ = opts
	seq := atomic.AddUint64(&c.assignSeq, 1)
	fid := types.FileId{VolumeId: 9, NeedleId: types.NeedleId(seq), Cookie: 123}
	return &filer_pb.AssignVolumeResponse{
		FileId: fid.String(),
		Url:    c.assignURL,
	}, nil
}

type fakeStream struct {
	grpc.ServerStreamingClient[filer_pb.SubscribeMetadataResponse]
	events []*filer_pb.SubscribeMetadataResponse
	idx    int
}

func (s *fakeStream) Recv() (*filer_pb.SubscribeMetadataResponse, error) {
	if s.idx >= len(s.events) {
		return nil, io.EOF
	}
	ev := s.events[s.idx]
	s.idx++
	return ev, nil
}

func resetStatuses() {
	statusMu.Lock()
	defer statusMu.Unlock()
	statuses = map[string]Status{}
}

func TestReplicateUpsertAndDelete(t *testing.T) {
	resetStatuses()
	source := newFakeClient()
	target := newFakeClient()
	source.entries[key("/bucket", "obj")] = &filer_pb.Entry{
		Name:       "obj",
		Attributes: &filer_pb.FuseAttributes{Mtime: time.Now().Add(-10 * time.Second).Unix()},
		Content:    []byte("data"),
	}

	r, err := NewReplicator(Config{
		SourceCluster: "a",
		SourceFiler:   "127.0.0.1:8888",
		TargetCluster: "b",
		TargetFiler:   "127.0.0.1:9888",
	})
	if err != nil {
		t.Fatalf("NewReplicator: %v", err)
	}
	r.SetClients(source, target)

	eventTs := time.Now().UnixNano()
	createEvent := &filer_pb.SubscribeMetadataResponse{
		Directory: "/bucket",
		TsNs:      eventTs,
		EventNotification: &filer_pb.EventNotification{
			NewEntry: &filer_pb.Entry{Name: "obj"},
		},
	}
	if err := r.handleEvent(context.Background(), createEvent); err != nil {
		t.Fatalf("handleEvent create: %v", err)
	}
	if _, ok := target.entries[key("/bucket", "obj")]; !ok {
		t.Fatal("expected replicated entry on target")
	}

	deleteEvent := &filer_pb.SubscribeMetadataResponse{
		Directory: "/bucket",
		TsNs:      eventTs,
		EventNotification: &filer_pb.EventNotification{
			OldEntry: &filer_pb.Entry{Name: "obj"},
		},
	}
	if err := r.handleEvent(context.Background(), deleteEvent); err != nil {
		t.Fatalf("handleEvent delete: %v", err)
	}
	if _, ok := target.entries[key("/bucket", "obj")]; ok {
		t.Fatal("expected target entry deleted")
	}
}

func TestReplicateDeleteSkipsNewerTarget(t *testing.T) {
	source := newFakeClient()
	target := newFakeClient()
	target.entries[key("/bucket", "obj")] = &filer_pb.Entry{
		Name:       "obj",
		Attributes: &filer_pb.FuseAttributes{Mtime: time.Now().Unix()},
	}

	r, err := NewReplicator(Config{
		SourceCluster: "a",
		SourceFiler:   "127.0.0.1:8888",
		TargetCluster: "b",
		TargetFiler:   "127.0.0.1:9888",
	})
	if err != nil {
		t.Fatalf("NewReplicator: %v", err)
	}
	r.SetClients(source, target)

	event := &filer_pb.SubscribeMetadataResponse{
		Directory: "/bucket",
		TsNs:      time.Now().Add(-time.Hour).UnixNano(),
		EventNotification: &filer_pb.EventNotification{
			OldEntry: &filer_pb.Entry{Name: "obj"},
		},
	}
	if err := r.handleEvent(context.Background(), event); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}
	if _, ok := target.entries[key("/bucket", "obj")]; !ok {
		t.Fatal("expected target entry to be kept (newer than delete event)")
	}
}

func TestStartConsumesStream(t *testing.T) {
	resetStatuses()
	source := newFakeClient()
	target := newFakeClient()
	source.entries[key("/bucket", "obj")] = &filer_pb.Entry{Name: "obj", Attributes: &filer_pb.FuseAttributes{Mtime: time.Now().Unix()}}
	source.stream = []*filer_pb.SubscribeMetadataResponse{
		{
			Directory: "/bucket",
			TsNs:      time.Now().Add(-time.Second).UnixNano(),
			EventNotification: &filer_pb.EventNotification{
				NewEntry: &filer_pb.Entry{Name: "obj"},
			},
		},
	}

	r, err := NewReplicator(Config{
		SourceCluster: "a",
		SourceFiler:   "127.0.0.1:8888",
		TargetCluster: "region-b",
		TargetFiler:   "127.0.0.1:9888",
	})
	if err != nil {
		t.Fatalf("NewReplicator: %v", err)
	}
	r.SetClients(source, target)

	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	statuses := SnapshotStatuses()
	if len(statuses) == 0 {
		t.Fatal("expected non-empty replication status")
	}
}

func TestReplicateChunksCopiesDataAndRewritesFID(t *testing.T) {
	source := newFakeClient()
	target := newFakeClient()

	originalFID := types.FileId{VolumeId: 3, NeedleId: 11, Cookie: 7}.String()
	payload := []byte("chunk-payload")

	sourceHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/"+originalFID {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer sourceHTTP.Close()

	var gotUploadPath string
	var gotUploadBody []byte
	targetHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		data, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		gotUploadPath = r.URL.Path
		gotUploadBody = data
		w.WriteHeader(http.StatusCreated)
	}))
	defer targetHTTP.Close()

	source.lookupURL = trimScheme(sourceHTTP.URL)
	target.assignURL = trimScheme(targetHTTP.URL)

	r, err := NewReplicator(Config{
		SourceCluster: "a",
		SourceFiler:   "127.0.0.1:8888",
		TargetCluster: "b",
		TargetFiler:   "127.0.0.1:9888",
	})
	if err != nil {
		t.Fatalf("NewReplicator: %v", err)
	}
	r.SetClients(source, target)

	entry := &filer_pb.Entry{
		Name:       "obj",
		Attributes: &filer_pb.FuseAttributes{},
		Chunks:     []*filer_pb.FileChunk{{FileId: originalFID}},
	}
	if err := r.replicateChunks(context.Background(), entry); err != nil {
		t.Fatalf("replicateChunks: %v", err)
	}
	if entry.GetChunks()[0].GetFileId() == originalFID {
		t.Fatalf("expected chunk file id to be rewritten")
	}
	if gotUploadPath == "" {
		t.Fatal("expected uploaded chunk request")
	}
	if string(gotUploadBody) != string(payload) {
		t.Fatalf("uploaded body=%q want=%q", gotUploadBody, payload)
	}
}

func trimScheme(raw string) string {
	return strings.TrimPrefix(raw, "http://")
}
