package wdclient

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/master_pb"

	"google.golang.org/grpc"
)

// MasterClient maintains a connection to one of the master servers and caches volume locations.
type MasterClient struct {
	masterAddresses []string
	currentMaster   types.ServerAddress
	mu              sync.RWMutex
	grpcDialOption  grpc.DialOption
	vidCache        *VidCache
}

// NewMasterClient creates a new MasterClient.
func NewMasterClient(masterAddresses []string, grpcDialOption grpc.DialOption) *MasterClient {
	normalized := make([]string, 0, len(masterAddresses))
	for _, addr := range masterAddresses {
		normalized = append(normalized, string(types.ServerAddress(addr).ToGrpcAddress()))
	}
	var current types.ServerAddress
	if len(normalized) > 0 {
		current = types.ServerAddress(normalized[0])
	}
	return &MasterClient{
		masterAddresses: normalized,
		currentMaster:   current,
		grpcDialOption:  grpcDialOption,
		vidCache:        NewVidCache(10 * time.Minute),
	}
}

// GetCurrentMaster returns the currently known master leader address.
func (mc *MasterClient) GetCurrentMaster() types.ServerAddress {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.currentMaster
}

// SetCurrentMaster updates the current master leader address.
func (mc *MasterClient) SetCurrentMaster(addr types.ServerAddress) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.currentMaster = addr.ToGrpcAddress()
}

// LookupVolumeId looks up the locations for a volume ID.
// It checks the local cache first; on a miss it queries the current master via gRPC.
func (mc *MasterClient) LookupVolumeId(ctx context.Context, vid types.VolumeId) ([]Location, error) {
	if locs, ok := mc.vidCache.Get(vid); ok {
		return locs, nil
	}

	master := mc.GetCurrentMaster()
	if master == "" {
		return nil, fmt.Errorf("no master server known")
	}
	grpcMaster := master.ToGrpcAddress()

	var locations []Location
	err := pb.WithMasterServerClient(string(grpcMaster), mc.grpcDialOption, func(client master_pb.MasterServiceClient) error {
		req := &master_pb.LookupVolumeRequest{
			VolumeOrFileIds: []string{strconv.FormatUint(uint64(vid), 10)},
		}
		resp, err := client.LookupVolume(ctx, req)
		if err != nil {
			return err
		}

		key := strconv.FormatUint(uint64(vid), 10)
		vidLocs, ok := resp.LocationsMap[key]
		if !ok {
			return fmt.Errorf("volume %d not found", vid)
		}
		if vidLocs.Error != "" {
			return fmt.Errorf("lookup error: %s", vidLocs.Error)
		}

		for _, l := range vidLocs.Locations {
			locations = append(locations, Location{
				Url:       l.Url,
				PublicUrl: l.PublicUrl,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	mc.vidCache.Set(vid, locations)
	return locations, nil
}

// KeepConnectedToMaster maintains a bidirectional stream with the master to receive
// topology updates. On leader changes, it calls onLeaderChange and updates currentMaster.
// Reconnects with a 1-second delay on error. Blocks until ctx is cancelled.
func (mc *MasterClient) KeepConnectedToMaster(ctx context.Context, clientType string, clientAddress string, onLeaderChange func(types.ServerAddress)) error {
	for {
		master := mc.GetCurrentMaster()
		if master == "" && len(mc.masterAddresses) > 0 {
			master = types.ServerAddress(mc.masterAddresses[0])
		}
		if master == "" {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
				continue
			}
		}
		grpcMaster := master.ToGrpcAddress()

		err := pb.WithMasterServerClient(string(grpcMaster), mc.grpcDialOption, func(client master_pb.MasterServiceClient) error {
			stream, err := client.KeepConnected(ctx)
			if err != nil {
				return err
			}

			// Send initial identification request.
			if sendErr := stream.Send(&master_pb.KeepConnectedRequest{
				ClientType:    clientType,
				ClientAddress: clientAddress,
			}); sendErr != nil {
				return sendErr
			}

			for {
				resp, recvErr := stream.Recv()
				if recvErr != nil {
					if recvErr == io.EOF {
						return nil
					}
					return recvErr
				}

				// Check for a leader change in the received volume locations.
				for _, vl := range resp.VolumeLocations {
					if vl.Leader != "" {
						newLeader := types.ServerAddress(vl.Leader)
						mc.SetCurrentMaster(newLeader)
						if onLeaderChange != nil {
							onLeaderChange(newLeader)
						}
					}
				}
			}
		})

		// If context was cancelled, stop retrying.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			time.Sleep(1 * time.Second)
		}
	}
}
