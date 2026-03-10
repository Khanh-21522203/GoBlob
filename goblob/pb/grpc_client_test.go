package pb

import (
	"context"
	"testing"

	"GoBlob/goblob/pb/master_pb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestWithMasterServerClientDialFailure verifies that an RPC call to an
// unreachable address results in an error being returned.
func TestWithMasterServerClientDialFailure(t *testing.T) {
	// grpc.NewClient is non-blocking, so the error surfaces on the first RPC call.
	// Use a port that is very unlikely to be listening.
	opt := grpc.WithTransportCredentials(insecure.NewCredentials())
	err := WithMasterServerClient("localhost:1", opt, func(c master_pb.MasterServiceClient) error {
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		_, rpcErr := c.GetMasterConfiguration(ctx, &master_pb.GetMasterConfigurationRequest{})
		return rpcErr
	})
	if err == nil {
		t.Fatal("expected an error when dialing an unreachable address, got nil")
	}
}
