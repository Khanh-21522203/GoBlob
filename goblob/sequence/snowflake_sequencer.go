package sequence

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SnowflakeSequencer is a coordination-free ID generator inspired by Twitter's Snowflake.
// It generates unique IDs without any coordination by using:
// - Timestamp (milliseconds since epoch)
// - Node ID (configured per instance)
// - Sequence number (for same-millisecond collisions)
//
// ID structure: 63 bits total
// - 41 bits: timestamp (milliseconds since custom epoch, supports ~69 years)
// - 10 bits: node ID (supports up to 1024 nodes)
// - 12 bits: sequence (supports up to 4096 IDs per millisecond per node)
//
// Note: IDs are NOT guaranteed to be strictly monotonically increasing across
// the cluster, but they are unique. This is acceptable for some use cases.
type SnowflakeSequencer struct {
	mu          sync.Mutex
	nodeId      uint64
	epoch       time.Time
	lastTime    uint64
	sequence    uint64
	maxSequence uint64
	nodeIdShift uint64
	timeShift   uint64
}

const (
	maxSequenceValue = 4095 // 2^12 - 1
	nodeIdShiftBits  = 12
	timeShiftBits    = 22 // nodeIdShift + sequence bits
)

// SnowflakeConfig holds configuration for SnowflakeSequencer.
type SnowflakeConfig struct {
	// NodeId is the unique identifier for this node (0-1023).
	NodeId uint64

	// Epoch is the custom epoch time. Defaults to 2024-01-01 00:00:00 UTC.
	Epoch time.Time
}

// DefaultSnowflakeConfig returns sensible defaults.
func DefaultSnowflakeConfig() *SnowflakeConfig {
	return &SnowflakeConfig{
		NodeId: 0,
		Epoch:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// NewSnowflakeSequencer creates a new SnowflakeSequencer.
func NewSnowflakeSequencer(cfg *SnowflakeConfig) (*SnowflakeSequencer, error) {
	if cfg.NodeId > 1023 {
		return nil, fmt.Errorf("node_id must be between 0 and 1023")
	}

	return &SnowflakeSequencer{
		nodeId:      cfg.NodeId,
		epoch:       cfg.Epoch,
		maxSequence: maxSequenceValue,
		nodeIdShift: nodeIdShiftBits,
		timeShift:   timeShiftBits,
	}, nil
}

// NextFileId returns the next unique file ID.
func (s *SnowflakeSequencer) NextFileId(ctx context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.currentTime()

	if now == s.lastTime {
		// Same millisecond - increment sequence
		s.sequence++
		if s.sequence > s.maxSequence {
			// Sequence overflow - wait for next millisecond
			for now <= s.lastTime {
				time.Sleep(100 * time.Microsecond)
				now = s.currentTime()
			}
			s.sequence = 0
		}
	} else if now > s.lastTime {
		// New millisecond - reset sequence
		s.sequence = 0
		s.lastTime = now
	} else {
		// Clock moved backward - this is problematic
		// Wait until we catch up
		for now < s.lastTime {
			time.Sleep(100 * time.Microsecond)
			now = s.currentTime()
		}
		s.sequence = 0
		s.lastTime = now
	}

	// Generate ID: timestamp | node_id | sequence
	id := (now << s.timeShift) | (s.nodeId << s.nodeIdShift) | s.sequence

	return id, nil
}

// GetMaxFileId returns the approximate maximum file ID generated.
// Note: This is an approximation since we don't track every ID.
func (s *SnowflakeSequencer) GetMaxFileId() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.currentTime()
	if now == s.lastTime {
		return (now << s.timeShift) | (s.nodeId << s.nodeIdShift) | s.sequence
	}
	return (s.lastTime << s.timeShift) | (s.nodeId << s.nodeIdShift) | s.sequence
}

// Close gracefully shuts down the sequencer.
func (s *SnowflakeSequencer) Close() error {
	return nil
}

// currentTime returns the current time in milliseconds since the custom epoch.
func (s *SnowflakeSequencer) currentTime() uint64 {
	return uint64(time.Since(s.epoch).Milliseconds())
}
