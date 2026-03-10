package sequence

import (
	"sync"
	"testing"
	"time"
)

func TestFileSequencer(t *testing.T) {
	t.Run("new sequencer starts with zero max", func(t *testing.T) {
		cfg := &Config{DataDir: t.TempDir(), StepSize: 100}
		seq, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		defer seq.Close()

		if got := seq.GetMax(); got != 0 {
			t.Errorf("expected GetMax()=0, got %d", got)
		}
	})

	t.Run("NextFileId is monotonic", func(t *testing.T) {
		cfg := &Config{DataDir: t.TempDir(), StepSize: 100}
		seq, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		defer seq.Close()

		var last uint64
		for i := 0; i < 200; i++ {
			id := seq.NextFileId(1)
			if id <= last {
				t.Errorf("non-monotonic: got %d after %d at iteration %d", id, last, i)
			}
			last = id
		}
	})

	t.Run("batch NextFileId returns non-overlapping ranges", func(t *testing.T) {
		cfg := &Config{DataDir: t.TempDir(), StepSize: 100}
		seq, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		defer seq.Close()

		start1 := seq.NextFileId(50)
		start2 := seq.NextFileId(50)

		// [start1, start1+49] must not overlap [start2, start2+49]
		end1 := start1 + 49
		if start2 <= end1 {
			t.Errorf("ranges overlap: [%d,%d] and [%d,%d]", start1, end1, start2, start2+49)
		}
	})

	t.Run("persist and restore", func(t *testing.T) {
		dir := t.TempDir()
		cfg := &Config{DataDir: dir, StepSize: 10}

		seq1, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		var lastId uint64
		for i := 0; i < 15; i++ {
			lastId = seq1.NextFileId(1)
		}
		if err := seq1.Close(); err != nil {
			t.Fatalf("failed to close: %v", err)
		}

		// New sequencer from same dir must continue without reusing IDs.
		seq2, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create second sequencer: %v", err)
		}
		defer seq2.Close()

		nextId := seq2.NextFileId(1)
		if nextId <= lastId {
			t.Errorf("expected next_id > %d after restart, got %d", lastId, nextId)
		}
	})

	t.Run("SetMax advances counter", func(t *testing.T) {
		cfg := &Config{DataDir: t.TempDir(), StepSize: 100}
		seq, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		defer seq.Close()

		seq.SetMax(99999)
		next := seq.NextFileId(1)
		if next <= 99999 {
			t.Errorf("expected next > 99999 after SetMax, got %d", next)
		}
	})

	t.Run("SetMax is idempotent below current", func(t *testing.T) {
		cfg := &Config{DataDir: t.TempDir(), StepSize: 100}
		seq, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		defer seq.Close()

		seq.NextFileId(50) // advance to 50
		seq.SetMax(10)     // smaller than current — should be no-op
		next := seq.NextFileId(1)
		if next <= 50 {
			t.Errorf("expected next > 50 after no-op SetMax(10), got %d", next)
		}
	})

	t.Run("concurrent access generates unique IDs", func(t *testing.T) {
		cfg := &Config{DataDir: t.TempDir(), StepSize: 10000}
		seq, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		defer seq.Close()

		const goroutines = 100
		const perGoroutine = 10

		var wg sync.WaitGroup
		ids := make(chan uint64, goroutines*perGoroutine)

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < perGoroutine; j++ {
					ids <- seq.NextFileId(1)
				}
			}()
		}

		wg.Wait()
		close(ids)

		seen := make(map[uint64]bool)
		for id := range ids {
			if seen[id] {
				t.Errorf("duplicate ID: %d", id)
			}
			seen[id] = true
		}
		if len(seen) != goroutines*perGoroutine {
			t.Errorf("expected %d unique IDs, got %d", goroutines*perGoroutine, len(seen))
		}
	})
}

func TestSnowflakeSequencer(t *testing.T) {
	t.Run("new sequencer generates non-zero IDs", func(t *testing.T) {
		seq, err := NewSnowflakeSequencer(&SnowflakeConfig{NodeId: 1, Epoch: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		if id := seq.NextFileId(1); id == 0 {
			t.Error("expected non-zero ID")
		}
	})

	t.Run("generates unique IDs concurrently", func(t *testing.T) {
		seq, err := NewSnowflakeSequencer(DefaultSnowflakeConfig())
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}

		const goroutines = 50
		const perGoroutine = 100

		var wg sync.WaitGroup
		ids := make(chan uint64, goroutines*perGoroutine)

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < perGoroutine; j++ {
					ids <- seq.NextFileId(1)
				}
			}()
		}
		wg.Wait()
		close(ids)

		seen := make(map[uint64]bool)
		for id := range ids {
			if seen[id] {
				t.Errorf("duplicate ID: %d", id)
			}
			seen[id] = true
		}
	})

	t.Run("different node IDs produce different IDs", func(t *testing.T) {
		epoch := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		seq1, _ := NewSnowflakeSequencer(&SnowflakeConfig{NodeId: 1, Epoch: epoch})
		seq2, _ := NewSnowflakeSequencer(&SnowflakeConfig{NodeId: 2, Epoch: epoch})

		id1 := seq1.NextFileId(1)
		id2 := seq2.NextFileId(1)
		if id1 == id2 {
			t.Errorf("expected different IDs from different nodes, both got %d", id1)
		}
	})

	t.Run("rejects invalid node ID", func(t *testing.T) {
		_, err := NewSnowflakeSequencer(&SnowflakeConfig{NodeId: 1024})
		if err == nil {
			t.Error("expected error for node ID > 1023")
		}
	})

	t.Run("GetMax returns non-zero after generating IDs", func(t *testing.T) {
		seq, _ := NewSnowflakeSequencer(DefaultSnowflakeConfig())
		seq.NextFileId(1)
		if seq.GetMax() == 0 {
			t.Error("expected non-zero GetMax after generating IDs")
		}
	})

	t.Run("SetMax is a no-op", func(t *testing.T) {
		seq, _ := NewSnowflakeSequencer(DefaultSnowflakeConfig())
		before := seq.NextFileId(1)
		seq.SetMax(before - 1) // no-op: smaller than current
		after := seq.NextFileId(1)
		if after <= before {
			t.Errorf("expected after > before, got after=%d before=%d", after, before)
		}
	})
}
