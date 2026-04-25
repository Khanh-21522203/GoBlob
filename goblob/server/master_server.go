package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"log/slog"

	"GoBlob/goblob/cluster"
	"GoBlob/goblob/config"
	"GoBlob/goblob/consensus"
	"GoBlob/goblob/consensus/hashicorpraft"
	"GoBlob/goblob/obs"
	"GoBlob/goblob/pb/master_pb"
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
	Raft                    consensus.Engine
	Cluster                 *cluster.ClusterRegistry
	Guard                   *security.Guard
	option                  *MasterOption
	volumeGrowthRequestChan chan *topology.VolumeGrowOption
	ctx                     context.Context
	cancel                  context.CancelFunc
	logger                  *slog.Logger
	isLeader                atomic.Bool
	topologyId              atomic.Value
}

// NewMasterServer creates and initializes a new MasterServer.
func NewMasterServer(mux *http.ServeMux, opt *MasterOption) (*MasterServer, error) {
	return NewMasterServerWithGRPC(mux, nil, opt)
}

// NewMasterServerWithGRPC creates and initializes a new MasterServer and registers
// the gRPC service if grpcServer is provided.
func NewMasterServerWithGRPC(mux *http.ServeMux, grpcServer *grpc.Server, opt *MasterOption) (*MasterServer, error) {
	ctx, cancel := context.WithCancel(context.Background())

	logger := obs.New("master").With("server", "master")

	// Phase 1: FSM with no callbacks.
	fsm := raft.NewMasterFSM()

	// Phase 2: Raft and sequencer, no circular deps.
	raftPort := opt.RaftPort
	if raftPort == 0 {
		raftPort = opt.Port + 1
	}
	raftConfig := &hashicorpraft.Config{
		NodeId:             fmt.Sprintf("%s:%d", opt.Host, opt.Port),
		BindAddr:           fmt.Sprintf("%s:%d", opt.Host, raftPort),
		MetaDir:            filepath.Join(opt.MetaDir, "raft"),
		Peers:              opt.Peers,
		SingleMode:         len(opt.Peers) == 0,
		SnapshotThreshold:  10000,
		SnapshotInterval:   2 * time.Minute,
		HeartbeatTimeout:   1 * time.Second,
		ElectionTimeout:    1 * time.Second,
		LeaderLeaseTimeout: 500 * time.Millisecond,
		CommitTimeout:      50 * time.Millisecond,
		MaxAppendEntries:   32,
	}
	raftServer, err := hashicorpraft.NewEngine(raftConfig, fsm)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create Raft server: %w", err)
	}

	seqConfig := &sequence.Config{
		DataDir:  filepath.Join(opt.MetaDir, "sequencer"),
		StepSize: 10000,
	}
	raftSequencer, err := sequence.NewRaftSequencer(seqConfig, raftServer, logger)
	if err != nil {
		cancel()
		_ = raftServer.Shutdown()
		return nil, fmt.Errorf("failed to create raft sequencer: %w", err)
	}

	// Phase 3: assemble MasterServer, then wire events.
	topo := topology.NewTopology()
	vg := topology.NewVolumeGrowth(topo)
	signingKey, _ := security.GenerateSigningKey(32)
	guard := security.NewGuard("", signingKey, "")

	ms := &MasterServer{
		Topo:                    topo,
		Vg:                      vg,
		Sequencer:               raftSequencer,
		Raft:                    raftServer,
		Cluster:                 cluster.NewClusterRegistry(),
		Guard:                   guard,
		option:                  opt,
		volumeGrowthRequestChan: make(chan *topology.VolumeGrowOption, 100),
		ctx:                     ctx,
		cancel:                  cancel,
		logger:                  logger,
	}

	// Prime state from already-committed FSM entries (e.g. after a restart).
	state := fsm.CommittedState()
	if state.MaxFileId > 0 {
		ms.Sequencer.SetMax(state.MaxFileId)
	}
	if state.MaxVolumeId > 0 {
		ms.Topo.SetMaxVolumeId(state.MaxVolumeId)
	}
	if state.TopologyId != "" {
		ms.topologyId.Store(state.TopologyId)
	}
	ms.isLeader.Store(raftServer.IsLeader())
	if raftServer.IsLeader() {
		obs.MasterLeadershipGauge.Set(1)
	}

	ms.Vg.SetMaxVolumeIdReplicator(func(maxVid uint32) error {
		return ms.Raft.Apply(raft.MaxVolumeIdCommand{MaxVolumeId: maxVid}, 5*time.Second)
	})

	// Subscribe to FSM and leader events.
	fsmEvents := fsm.Subscribe("master", 64)
	leaderEvents := raftServer.Subscribe("master", 8)
	go ms.handleFSMEvents(fsmEvents)
	go ms.handleLeaderEvents(leaderEvents)

	// Register HTTP routes
	ms.registerRoutes(mux)
	if grpcServer != nil {
		master_pb.RegisterMasterServiceServer(grpcServer, NewMasterGRPCServer(ms))
	}

	// Start background goroutines
	go ms.processVolumeGrowRequests()
	go ms.raftStatsWorker()

	return ms, nil
}

