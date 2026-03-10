package command

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/master_pb"
)

type ExportCommand struct {
	master string
	out    string
}

func init() {
	Register(&ExportCommand{})
}

func (c *ExportCommand) Name() string     { return "export" }
func (c *ExportCommand) Synopsis() string { return "export master topology as JSON" }
func (c *ExportCommand) Usage() string {
	return "blob export -master 127.0.0.1:9333 -out topology.json"
}

func (c *ExportCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.master, "master", "127.0.0.1:9333", "master HTTP address")
	fs.StringVar(&c.out, "out", "-", "output JSON file, use - for stdout")
}

func (c *ExportCommand) Run(ctx context.Context, args []string) error {
	_ = ctx
	_ = args
	masterAddr := string(types.ServerAddress(strings.TrimSpace(c.master)).ToGrpcAddress())
	if masterAddr == "" {
		return fmt.Errorf("empty master address")
	}

	var resp *master_pb.VolumeListResponse
	err := pb.WithMasterServerClient(masterAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), func(client master_pb.MasterServiceClient) error {
		rctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var callErr error
		resp, callErr = client.VolumeList(rctx, &master_pb.VolumeListRequest{})
		return callErr
	})
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return err
	}
	if c.out == "-" {
		_, _ = os.Stdout.Write(data)
		_, _ = os.Stdout.Write([]byte{'\n'})
		return nil
	}
	return os.WriteFile(c.out, data, 0644)
}
