package command

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/volume_server_pb"
)

type CompactCommand struct {
	volumeServer string
	volumeID     uint
	threshold    float64
}

func init() {
	Register(&CompactCommand{})
}

func (c *CompactCommand) Name() string     { return "compact" }
func (c *CompactCommand) Synopsis() string { return "trigger compaction on a volume server" }
func (c *CompactCommand) Usage() string {
	return "blob compact -volumeServer 127.0.0.1:8080 -vid 1 -threshold 0.3"
}

func (c *CompactCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.volumeServer, "volumeServer", "127.0.0.1:8080", "volume server HTTP address")
	fs.UintVar(&c.volumeID, "vid", 0, "volume id")
	fs.Float64Var(&c.threshold, "threshold", 0.3, "garbage threshold [0,1]")
}

func (c *CompactCommand) Run(ctx context.Context, args []string) error {
	_ = ctx
	_ = args
	if c.volumeID == 0 {
		return fmt.Errorf("vid is required")
	}
	if c.threshold < 0 || c.threshold > 1 {
		return fmt.Errorf("threshold must be between 0 and 1")
	}
	grpcAddr := string(types.ServerAddress(strings.TrimSpace(c.volumeServer)).ToGrpcAddress())
	if grpcAddr == "" {
		return fmt.Errorf("empty volume server address")
	}
	return pb.WithVolumeServerClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), func(client volume_server_pb.VolumeServerClient) error {
		rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := client.VolumeCompact(rctx, &volume_server_pb.VolumeCompactRequest{
			VolumeId:         uint32(c.volumeID),
			GarbageThreshold: float32(c.threshold),
		})
		if err != nil {
			return err
		}
		if resp.GetError() != "" {
			return fmt.Errorf("volume compact failed: %s", resp.GetError())
		}
		fmt.Printf("compact requested for volume %d on %s\n", c.volumeID, c.volumeServer)
		return nil
	})
}
