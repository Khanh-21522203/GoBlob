package raft

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
)

// RaftServer manages the Raft consensus layer for the master server.
type RaftServer struct {
	cfg       *RaftConfig
	raft      *raft.Raft
	fsm       *MasterFSM
	transport raft.Transport

	// onLeaderChange is called when the leader status changes.
	// IMPORTANT: This callback must not block as it's called from
	// the Raft goroutine. Use a channel or goroutine if needed.
	onLeaderChange func(isLeader bool)

	// leaderCh tracks leader status changes.
	leaderCh      chan bool
	leaderChClose chan struct{}

	mu sync.RWMutex
}

// NewRaftServer creates a new RaftServer.
func NewRaftServer(cfg *RaftConfig, fsm *MasterFSM, onLeaderChange func(bool)) (*RaftServer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Create data directory if it doesn't exist
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}

	rs := &RaftServer{
		cfg:           cfg,
		fsm:           fsm,
		onLeaderChange: onLeaderChange,
		leaderCh:      make(chan bool, 1),
		leaderChClose: make(chan struct{}),
	}

	if err := rs.setupRaft(); err != nil {
		return nil, fmt.Errorf("failed to setup raft: %w", err)
	}

	// Start leader observer goroutine
	go rs.observeLeader()

	return rs, nil
}

// setupRaft configures and initializes the Raft instance.
func (rs *RaftServer) setupRaft() error {
	// Create TCP transport
	addr, err := net.ResolveTCPAddr("tcp", rs.cfg.BindAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve address: %w", err)
	}

	transport, err := raft.NewTCPTransport(rs.cfg.BindAddr, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to create transport: %w", err)
	}
	rs.transport = transport

	// Create BoltDB store for stable storage
	// Use the same store for both log and stable storage
	boltDB, err := raftboltdb.NewBoltStore(filepath.Join(rs.cfg.DataDir, "raft.db"))
	if err != nil {
		return fmt.Errorf("failed to create bolt store: %w", err)
	}

	// Create snapshot store
	snapshots, err := raft.NewFileSnapshotStore(rs.cfg.DataDir, 1, os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to create snapshot store: %w", err)
	}

	// Use BoltDB for both log and stable store
	logStore := boltDB
	stableStore := boltDB
	if err != nil {
		return fmt.Errorf("failed to create log store: %w", err)
	}

	// Create Raft configuration
	raftCfg := raft.DefaultConfig()
	raftCfg.LocalID = raft.ServerID(rs.cfg.BindAddr)
	raftCfg.SnapshotThreshold = rs.cfg.SnapshotThreshold
	raftCfg.SnapshotInterval = rs.cfg.SnapshotInterval
	raftCfg.HeartbeatTimeout = rs.cfg.HeartbeatTimeout
	raftCfg.ElectionTimeout = rs.cfg.ElectionTimeout
	raftCfg.LeaderLeaseTimeout = rs.cfg.LeaderLeaseTimeout
	raftCfg.CommitTimeout = rs.cfg.CommitTimeout
	// Use default logger (hclog)

	// Bootstrap if in single-node mode
	if rs.cfg.Bootstrap {
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      raftCfg.LocalID,
					Address: transport.LocalAddr(),
				},
			},
		}
		err = raft.BootstrapCluster(raftCfg, logStore, stableStore, snapshots, transport, configuration)
		if err != nil && err != raft.ErrCantBootstrap {
			return fmt.Errorf("failed to bootstrap: %w", err)
		}
	}

	// Create Raft instance
	r, err := raft.NewRaft(raftCfg, rs.fsm, logStore, stableStore, snapshots, transport)
	if err != nil {
		return fmt.Errorf("failed to create raft: %w", err)
	}

	rs.raft = r
	return nil
}

// observeLeader watches for leader changes and calls the callback.
func (rs *RaftServer) observeLeader() {
	for {
		select {
		case isLeader := <-rs.leaderCh:
			if rs.onLeaderChange != nil {
				// Call in goroutine to avoid blocking Raft
				go rs.onLeaderChange(isLeader)
			}
		case <-rs.raft.LeaderCh():
			// Raft leader change detected
			isLeader := rs.IsLeader()
			select {
			case rs.leaderCh <- isLeader:
			default:
				// Channel already has a pending update
			}
		case <-rs.leaderChClose:
			return
		}
	}
}

// IsLeader returns true if this node is the Raft leader.
func (rs *RaftServer) IsLeader() bool {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.raft.State() == raft.Leader
}

// LeaderAddress returns the address of the current cluster leader.
func (rs *RaftServer) LeaderAddress() string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	addr := string(rs.raft.Leader())
	if addr == "" {
		return ""
	}
	return addr
}

// ApplyCommand applies a command to the Raft log.
// This method should only be called on the leader.
func (rs *RaftServer) ApplyCommand(ctx context.Context, cmd *LogCommand) error {
	if !rs.IsLeader() {
		return fmt.Errorf("not leader, current leader: %s", rs.LeaderAddress())
	}

	data, err := cmd.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}

	future := rs.raft.Apply(data, 0)

	// Create a channel to receive the error
	errCh := make(chan error, 1)
	go func() {
		errCh <- future.Error()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("failed to apply command: %w", err)
		}
		// Also check FSM response for errors
		response := future.Response()
		if response != nil {
			if err, ok := response.(error); ok && err != nil {
				return fmt.Errorf("fsm returned error: %w", err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GetMaxFileId returns the current maximum file ID from the FSM.
func (rs *RaftServer) GetMaxFileId() uint64 {
	return rs.fsm.GetMaxFileId()
}

// AddVoter adds a voting node to the Raft cluster.
func (rs *RaftServer) AddVoter(id, address string) error {
	future := rs.raft.AddVoter(raft.ServerID(id), raft.ServerAddress(address), 0, 0)
	if err := future.Error(); err != nil {
		return fmt.Errorf("failed to add voter: %w", err)
	}
	return nil
}

// RemoveServer removes a server from the Raft cluster.
func (rs *RaftServer) RemoveServer(id string) error {
	future := rs.raft.RemoveServer(raft.ServerID(id), 0, 0)
	if err := future.Error(); err != nil {
		return fmt.Errorf("failed to remove server: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the Raft server.
func (rs *RaftServer) Shutdown() error {
	close(rs.leaderChClose)

	if rs.raft != nil {
		future := rs.raft.Shutdown()
		if err := future.Error(); err != nil {
			return fmt.Errorf("failed to shutdown raft: %w", err)
		}
	}

	// Note: Transport is closed by Raft itself during shutdown

	return nil
}

// State returns the current Raft state.
func (rs *RaftServer) State() raft.RaftState {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if rs.raft == nil {
		return raft.Shutdown
	}
	return rs.raft.State()
}

// Marshal converts the LogCommand to JSON.
func (cmd *LogCommand) Marshal() ([]byte, error) {
	// Wrap to handle any data type
	wrapped := struct {
		Type LogCommandType `json:"type"`
		Data any            `json:"data"`
	}{
		Type: cmd.Type,
		Data: cmd.Data,
	}
	return jsonEncode(wrapped)
}

// UnmarshalLogCommand creates a LogCommand from JSON data.
func UnmarshalLogCommand(data []byte) (*LogCommand, error) {
	var cmd LogCommand
	if err := jsonDecode(data, &cmd); err != nil {
		return nil, err
	}
	return &cmd, nil
}

// Simple JSON helpers
func jsonEncode(v any) ([]byte, error) {
	return json.Marshal(v)
}

func jsonDecode(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
