package shell

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/master_pb"
)

type nodeSnapshot struct {
	id          string
	grpcAddress string
	publicURL   string
	volumes     map[uint32]struct{}
	free        uint64
}

type volumeSnapshot struct {
	id          uint32
	collection  string
	replication string
	ttl         string
	diskType    string
	nodes       map[string]struct{}
}

type clusterSnapshot struct {
	nodes   map[string]*nodeSnapshot
	volumes map[uint32]*volumeSnapshot
}

func fetchClusterSnapshot(env *CommandEnv) (*clusterSnapshot, error) {
	masterAddr := env.masterGRPCAddress()
	if masterAddr == "" {
		return nil, fmt.Errorf("no master address configured")
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
		return nil, err
	}
	if resp == nil || resp.GetTopologyInfo() == nil {
		return &clusterSnapshot{
			nodes:   map[string]*nodeSnapshot{},
			volumes: map[uint32]*volumeSnapshot{},
		}, nil
	}

	snap := &clusterSnapshot{
		nodes:   make(map[string]*nodeSnapshot),
		volumes: make(map[uint32]*volumeSnapshot),
	}

	for _, dc := range resp.GetTopologyInfo().GetDataCenterInfos() {
		for _, rack := range dc.GetRackInfos() {
			for _, dn := range rack.GetDataNodeInfos() {
				nodeID := dn.GetId()
				if nodeID == "" {
					nodeID = dn.GetPublicUrl()
				}
				if nodeID == "" {
					continue
				}
				grpcAddr := strings.TrimSpace(dn.GetId())
				if grpcAddr == "" {
					grpcAddr = string(types.ServerAddress(strings.TrimSpace(dn.GetPublicUrl())).ToGrpcAddress())
				}
				publicURL := strings.TrimSpace(dn.GetPublicUrl())
				if publicURL == "" {
					publicURL = inferHTTPAddress(grpcAddr)
				}
				node := snap.nodes[nodeID]
				if node == nil {
					node = &nodeSnapshot{
						id:          nodeID,
						grpcAddress: grpcAddr,
						publicURL:   publicURL,
						volumes:     make(map[uint32]struct{}),
					}
					snap.nodes[nodeID] = node
				} else {
					if node.grpcAddress == "" {
						node.grpcAddress = grpcAddr
					}
					if node.publicURL == "" {
						node.publicURL = publicURL
					}
				}
				for _, disk := range dn.GetDiskInfos() {
					node.free += disk.GetFreeVolumeCount()
					diskType := disk.GetType()
					for _, vol := range disk.GetVolumeInfos() {
						node.volumes[vol.GetId()] = struct{}{}
						vs := snap.volumes[vol.GetId()]
						if vs == nil {
							replication := types.ParseReplicaPlacement(byte(vol.GetReplicaPlacement())).String()
							vs = &volumeSnapshot{
								id:          vol.GetId(),
								collection:  vol.GetCollection(),
								replication: replication,
								ttl:         vol.GetTtl(),
								diskType:    diskType,
								nodes:       make(map[string]struct{}),
							}
							snap.volumes[vol.GetId()] = vs
						} else if vs.diskType == "" {
							vs.diskType = diskType
						}
						vs.nodes[nodeID] = struct{}{}
					}
				}
			}
		}
	}
	return snap, nil
}

func sortedNodeIDs(nodes map[string]*nodeSnapshot) []string {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedVolumeIDs(vols map[uint32]struct{}) []uint32 {
	ids := make([]uint32, 0, len(vols))
	for id := range vols {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func resolveNodeSnapshot(snap *clusterSnapshot, input string) (string, *nodeSnapshot, error) {
	if snap == nil {
		return "", nil, fmt.Errorf("cluster snapshot not available")
	}
	v := strings.TrimSpace(input)
	if v == "" {
		return "", nil, fmt.Errorf("empty node id")
	}
	if node, ok := snap.nodes[v]; ok {
		return v, node, nil
	}
	for key, node := range snap.nodes {
		if node == nil {
			continue
		}
		if v == node.grpcAddress || v == node.publicURL {
			return key, node, nil
		}
	}

	candidates := []string{
		string(types.ServerAddress(v).ToGrpcAddress()),
		string(types.ServerAddress(v).ToHttpAddress()),
	}
	if inferred := inferHTTPAddress(v); inferred != "" {
		candidates = append(candidates, inferred)
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if node, ok := snap.nodes[c]; ok {
			return c, node, nil
		}
		for key, node := range snap.nodes {
			if node == nil {
				continue
			}
			if c == node.grpcAddress || c == node.publicURL {
				return key, node, nil
			}
		}
	}

	return "", nil, fmt.Errorf("node %q not found in topology", input)
}

func nodeGRPCAddress(nodeID string, node *nodeSnapshot) string {
	if node != nil {
		if node.grpcAddress != "" {
			return node.grpcAddress
		}
		if node.publicURL != "" {
			return string(types.ServerAddress(node.publicURL).ToGrpcAddress())
		}
	}
	return string(types.ServerAddress(nodeID).ToGrpcAddress())
}

func inferHTTPAddress(addr string) string {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return strings.TrimSpace(addr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return strings.TrimSpace(addr)
	}
	if port > types.GRPCPortOffset {
		return net.JoinHostPort(host, strconv.Itoa(port-types.GRPCPortOffset))
	}
	return net.JoinHostPort(host, portStr)
}
