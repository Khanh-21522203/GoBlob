package shell

import (
	"context"
	"fmt"
	"io"
	"time"

	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/master_pb"
)

type clusterStatusCommand struct {
	name string
}

func init() {
	RegisterCommand(&clusterStatusCommand{name: "cluster.status"})
	RegisterCommand(&clusterStatusCommand{name: "cluster.ps"})
}

func (c *clusterStatusCommand) Name() string { return c.name }

func (c *clusterStatusCommand) Help() string {
	return "show cluster topology summary"
}

func (c *clusterStatusCommand) Do(args []string, env *CommandEnv, writer io.Writer) error {
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
		return fmt.Errorf("query master topology: %w", err)
	}

	topo := resp.GetTopologyInfo()
	if topo == nil {
		_, _ = fmt.Fprintf(writer, "master=%s topology=empty\n", masterAddr)
		return nil
	}

	dcCount := len(topo.GetDataCenterInfos())
	rackCount := 0
	nodeCount := 0
	volumeCount := 0

	for _, dc := range topo.GetDataCenterInfos() {
		rackCount += len(dc.GetRackInfos())
		for _, rack := range dc.GetRackInfos() {
			nodeCount += len(rack.GetDataNodeInfos())
			for _, node := range rack.GetDataNodeInfos() {
				for _, disk := range node.GetDiskInfos() {
					volumeCount += len(disk.GetVolumeInfos())
				}
			}
		}
	}

	_, _ = fmt.Fprintf(writer, "master: %s\n", masterAddr)
	_, _ = fmt.Fprintf(writer, "topology: %s\n", topo.GetId())
	_, _ = fmt.Fprintf(writer, "data_centers: %d\n", dcCount)
	_, _ = fmt.Fprintf(writer, "racks: %d\n", rackCount)
	_, _ = fmt.Fprintf(writer, "nodes: %d\n", nodeCount)
	_, _ = fmt.Fprintf(writer, "volumes: %d\n", volumeCount)
	_, _ = fmt.Fprintf(writer, "volume_size_limit_bytes: %d\n", resp.GetVolumeSizeLimit())
	return nil
}
