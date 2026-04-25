package hashicorpraft

import (
	"time"

	"GoBlob/goblob/consensus"
	"GoBlob/goblob/raft"
)

// Config preserves the current HashiCorp Raft configuration surface.
type Config = raft.RaftConfig

// Engine adapts the existing HashiCorp-backed raft implementation to the
// engine-neutral consensus boundary.
type Engine struct {
	inner consensus.Engine
}

var _ consensus.Engine = (*Engine)(nil)

// DefaultConfig returns the current HashiCorp Raft defaults.
func DefaultConfig() *Config {
	return raft.DefaultRaftConfig()
}

// NewEngine creates a HashiCorp-backed consensus engine.
//
// The current implementation still uses the master FSM from goblob/raft. Phase
// 2 intentionally keeps that ownership unchanged while routing runtime code
// through the adapter package.
func NewEngine(cfg *Config, fsm *raft.MasterFSM) (*Engine, error) {
	inner, err := raft.NewRaftServer(cfg, fsm)
	if err != nil {
		return nil, err
	}
	return &Engine{inner: inner}, nil
}

func (e *Engine) IsLeader() bool {
	return e.inner.IsLeader()
}

func (e *Engine) LeaderAddress() string {
	return e.inner.LeaderAddress()
}

func (e *Engine) Apply(cmd consensus.Command, timeout time.Duration) error {
	return e.inner.Apply(cmd, timeout)
}

func (e *Engine) Barrier(timeout time.Duration) error {
	return e.inner.Barrier(timeout)
}

func (e *Engine) AddPeer(addr string) error {
	return e.inner.AddPeer(addr)
}

func (e *Engine) RemovePeer(addr string) error {
	return e.inner.RemovePeer(addr)
}

func (e *Engine) Stats() map[string]string {
	return e.inner.Stats()
}

func (e *Engine) Shutdown() error {
	return e.inner.Shutdown()
}

func (e *Engine) Subscribe(name string, bufSize int) <-chan consensus.Event {
	return e.inner.Subscribe(name, bufSize)
}

func (e *Engine) Unsubscribe(name string) {
	e.inner.Unsubscribe(name)
}
