package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/volume_server_pb"
	"GoBlob/goblob/storage/needle"
)

func TestReadAllNeedlesAndVolumeCopy(t *testing.T) {
	source := newVolumeServerForCopyTest(t)
	target := newVolumeServerForCopyTest(t)

	sourceRPC := NewVolumeGRPCServer(source)
	targetRPC := NewVolumeGRPCServer(target)

	const vid = types.VolumeId(101)
	if err := source.store.AllocateVolume(vid, "photos", types.CurrentNeedleVersion); err != nil {
		t.Fatalf("allocate source volume: %v", err)
	}
	srcNeedle := &needle.Needle{
		Id:       types.NeedleId(12345),
		Cookie:   types.Cookie(98765),
		Data:     []byte("hello-replica"),
		DataSize: uint32(len("hello-replica")),
	}
	srcNeedle.SetMime("text/plain")
	srcNeedle.SetName("hello.txt")
	if _, _, err := source.store.WriteVolumeNeedle(vid, srcNeedle); err != nil {
		t.Fatalf("write source needle: %v", err)
	}

	readStream := &collectReadNeedlesStream{ctx: context.Background()}
	if err := sourceRPC.ReadAllNeedles(&volume_server_pb.ReadAllNeedlesRequest{VolumeId: uint32(vid)}, readStream); err != nil {
		t.Fatalf("ReadAllNeedles: %v", err)
	}
	if len(readStream.responses) != 1 {
		t.Fatalf("expected 1 streamed needle, got %d", len(readStream.responses))
	}
	item := readStream.responses[0]
	if got, want := string(item.GetData()), "hello-replica"; got != want {
		t.Fatalf("ReadAllNeedles data=%q want %q", got, want)
	}
	if got, want := item.GetMime(), "text/plain"; got != want {
		t.Fatalf("ReadAllNeedles mime=%q want %q", got, want)
	}

	restoreDial := overrideWithVolumeClientFromServer(t, sourceRPC)
	defer restoreDial()

	copyStream := &collectVolumeCopyStream{ctx: context.Background()}
	if err := targetRPC.VolumeCopy(&volume_server_pb.VolumeCopyRequest{
		VolumeId:       uint32(vid),
		Collection:     "photos",
		SourceDataNode: "source-bufconn",
	}, copyStream); err != nil {
		t.Fatalf("VolumeCopy: %v", err)
	}

	if len(copyStream.responses) == 0 || copyStream.responses[len(copyStream.responses)-1].GetProcessPercent() < 100 {
		t.Fatalf("expected copy progress to reach 100, got %+v", copyStream.responses)
	}

	fid := types.FileId{VolumeId: vid, NeedleId: srcNeedle.Id, Cookie: srcNeedle.Cookie}
	copied, err := target.store.ReadVolumeNeedle(vid, fid)
	if err != nil {
		t.Fatalf("read copied needle: %v", err)
	}
	if got, want := string(copied.Data), "hello-replica"; got != want {
		t.Fatalf("copied data=%q want %q", got, want)
	}
	if got, want := copied.GetMime(), "text/plain"; got != want {
		t.Fatalf("copied mime=%q want %q", got, want)
	}
}

func TestVolumeCopyAlreadyExists(t *testing.T) {
	source := newVolumeServerForCopyTest(t)
	target := newVolumeServerForCopyTest(t)

	sourceRPC := NewVolumeGRPCServer(source)
	targetRPC := NewVolumeGRPCServer(target)

	const vid = types.VolumeId(202)
	if err := source.store.AllocateVolume(vid, "default", types.CurrentNeedleVersion); err != nil {
		t.Fatalf("allocate source volume: %v", err)
	}
	if err := target.store.AllocateVolume(vid, "default", types.CurrentNeedleVersion); err != nil {
		t.Fatalf("allocate target volume: %v", err)
	}

	restoreDial := overrideWithVolumeClientFromServer(t, sourceRPC)
	defer restoreDial()

	err := targetRPC.VolumeCopy(&volume_server_pb.VolumeCopyRequest{
		VolumeId:       uint32(vid),
		Collection:     "default",
		SourceDataNode: "source-bufconn",
	}, &collectVolumeCopyStream{ctx: context.Background()})
	if err == nil {
		t.Fatalf("expected already-exists error")
	}
	if code := status.Code(err); code != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists, got %s (%v)", code, err)
	}
}

func newVolumeServerForCopyTest(t *testing.T) *VolumeServer {
	t.Helper()

	opt := DefaultVolumeServerOption()
	opt.Host = "127.0.0.1"
	opt.Port = 8080
	opt.GRPCPort = 18080
	opt.PreStopSeconds = 0
	opt.HeartbeatInterval = time.Hour
	opt.Masters = nil
	opt.Directories = []DiskDirectoryConfig{
		{Path: t.TempDir(), MaxVolumeCount: 16},
	}

	adminMux := http.NewServeMux()
	publicMux := http.NewServeMux()
	vs, err := NewVolumeServer(adminMux, publicMux, nil, opt)
	if err != nil {
		t.Fatalf("NewVolumeServer: %v", err)
	}
	t.Cleanup(vs.Shutdown)
	return vs
}

func overrideWithVolumeClientFromServer(t *testing.T, source *VolumeGRPCServer) func() {
	t.Helper()

	lis := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	volume_server_pb.RegisterVolumeServerServer(grpcServer, source)
	go func() {
		_ = grpcServer.Serve(lis)
	}()

	old := withVolumeServerClient
	withVolumeServerClient = func(_ string, _ grpc.DialOption, fn func(volume_server_pb.VolumeServerClient) error) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, err := grpc.DialContext(
			ctx,
			"bufnet",
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			return err
		}
		defer conn.Close()
		return fn(volume_server_pb.NewVolumeServerClient(conn))
	}

	return func() {
		withVolumeServerClient = old
		grpcServer.Stop()
		_ = lis.Close()
	}
}

type collectVolumeCopyStream struct {
	ctx       context.Context
	responses []*volume_server_pb.VolumeCopyResponse
}

func (s *collectVolumeCopyStream) SetHeader(metadata.MD) error  { return nil }
func (s *collectVolumeCopyStream) SendHeader(metadata.MD) error { return nil }
func (s *collectVolumeCopyStream) SetTrailer(metadata.MD)       {}
func (s *collectVolumeCopyStream) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}
func (s *collectVolumeCopyStream) SendMsg(any) error { return nil }
func (s *collectVolumeCopyStream) RecvMsg(any) error { return io.EOF }
func (s *collectVolumeCopyStream) Send(resp *volume_server_pb.VolumeCopyResponse) error {
	if resp == nil {
		return nil
	}
	copied := *resp
	s.responses = append(s.responses, &copied)
	return nil
}

type collectReadNeedlesStream struct {
	ctx       context.Context
	responses []*volume_server_pb.ReadAllNeedlesResponse
}

func (s *collectReadNeedlesStream) SetHeader(metadata.MD) error  { return nil }
func (s *collectReadNeedlesStream) SendHeader(metadata.MD) error { return nil }
func (s *collectReadNeedlesStream) SetTrailer(metadata.MD)       {}
func (s *collectReadNeedlesStream) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}
func (s *collectReadNeedlesStream) SendMsg(any) error { return nil }
func (s *collectReadNeedlesStream) RecvMsg(any) error { return io.EOF }
func (s *collectReadNeedlesStream) Send(resp *volume_server_pb.ReadAllNeedlesResponse) error {
	if resp == nil {
		return nil
	}
	copied := *resp
	copied.Data = append([]byte(nil), resp.GetData()...)
	s.responses = append(s.responses, &copied)
	return nil
}
