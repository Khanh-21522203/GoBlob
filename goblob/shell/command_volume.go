package shell

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/master_pb"
)

type volumeListCommand struct{}

func init() {
	RegisterCommand(&volumeListCommand{})
}

func (c *volumeListCommand) Name() string { return "volume.list" }

func (c *volumeListCommand) Help() string {
	return "list volumes discovered from master topology"
}

func (c *volumeListCommand) Do(args []string, env *CommandEnv, writer io.Writer) error {
	_ = args
	masterAddr := env.masterGRPCAddress()
	if masterAddr == "" {
		return fmt.Errorf("no master address configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var resp *master_pb.VolumeListResponse
	err := pb.WithMasterServerClient(masterAddr, env.GrpcDialOption, func(client master_pb.MasterServiceClient) error {
		var callErr error
		resp, callErr = client.VolumeList(ctx, &master_pb.VolumeListRequest{})
		return callErr
	})
	if err != nil {
		return fmt.Errorf("query volume list: %w", err)
	}
	if resp == nil || resp.GetTopologyInfo() == nil {
		_, _ = fmt.Fprintln(writer, "no volumes")
		return nil
	}

	type row struct {
		vid      uint32
		node     string
		diskType string
		size     uint64
		readOnly bool
	}

	rows := make([]row, 0, 64)
	for _, dc := range resp.GetTopologyInfo().GetDataCenterInfos() {
		for _, rack := range dc.GetRackInfos() {
			for _, node := range rack.GetDataNodeInfos() {
				nodeID := node.GetId()
				if nodeID == "" {
					nodeID = node.GetPublicUrl()
				}
				for _, disk := range node.GetDiskInfos() {
					for _, v := range disk.GetVolumeInfos() {
						rows = append(rows, row{
							vid:      v.GetId(),
							node:     nodeID,
							diskType: disk.GetType(),
							size:     v.GetSize(),
							readOnly: v.GetReadOnly(),
						})
					}
				}
			}
		}
	}

	if len(rows) == 0 {
		_, _ = fmt.Fprintln(writer, "no volumes")
		return nil
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].vid != rows[j].vid {
			return rows[i].vid < rows[j].vid
		}
		return rows[i].node < rows[j].node
	})

	_, _ = fmt.Fprintln(writer, "VID\tNODE\tDISK\tSIZE\tREADONLY")
	for _, r := range rows {
		_, _ = fmt.Fprintf(writer, "%d\t%s\t%s\t%d\t%t\n", r.vid, r.node, r.diskType, r.size, r.readOnly)
	}
	return nil
}
