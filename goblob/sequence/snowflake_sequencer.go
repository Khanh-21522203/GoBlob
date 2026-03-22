package sequence

import (
	"fmt"
	"sync"
	"time"
)

// SnowflakeSequencer is a coordination-free ID generator inspired by Twitter's Snowflake.
// It generates unique IDs without any coordination by combining:
//   - 41 bits: timestamp (milliseconds since custom epoch, ~69 years)
//   - 10 bits: node ID (0–1023)
//   - 12 bits: sequence (4096 IDs per millisecond per node)
//
// IDs are NOT strictly monotonically increasing across the cluster, but they
// are unique per node. This is acceptable when cross-node monotonicity is not required.
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
	// NodeId is the unique identifier for this node (0–1023).
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
		return nil, fmt.Errorf("node_id must be between 0 and 1023, got %d", cfg.NodeId)
	}
	return &SnowflakeSequencer{
		nodeId:      cfg.NodeId,
		epoch:       cfg.Epoch,
		maxSequence: maxSequenceValue,
		nodeIdShift: nodeIdShiftBits,
		timeShift:   timeShiftBits,
	}, nil
}

// NextFileId returns the start of a batch of unique IDs.
// For Snowflake, each call generates one ID regardless of count,
// since IDs embed a timestamp and are not pre-allocated.
func (s *SnowflakeSequencer) NextFileId(count uint64) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.currentTime()

	switch {
	case now == s.lastTime:
		s.sequence++
		if s.sequence > s.maxSequence {
			// Sequence overflow — spin until the next millisecond.
			for now <= s.lastTime {
				time.Sleep(100 * time.Microsecond)
				now = s.currentTime()
			}
			s.sequence = 0
			s.lastTime = now // advance lastTime so the next call doesn't reset sequence again
		}
	case now > s.lastTime:
		s.sequence = 0
		s.lastTime = now
	default:
		// Clock moved backward — wait to catch up.
		for now < s.lastTime {
			time.Sleep(100 * time.Microsecond)
			now = s.currentTime()
		}
		// now >= s.lastTime here; update state
		s.sequence = 0
		s.lastTime = now
	}

	return (now << s.timeShift) | (s.nodeId << s.nodeIdShift) | s.sequence
}

// SetMax is a no-op for SnowflakeSequencer because IDs are time-based.
func (s *SnowflakeSequencer) SetMax(_ uint64) {}

// GetMax returns the approximate maximum ID generated so far.
func (s *SnowflakeSequencer) GetMax() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return (s.lastTime << s.timeShift) | (s.nodeId << s.nodeIdShift) | s.sequence
}

// Close is a no-op for SnowflakeSequencer.
func (s *SnowflakeSequencer) Close() error { return nil }

// currentTime returns milliseconds since the custom epoch.
func (s *SnowflakeSequencer) currentTime() uint64 {
	return uint64(time.Since(s.epoch).Milliseconds())
}
