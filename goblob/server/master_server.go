package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"time"

	"log/slog"

	"GoBlob/goblob/cluster"
	"GoBlob/goblob/raft"
	"GoBlob/goblob/security"
	"GoBlob/goblob/sequence"
	"GoBlob/goblob/topology"
)

// MasterServer is the central coordinator for the GoBlob distributed storage system.
// It handles file ID assignment, volume lookup, and topology management.
type MasterServer struct {
	Topo                    *topology.Topology
	Vg                      *topology.VolumeGrowth
	Sequencer               sequence.Sequencer
	Raft                    raft.RaftServer
	Cluster                 *cluster.ClusterRegistry
	Guard                   *security.Guard
	option                 *MasterOption
	volumeGrowthRequestChan chan *topology.VolumeGrowOption
	ctx                    context.Context
	cancel                 context.CancelFunc
	logger                 *slog.Logger
	isLeader               atomic.Bool
}

// NewMasterServer creates and initializes a new MasterServer.
func NewMasterServer(mux *http.ServeMux, opt *MasterOption) (*MasterServer, error) {
	ctx, cancel := context.WithCancel(context.Background())

	logger := slog.Default().With("server", "master")

	// Create topology
	topo := topology.NewTopology()

	// Create volume growth manager
	vg := topology.NewVolumeGrowth(topo)

	// Create sequencer
	seqConfig := &sequence.Config{
		DataDir:  filepath.Join(opt.MetaDir, "sequencer"),
		StepSize: 10000,
	}
	sequencer, err := sequence.NewFileSequencer(seqConfig)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create sequencer: %w", err)
	}

	// Create Raft config
	raftConfig := &raft.RaftConfig{
		NodeId:             fmt.Sprintf("%s:%d", opt.Host, opt.GRPCPort),
		BindAddr:           fmt.Sprintf("%s:%d", opt.Host, opt.GRPCPort),
		MetaDir:            filepath.Join(opt.MetaDir, "raft"),
		Peers:              opt.Peers,
		SingleMode:         len(opt.Peers) == 0,
		SnapshotThreshold:  10000,
		SnapshotInterval:   2 * time.Minute,
		HeartbeatTimeout:   1 * time.Second,
		ElectionTimeout:    1 * time.Second,
		LeaderLeaseTimeout: 500 * time.Millisecond,
		MaxAppendEntries:   32,
	}

	// Create FSM
	fsm := raft.NewMasterFSM(
		func(maxFileId uint64) {
			sequencer.SetMax(maxFileId)
		},
		func(maxVolumeId uint32) {
			// TODO: handle max volume ID
		},
		func(topologyId string) {
			// TODO: handle topology ID
		},
	)

	// Create cluster registry
	clusterReg := cluster.NewClusterRegistry()

	// Create guard (empty whitelist = allow all)
	signingKey, _ := security.GenerateSigningKey(32)
	guard := security.NewGuard("", signingKey, "")

	// Create Raft server (without leader callback for now)
	raftServer, err := raft.NewRaftServer(raftConfig, fsm, nil)
	if err != nil {
		cancel()
		sequencer.Close()
		return nil, fmt.Errorf("failed to create Raft server: %w", err)
	}

	ms := &MasterServer{
		Topo:                    topo,
		Vg:                      vg,
		Sequencer:               sequencer,
		Raft:                    raftServer,
		Cluster:                 clusterReg,
		Guard:                   guard,
		option:                 opt,
		volumeGrowthRequestChan: make(chan *topology.VolumeGrowOption, 100),
		ctx:                     ctx,
		cancel:                  cancel,
		logger:                  logger,
	}

	// Register HTTP routes
	ms.registerRoutes(mux)

	// Start background goroutines
	go ms.processVolumeGrowRequests()

	return ms, nil
}

// registerRoutes registers HTTP handlers for the master server.
func (ms *MasterServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /dir/assign", ms.handleAssign)
	mux.HandleFunc("GET /dir/lookup", ms.handleLookup)
	mux.HandleFunc("GET /dir/status", ms.handleStatus)
	mux.HandleFunc("POST /vol/grow", ms.handleVolGrow)
	mux.HandleFunc("POST /vol/vacuum", ms.handleVacuum)
	mux.HandleFunc("GET /cluster/healthz", ms.handleHealthz)
}

// Shutdown gracefully shuts down the master server.
func (ms *MasterServer) Shutdown() {
	ms.cancel()

	// Stop Raft
	if ms.Raft != nil {
		_ = ms.Raft.Shutdown()
	}

	// Close sequencer
	if ms.Sequencer != nil {
		_ = ms.Sequencer.Close()
	}
}

// processVolumeGrowRequests processes volume growth requests from the channel.
func (ms *MasterServer) processVolumeGrowRequests() {
	for {
		select {
		case <-ms.ctx.Done():
			return
		case req := <-ms.volumeGrowthRequestChan:
			if req == nil {
				continue
			}
			// In a full implementation, this would call vg.AutomaticGrowByType
			// For now, just log the request
			ms.logger.Info("volume growth request", "collection", req.Collection)
		}
	}
}
