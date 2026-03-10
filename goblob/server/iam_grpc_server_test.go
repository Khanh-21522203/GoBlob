package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"GoBlob/goblob/filer/leveldb2"
	iampb "GoBlob/goblob/pb/iam_pb"
	s3iam "GoBlob/goblob/s3api/iam"
)

func TestIAMGRPCServerRoundTrip(t *testing.T) {
	grpcServer := grpc.NewServer()
	fs, err := NewFilerServer(http.NewServeMux(), http.NewServeMux(), grpcServer, DefaultFilerOption())
	if err != nil {
		t.Fatalf("NewFilerServer: %v", err)
	}

	store, err := leveldb2.NewLevelDB2Store(t.TempDir())
	if err != nil {
		t.Fatalf("NewLevelDB2Store: %v", err)
	}
	defer store.Shutdown()
	fs.SetStore(store)

	conn := dialBufConn(t, grpcServer)
	defer conn.Close()

	client := iampb.NewIAMServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cfg := &iampb.S3ApiConfiguration{Identities: []*iampb.Identity{{
		Name:        "dev",
		Credentials: []*iampb.Credential{{AccessKey: "AKIA_DEV", SecretKey: "secret"}},
		Actions:     []string{"Read", "Write"},
	}}}
	if _, err := client.PutS3ApiConfiguration(ctx, &iampb.PutS3ApiConfigurationRequest{S3ApiConfiguration: cfg}); err != nil {
		t.Fatalf("PutS3ApiConfiguration: %v", err)
	}

	resp, err := client.GetS3ApiConfiguration(ctx, &iampb.GetS3ApiConfigurationRequest{})
	if err != nil {
		t.Fatalf("GetS3ApiConfiguration: %v", err)
	}
	if got := len(resp.GetS3ApiConfiguration().GetIdentities()); got != 1 {
		t.Fatalf("expected 1 identity, got %d", got)
	}
	if got := resp.GetS3ApiConfiguration().GetIdentities()[0].GetName(); got != "dev" {
		t.Fatalf("unexpected identity name: %q", got)
	}

	persisted, err := store.KvGet(ctx, s3iam.IAMConfigKey)
	if err != nil {
		t.Fatalf("KvGet persisted IAM config: %v", err)
	}
	if len(persisted) == 0 {
		t.Fatalf("expected persisted IAM config bytes")
	}
}

func TestIAMGRPCServerUnavailableWhenFilerNotReady(t *testing.T) {
	grpcServer := grpc.NewServer()
	_, err := NewFilerServer(http.NewServeMux(), http.NewServeMux(), grpcServer, DefaultFilerOption())
	if err != nil {
		t.Fatalf("NewFilerServer: %v", err)
	}

	conn := dialBufConn(t, grpcServer)
	defer conn.Close()

	client := iampb.NewIAMServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = client.GetS3ApiConfiguration(ctx, &iampb.GetS3ApiConfigurationRequest{})
	if err == nil {
		t.Fatalf("expected GetS3ApiConfiguration to fail when filer store is not set")
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Unavailable {
		t.Fatalf("expected Unavailable error, got %v", err)
	}
}

func dialBufConn(t *testing.T, grpcServer *grpc.Server) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = lis.Close()
	})

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.NewClient("bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	return conn
}
