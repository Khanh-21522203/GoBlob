package log_buffer

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// helper: build a LogBuffer with small defaults suitable for testing.
func newTestBuffer(maxBytes int, flushFn FlushFn) *LogBuffer {
	return NewLogBuffer(3, maxBytes, 100*time.Millisecond, nil, flushFn, nil)
}

// TestAppendReadBasic verifies that three appended entries are all returned by
// ReadFromBuffer(0).
func TestAppendReadBasic(t *testing.T) {
	lb := newTestBuffer(0, nil) // 0 → default 2 MB
	defer lb.Stop()

	entries := [][]byte{
		[]byte("event-1"),
		[]byte("event-2"),
		[]byte("event-3"),
	}
	for _, e := range entries {
		lb.AppendEntry(e)
	}

	result := lb.ReadFromBuffer(0)
	if len(result.Events) != len(entries) {
		t.Fatalf("expected %d events, got %d", len(entries), len(result.Events))
	}
	for i, e := range entries {
		if string(result.Events[i]) != string(e) {
			t.Errorf("event[%d]: want %q, got %q", i, e, result.Events[i])
		}
	}
}

// TestReadFromBufferSinceNs verifies that sinceNs filtering works at the segment
// level: reading after the first segment's stopTsNs returns only events from
// newer segments.
func TestReadFromBufferSinceNs(t *testing.T) {
	// Use a very small segment so a rotation happens after a few bytes.
	// Frame overhead is 4 bytes; "event-X" is 7 bytes → 11 bytes per entry.
	// Set maxSegmentBytes = 25 so segment rotates after 2 entries.
	lb := newTestBuffer(25, nil)
	defer lb.Stop()

	lb.AppendEntry([]byte("event-1"))
	lb.AppendEntry([]byte("event-2"))
	// Force a tiny sleep so timestamps differ reliably in tests.
	time.Sleep(time.Millisecond)

	// Capture the stopTsNs of whatever segment holds the first two events.
	lb.segmentsMu.RLock()
	// Walk oldest → newest to find the first non-empty segment.
	var firstStopTs int64
	for i := 0; i < lb.maxSegmentCount; i++ {
		idx := (lb.lastSegmentIdx + 1 + i) % lb.maxSegmentCount
		seg := lb.segments[idx]
		if seg != nil && len(seg.buf) > 0 {
			firstStopTs = seg.stopTsNs
			break
		}
	}
	lb.segmentsMu.RUnlock()

	lb.AppendEntry([]byte("event-3"))

	result := lb.ReadFromBuffer(firstStopTs)
	if len(result.Events) == 0 {
		t.Fatal("expected at least one event after firstStopTs, got none")
	}
	for _, ev := range result.Events {
		if string(ev) == "event-1" || string(ev) == "event-2" {
			t.Errorf("unexpected early event %q in filtered result", ev)
		}
	}
}

// TestSegmentRotation forces multiple rotations by using tiny segments and then
// verifies that ReadFromBuffer still returns accessible events without panicking.
func TestSegmentRotation(t *testing.T) {
	// Each frame = 4 + len("entry-XX") ≈ 12 bytes; set max to 15 so each
	// segment holds at most one entry before rotating.
	lb := newTestBuffer(15, nil)
	defer lb.Stop()

	const n = 10
	for i := 0; i < n; i++ {
		lb.AppendEntry([]byte("entry"))
	}

	result := lb.ReadFromBuffer(0)
	// With 3 segments of 1 entry each, at most 3 entries are accessible.
	if len(result.Events) == 0 {
		t.Fatal("ReadFromBuffer returned no events after rotation")
	}
	if len(result.Events) > lb.maxSegmentCount {
		t.Errorf("got %d events but ring buffer has only %d segments",
			len(result.Events), lb.maxSegmentCount)
	}
}

// TestFlushDaemonCalled verifies that the flush daemon eventually calls flushFn
// for old (non-current) segments.
func TestFlushDaemonCalled(t *testing.T) {
	var callCount atomic.Int32
	flushFn := func(_ context.Context, _, _ int64, _ []byte) error {
		callCount.Add(1)
		return nil
	}

	// Small segments so we have two filled, non-current segments to flush.
	lb := newTestBuffer(15, flushFn)
	defer lb.Stop()

	// Write enough to fill two segments (each holds ~1 entry at maxBytes=15).
	for i := 0; i < 5; i++ {
		lb.AppendEntry([]byte("entry"))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lb.StartFlushDaemon(ctx)

	// Give the daemon up to 1 second to flush.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if callCount.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("flushFn was not called within 1 second (call count = %d)", callCount.Load())
}

// TestConcurrentAppendRead runs concurrent appenders and readers to detect
// data races (run with -race).
func TestConcurrentAppendRead(t *testing.T) {
	lb := newTestBuffer(256, nil)
	defer lb.Stop()

	const (
		writers = 4
		readers = 4
		ops     = 100
	)

	var wg sync.WaitGroup

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				lb.AppendEntry([]byte("concurrent-event"))
			}
		}()
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				lb.ReadFromBuffer(0)
				lb.IsInBuffer(0)
			}
		}()
	}

	wg.Wait()
}