// handleFSMEvents reacts to replicated state changes from the FSM.
func (ms *MasterServer) handleFSMEvents(ch <-chan raft.StateEvent) {
	for evt := range ch {
		switch evt.Kind {
		case raft.EventMaxFileId:
			ms.Sequencer.SetMax(evt.MaxFileId)
		case raft.EventMaxVolumeId:
			ms.Topo.SetMaxVolumeId(evt.MaxVolumeId)
		case raft.EventTopologyId:
			if evt.TopologyId != "" {
				ms.topologyId.Store(evt.TopologyId)
			}
		case raft.EventRestored:
			if evt.MaxFileId > 0 {
				ms.Sequencer.SetMax(evt.MaxFileId)
			}
			if evt.MaxVolumeId > 0 {
				ms.Topo.SetMaxVolumeId(evt.MaxVolumeId)
			}
			if evt.TopologyId != "" {
				ms.topologyId.Store(evt.TopologyId)
			}
		}
	}
}

// handleLeaderEvents reacts to Raft leadership transitions.
func (ms *MasterServer) handleLeaderEvents(ch <-chan consensus.Event) {
	for evt := range ch {
		if evt.Kind != consensus.EventLeaderChange {
			continue
		}
		isLeader := evt.IsLeader
		ms.isLeader.Store(isLeader)
		if isLeader {
			obs.MasterLeadershipGauge.Set(1)
			if err := ms.Raft.Barrier(10 * time.Second); err != nil {
				ms.logger.Warn("raft barrier failed on leadership", "error", err)
			}
			// Initialize topology ID only if it has never been set.
			if tid, _ := ms.topologyId.Load().(string); tid == "" {
				topologyID := generateTopologyID()
				if err := ms.Raft.Apply(raft.TopologyIdCommand{TopologyId: topologyID}, 5*time.Second); err != nil {
					ms.logger.Warn("failed to initialize topology id", "error", err)
				}
			}
		} else {
			obs.MasterLeadershipGauge.Set(0)
			ms.logger.Info("lost leadership")
		}
	}
}

// registerRoutes registers HTTP handlers for the master server.
func (ms *MasterServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /dir/assign", ms.handleAssign)
	mux.HandleFunc("GET /dir/lookup", ms.handleLookup)
	mux.HandleFunc("GET /dir/status", ms.handleStatus)
	mux.HandleFunc("POST /vol/grow", ms.handleVolGrow)
	mux.HandleFunc("POST /vol/vacuum", ms.handleVacuum)
	mux.HandleFunc("GET /cluster/healthz", ms.handleHealthz)
	mux.HandleFunc("GET /health", ms.handleHealthz)
	mux.HandleFunc("GET /ready", ms.handleReady)
	mux.HandleFunc("POST /cluster/peer", ms.handleAddPeer)
	mux.HandleFunc("DELETE /cluster/peer", ms.handleRemovePeer)
}

// Shutdown gracefully shuts down the master server.
func (ms *MasterServer) Shutdown() {
	ms.cancel()
	obs.MasterLeadershipGauge.Set(0)

	// Stop Raft
	if ms.Raft != nil {
		_ = ms.Raft.Shutdown()
	}

	// Close sequencer
	if ms.Sequencer != nil {
		_ = ms.Sequencer.Close()
	}
}

// ReloadSecurityConfig applies updated security settings to the live master server.
func (ms *MasterServer) ReloadSecurityConfig(secCfg *config.SecurityConfig) {
	if ms == nil || ms.Guard == nil || secCfg == nil {
		return
	}
	ms.Guard.SetWhiteList(secCfg.Guard.WhiteList)
	ms.Guard.SetSigningKey(secCfg.JWT.Signing.Key)
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
			if ms.Raft != nil && !ms.Raft.IsLeader() {
				continue
			}
			created, err := ms.Vg.CheckAndGrow(ms.ctx, req.Collection, req.ReplicaPlacement, req.Ttl, req.DiskType, 1)
			if err != nil {
				ms.logger.Warn("volume growth failed", "collection", req.Collection, "error", err)
				continue
			}
			ms.logger.Info("volume growth completed", "collection", req.Collection, "created", created)
		}
	}
}

// raftStatsWorker periodically reads Raft stats and updates Prometheus gauges.
func (ms *MasterServer) raftStatsWorker() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ms.ctx.Done():
			return
		case <-ticker.C:
			if ms.Raft == nil {
				continue
			}
			stats := ms.Raft.Stats()
			if v, err := strconv.ParseFloat(stats["commit_index"], 64); err == nil {
				obs.RaftCommitIndex.Set(v)
			}
			if v, err := strconv.ParseFloat(stats["last_log_index"], 64); err == nil {
				obs.RaftLastLogIndex.Set(v)
			}
			if v, err := strconv.ParseFloat(stats["last_snapshot_index"], 64); err == nil {
				obs.RaftLastSnapshotIndex.Set(v)
			}
			if v, err := strconv.ParseFloat(stats["num_peers"], 64); err == nil {
				obs.RaftNumPeers.Set(v)
			}
			switch lc := stats["last_contact"]; lc {
			case "", "0s":
				obs.RaftLastContactSeconds.Set(0)
			case "never":
				obs.RaftLastContactSeconds.Set(-1)
			default:
				if d, err := time.ParseDuration(lc); err == nil {
					obs.RaftLastContactSeconds.Set(d.Seconds())
				}
			}
		}
	}
}

func generateTopologyID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("topo-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}
