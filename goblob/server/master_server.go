package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"log/slog"

	"GoBlob/goblob/cluster"
	"GoBlob/goblob/config"
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
	Raft                    raft.RaftServer
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

	// Create topology
	topo := topology.NewTopology()

	// Create volume growth manager
	vg := topology.NewVolumeGrowth(topo)

	// Create sequencer config.
	seqConfig := &sequence.Config{
		DataDir:  filepath.Join(opt.MetaDir, "sequencer"),
		StepSize: 10000,
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
		CommitTimeout:      50 * time.Millisecond,
		MaxAppendEntries:   32,
	}

	var (
		sequencer   sequence.Sequencer
		sequencerMu sync.RWMutex
		ms          *MasterServer
	)

	// Create FSM
	fsm := raft.NewMasterFSM(
		func(maxFileId uint64) {
			sequencerMu.RLock()
			s := sequencer
			sequencerMu.RUnlock()
			if s != nil {
				s.SetMax(maxFileId)
			}
		},
		func(maxVolumeId uint32) {
			ms.Topo.SetMaxVolumeId(maxVolumeId)
		},
		func(topologyId string) {
			if topologyId != "" {
				ms.topologyId.Store(topologyId)
			}
		},
	)

	// Create cluster registry
	clusterReg := cluster.NewClusterRegistry()

	// Create guard (empty whitelist = allow all)
	signingKey, _ := security.GenerateSigningKey(32)
	guard := security.NewGuard("", signingKey, "")

	ms = &MasterServer{
		Topo:                    topo,
		Vg:                      vg,
		Cluster:                 clusterReg,
		Guard:                   guard,
		option:                  opt,
		volumeGrowthRequestChan: make(chan *topology.VolumeGrowOption, 100),
		ctx:                     ctx,
		cancel:                  cancel,
		logger:                  logger,
	}

	raftServer, err := raft.NewRaftServer(raftConfig, fsm, func(isLeader bool) {
		ms.isLeader.Store(isLeader)
		if isLeader {
			obs.MasterLeadershipGauge.Set(1)
		} else {
			obs.MasterLeadershipGauge.Set(0)
		}
		if isLeader {
			if err := ms.Raft.Barrier(10 * time.Second); err != nil {
				ms.logger.Warn("raft barrier failed on leadership", "error", err)
			}
			if fsm.GetTopologyId() == "" {
				topologyID := generateTopologyID()
				if err := ms.Raft.Apply(raft.TopologyIdCommand{TopologyId: topologyID}, 5*time.Second); err != nil {
					ms.logger.Warn("failed to initialize topology id", "error", err)
				} else {
					ms.topologyId.Store(topologyID)
				}
			}
		} else {
			ms.logger.Info("lost leadership")
		}
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create Raft server: %w", err)
	}
	ms.Raft = raftServer

	raftSequencer, err := sequence.NewRaftSequencer(seqConfig, raftServer, logger)
	if err != nil {
		cancel()
		_ = raftServer.Shutdown()
		return nil, fmt.Errorf("failed to create raft sequencer: %w", err)
	}
	ms.Sequencer = raftSequencer
	sequencerMu.Lock()
	sequencer = raftSequencer
	sequencerMu.Unlock()
	if maxFileID := fsm.GetMaxFileId(); maxFileID > 0 {
		ms.Sequencer.SetMax(maxFileID)
	}
	if maxVolumeID := fsm.GetMaxVolumeId(); maxVolumeID > 0 {
		ms.Topo.SetMaxVolumeId(maxVolumeID)
	}
	ms.isLeader.Store(ms.Raft.IsLeader())
	if ms.Raft.IsLeader() {
		obs.MasterLeadershipGauge.Set(1)
	} else {
		obs.MasterLeadershipGauge.Set(0)
	}
	if topologyID := fsm.GetTopologyId(); topologyID != "" {
		ms.topologyId.Store(topologyID)
	}
	ms.Vg.SetMaxVolumeIdReplicator(func(maxVid uint32) error {
		if ms.Raft == nil {
			return fmt.Errorf("raft not initialized")
		}
		return ms.Raft.Apply(raft.MaxVolumeIdCommand{MaxVolumeId: maxVid}, 5*time.Second)
	})

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
