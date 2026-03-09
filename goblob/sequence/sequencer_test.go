package sequence

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestFileSequencer(t *testing.T) {
	t.Run("new sequencer starts with correct ID", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			DataDir:  tmpDir,
			StepSize: 100,
		}

		seq, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		defer seq.Close()

		if got := seq.GetMaxFileId(); got != 0 {
			t.Errorf("expected max_file_id=0, got %d", got)
		}
	})

	t.Run("next file ID is monotonic", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			DataDir:  tmpDir,
			StepSize: 100,
		}

		seq, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		defer seq.Close()

		ctx := context.Background()

		var lastId uint64 = 0
		for i := 0; i < 100; i++ {
			id, err := seq.NextFileId(ctx)
			if err != nil {
				t.Fatalf("failed to get next file id: %v", err)
			}
			if id <= lastId {
				t.Errorf("expected id > %d, got %d", lastId, id)
			}
			lastId = id
		}
	})

	t.Run("persist and restore state", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			DataDir:  tmpDir,
			StepSize: 10,
		}

		// Create sequencer and generate some IDs
		seq1, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}

		ctx := context.Background()
		var lastId uint64
		for i := 0; i < 15; i++ {
			id, err := seq1.NextFileId(ctx)
			if err != nil {
				t.Fatalf("failed to get next file id: %v", err)
			}
			lastId = id
		}

		if err := seq1.Close(); err != nil {
			t.Fatalf("failed to close sequencer: %v", err)
		}

		// Create new sequencer - should restore state
		seq2, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create second sequencer: %v", err)
		}
		defer seq2.Close()

		// Next ID should be greater than the last one from first sequencer
		nextId, err := seq2.NextFileId(ctx)
		if err != nil {
			t.Fatalf("failed to get next file id: %v", err)
		}

		if nextId <= lastId {
			t.Errorf("expected next_id > %d, got %d", lastId, nextId)
		}
	})

	t.Run("concurrent access generates unique IDs", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			DataDir:  tmpDir,
			StepSize: 10000,
		}

		seq, err := NewFileSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		defer seq.Close()

		ctx := context.Background()
		numGoroutines := 100
		idsPerGoroutine := 10

		var wg sync.WaitGroup
		ids := make(chan uint64, numGoroutines*idsPerGoroutine)

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < idsPerGoroutine; j++ {
					id, err := seq.NextFileId(ctx)
					if err != nil {
						t.Errorf("failed to get next file id: %v", err)
						return
					}
					ids <- id
				}
			}()
		}

		wg.Wait()
		close(ids)

		// Check uniqueness
		seen := make(map[uint64]bool)
		for id := range ids {
			if seen[id] {
				t.Errorf("duplicate ID generated: %d", id)
			}
			seen[id] = true
		}

		if len(seen) != numGoroutines*idsPerGoroutine {
			t.Errorf("expected %d IDs, got %d", numGoroutines*idsPerGoroutine, len(seen))
		}
	})
}

func TestSnowflakeSequencer(t *testing.T) {
	t.Run("new sequencer starts correctly", func(t *testing.T) {
		cfg := DefaultSnowflakeConfig()
		cfg.NodeId = 1

		seq, err := NewSnowflakeSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		defer seq.Close()

		if got := seq.GetMaxFileId(); got == 0 {
			t.Error("expected non-zero max file id")
		}
	})

	t.Run("generates unique IDs concurrently", func(t *testing.T) {
		cfg := DefaultSnowflakeConfig()
		cfg.NodeId = 42

		seq, err := NewSnowflakeSequencer(cfg)
		if err != nil {
			t.Fatalf("failed to create sequencer: %v", err)
		}
		defer seq.Close()

		ctx := context.Background()
		numGoroutines := 50
		idsPerGoroutine := 100

		var wg sync.WaitGroup
		ids := make(chan uint64, numGoroutines*idsPerGoroutine)

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < idsPerGoroutine; j++ {
					id, err := seq.NextFileId(ctx)
					if err != nil {
						t.Errorf("failed to get next file id: %v", err)
						return
					}
					ids <- id
				}
			}()
		}

		wg.Wait()
		close(ids)

		// Check uniqueness
		seen := make(map[uint64]bool)
		for id := range ids {
			if seen[id] {
				t.Errorf("duplicate ID generated: %d", id)
			}
			seen[id] = true
		}

		if len(seen) != numGoroutines*idsPerGoroutine {
			t.Errorf("expected %d IDs, got %d", numGoroutines*idsPerGoroutine, len(seen))
		}
	})

	t.Run("different node IDs generate different sequences", func(t *testing.T) {
		cfg1 := DefaultSnowflakeConfig()
		cfg1.NodeId = 1
		cfg1.Epoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

		cfg2 := DefaultSnowflakeConfig()
		cfg2.NodeId = 2
		cfg2.Epoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

		seq1, err := NewSnowflakeSequencer(cfg1)
		if err != nil {
			t.Fatalf("failed to create sequencer 1: %v", err)
		}
		defer seq1.Close()

		seq2, err := NewSnowflakeSequencer(cfg2)
		if err != nil {
			t.Fatalf("failed to create sequencer 2: %v", err)
		}
		defer seq2.Close()

		ctx := context.Background()

		id1, _ := seq1.NextFileId(ctx)
		id2, _ := seq2.NextFileId(ctx)

		if id1 == id2 {
			t.Errorf("expected different IDs from different nodes, got %d", id1)
		}
	})

	t.Run("rejects invalid node ID", func(t *testing.T) {
		cfg := DefaultSnowflakeConfig()
		cfg.NodeId = 1024 // Too large

		_, err := NewSnowflakeSequencer(cfg)
		if err == nil {
			t.Error("expected error for node ID > 1023")
		}
	})
}
