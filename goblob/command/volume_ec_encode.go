package command

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"GoBlob/goblob/storage/erasure_coding"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"GoBlob/goblob/pb/volume_server_pb"
)

// VolumeECEncodeCommand plans erasure-coding conversion for an existing volume.
type VolumeECEncodeCommand struct {
	volumeID     uint
	dataShards   int
	parityShards int
	nodesCSV     string
	apply        bool
	sourceGRPC   string
	outputDir    string
}

func init() {
	Register(&VolumeECEncodeCommand{})
}

func (c *VolumeECEncodeCommand) Name() string { return "volume.ec.encode" }
func (c *VolumeECEncodeCommand) Synopsis() string {
	return "plan erasure-coding conversion for a volume"
}
func (c *VolumeECEncodeCommand) Usage() string {
	return "blob volume.ec.encode -vid 5 -dataShards 10 -parityShards 3 -nodes node1:8080,node2:8080,... [-apply -source.grpc host:port -output.dir ./ec-output]"
}

func (c *VolumeECEncodeCommand) SetFlags(fs *flag.FlagSet) {
	fs.UintVar(&c.volumeID, "vid", 0, "volume id to convert")
	fs.IntVar(&c.dataShards, "dataShards", erasure_coding.DefaultDataShards, "number of data shards")
	fs.IntVar(&c.parityShards, "parityShards", erasure_coding.DefaultParityShards, "number of parity shards")
	fs.StringVar(&c.nodesCSV, "nodes", "", "comma-separated target data nodes for shard placement")
	fs.BoolVar(&c.apply, "apply", false, "execute conversion by reading source volume needles and writing shard files")
	fs.StringVar(&c.sourceGRPC, "source.grpc", "", "source volume gRPC address for -apply mode")
	fs.StringVar(&c.outputDir, "output.dir", "", "output directory for generated shard files in -apply mode")
}

func (c *VolumeECEncodeCommand) Run(ctx context.Context, args []string) error {
	_ = ctx
	_ = args

	if c.volumeID == 0 {
		return fmt.Errorf("-vid is required")
	}
	enc, err := erasure_coding.NewEncoder(c.dataShards, c.parityShards)
	if err != nil {
		return fmt.Errorf("invalid erasure coding config: %w", err)
	}

	nodes := splitCSV(c.nodesCSV)
	if len(nodes) > 0 && len(nodes) < enc.TotalShards() {
		return fmt.Errorf("insufficient nodes: got %d need at least %d", len(nodes), enc.TotalShards())
	}

	plan := make([]erasure_coding.ECShard, 0, enc.TotalShards())
	for i := 0; i < enc.TotalShards(); i++ {
		node := ""
		if len(nodes) > 0 {
			node = strings.TrimSpace(nodes[i%len(nodes)])
		}
		plan = append(plan, erasure_coding.ECShard{
			VolumeId:   uint32(c.volumeID),
			ShardIndex: i,
			DataNode:   node,
		})
	}

	fmt.Printf("EC conversion plan for volume %d\n", c.volumeID)
	fmt.Printf("data shards: %d, parity shards: %d, total shards: %d\n", enc.DataShards(), enc.ParityShards(), enc.TotalShards())
	for _, shard := range plan {
		if shard.DataNode == "" {
			fmt.Printf("- shard %d -> <unspecified node>\n", shard.ShardIndex)
			continue
		}
		fmt.Printf("- shard %d -> %s\n", shard.ShardIndex, shard.DataNode)
	}
	if !c.apply {
		fmt.Println("Use -apply with -source.grpc and -output.dir to execute shard generation.")
		return nil
	}
	if strings.TrimSpace(c.sourceGRPC) == "" {
		return fmt.Errorf("-source.grpc is required when -apply is set")
	}
	if strings.TrimSpace(c.outputDir) == "" {
		return fmt.Errorf("-output.dir is required when -apply is set")
	}
	if err := c.execute(ctx, enc); err != nil {
		return err
	}
	fmt.Printf("EC conversion completed for volume %d, output=%s\n", c.volumeID, c.outputDir)
	return nil
}

type ecShardRecord struct {
	NeedleID  uint64 `json:"needle_id"`
	Cookie    uint32 `json:"cookie"`
	Offset    uint64 `json:"offset"`
	Size      uint32 `json:"size"`
	ShardData string `json:"shard_data_base64"`
}

type ecManifest struct {
	VolumeID      uint   `json:"volume_id"`
	DataShards    int    `json:"data_shards"`
	ParityShards  int    `json:"parity_shards"`
	TotalShards   int    `json:"total_shards"`
	SourceAddress string `json:"source_grpc"`
	NeedleCount   int64  `json:"needle_count"`
}

func (c *VolumeECEncodeCommand) execute(ctx context.Context, enc *erasure_coding.Encoder) error {
	if err := os.MkdirAll(c.outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	conn, err := grpc.NewClient(c.sourceGRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial source grpc: %w", err)
	}
	defer conn.Close()
	client := volume_server_pb.NewVolumeServerClient(conn)

	stream, err := client.ReadAllNeedles(ctx, &volume_server_pb.ReadAllNeedlesRequest{VolumeId: uint32(c.volumeID)})
	if err != nil {
		return fmt.Errorf("read all needles: %w", err)
	}

	shardFiles := make([]*os.File, 0, enc.TotalShards())
	shardEncoders := make([]*json.Encoder, 0, enc.TotalShards())
	for i := 0; i < enc.TotalShards(); i++ {
		fp := filepath.Join(c.outputDir, fmt.Sprintf("%d.shard.%d.jsonl", c.volumeID, i))
		f, err := os.Create(fp)
		if err != nil {
			for _, opened := range shardFiles {
				_ = opened.Close()
			}
			return fmt.Errorf("create shard file %s: %w", fp, err)
		}
		shardFiles = append(shardFiles, f)
		shardEncoders = append(shardEncoders, json.NewEncoder(f))
	}
	defer func() {
		for _, f := range shardFiles {
			_ = f.Close()
		}
	}()

	var needleCount int64
	for {
		item, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("stream recv: %w", err)
		}
		if item.GetIsDeleted() {
			continue
		}
		shards, err := enc.Encode(item.GetData())
		if err != nil {
			return fmt.Errorf("encode needle %d: %w", item.GetNeedleId(), err)
		}
		for i, shard := range shards {
			rec := ecShardRecord{
				NeedleID:  item.GetNeedleId(),
				Cookie:    item.GetCookie(),
				Offset:    item.GetOffset(),
				Size:      item.GetSize(),
				ShardData: base64.StdEncoding.EncodeToString(shard),
			}
			if err := shardEncoders[i].Encode(rec); err != nil {
				return fmt.Errorf("write shard record %d: %w", i, err)
			}
		}
		needleCount++
	}

	manifest := ecManifest{
		VolumeID:      c.volumeID,
		DataShards:    enc.DataShards(),
		ParityShards:  enc.ParityShards(),
		TotalShards:   enc.TotalShards(),
		SourceAddress: c.sourceGRPC,
		NeedleCount:   needleCount,
	}
	manifestPath := filepath.Join(c.outputDir, fmt.Sprintf("%d.ec.manifest.json", c.volumeID))
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}
