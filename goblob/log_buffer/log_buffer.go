package log_buffer

import (
	"context"
	"encoding/binary"
	"log/slog"
	"sync"
	"time"
)

// FlushFn is called when a segment is ready to be flushed to durable storage.
type FlushFn func(ctx context.Context, startTsNs, stopTsNs int64, buf []byte) error

// LogSegment is one ring-buffer slot.
type LogSegment struct {
	buf       []byte
	startTsNs int64
	stopTsNs  int64
	isFlushed bool
}

// ReadResult is returned by ReadFromBuffer.
type ReadResult struct {
	Events [][]byte // raw proto bytes (decoded from frames)
	NextTs int64    // last event TsNs + 1, for use as next sinceNs
}

// LogBuffer is the ring buffer.
type LogBuffer struct {
	segments        []*LogSegment
	segmentsMu      sync.RWMutex
	lastSegmentIdx  int
	maxSegmentCount int
	maxSegmentBytes int
	flushInterval   time.Duration
	notifyFn        func()
	flushFn         FlushFn
	stopCh          chan struct{}
	stopOnce        sync.Once
	logger          *slog.Logger
}

// NewLogBuffer creates a new LogBuffer.
// maxSegmentCount defaults to 3 if <= 0.
// maxSegmentBytes defaults to 2*1024*1024 (2 MB) if <= 0.
func NewLogBuffer(
	maxSegmentCount int,
	maxSegmentBytes int,
	flushInterval time.Duration,
	notifyFn func(),
	flushFn FlushFn,
	logger *slog.Logger,
) *LogBuffer {
	if maxSegmentCount <= 0 {
		maxSegmentCount = 3
	}
	if maxSegmentBytes <= 0 {
		maxSegmentBytes = 2 * 1024 * 1024
	}
	if logger == nil {
		logger = slog.Default()
	}

	segments := make([]*LogSegment, maxSegmentCount)
	for i := range segments {
		segments[i] = &LogSegment{}
	}

	return &LogBuffer{
		segments:        segments,
		lastSegmentIdx:  0,
		maxSegmentCount: maxSegmentCount,
		maxSegmentBytes: maxSegmentBytes,
		flushInterval:   flushInterval,
		notifyFn:        notifyFn,
		flushFn:         flushFn,
		stopCh:          make(chan struct{}),
		logger:          logger,
	}
}

// AppendEntry appends raw bytes as a length-prefixed frame to the current segment.
// It rotates to a new segment when the current one would exceed maxSegmentBytes.
func (lb *LogBuffer) AppendEntry(data []byte) {
	now := time.Now().UnixNano()

	// Build the length-prefixed frame.
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(data)))
	copy(frame[4:], data)

	lb.segmentsMu.Lock()
	seg := lb.segments[lb.lastSegmentIdx]
	if len(seg.buf)+len(frame) > lb.maxSegmentBytes {
		lb.rotate()
		seg = lb.segments[lb.lastSegmentIdx]
	}
	seg.buf = append(seg.buf, frame...)
	if seg.startTsNs == 0 {
		seg.startTsNs = now
	}
	seg.stopTsNs = now
	lb.segmentsMu.Unlock()

	if lb.notifyFn != nil {
		lb.notifyFn()
	}
}

// rotate advances to the next segment slot, asynchronously flushing the evicted
// segment if it has unflushed data. Must be called under write lock.
func (lb *LogBuffer) rotate() {
	lb.lastSegmentIdx = (lb.lastSegmentIdx + 1) % lb.maxSegmentCount
	old := lb.segments[lb.lastSegmentIdx]
	if old != nil && !old.isFlushed && lb.flushFn != nil && len(old.buf) > 0 {
		// Capture values for the goroutine.
		startTsNs := old.startTsNs
		stopTsNs := old.stopTsNs
		buf := make([]byte, len(old.buf))
		copy(buf, old.buf)
		flushFn := lb.flushFn
		logger := lb.logger
		go func() {
			if err := flushFn(context.Background(), startTsNs, stopTsNs, buf); err != nil {
				logger.Error("log_buffer: async flush failed", "err", err)
			}
		}()
		old.isFlushed = true
	}
	lb.segments[lb.lastSegmentIdx] = &LogSegment{}
}

// ReadFromBuffer returns all raw proto bytes from segments whose stopTsNs > sinceNs.
func (lb *LogBuffer) ReadFromBuffer(sinceNs int64) ReadResult {
	lb.segmentsMu.RLock()
	defer lb.segmentsMu.RUnlock()

	var events [][]byte
	var nextTs int64

	for i := 0; i < lb.maxSegmentCount; i++ {
		idx := (lb.lastSegmentIdx + 1 + i) % lb.maxSegmentCount
		seg := lb.segments[idx]
		if seg == nil || len(seg.buf) == 0 {
			continue
		}
		if seg.stopTsNs <= sinceNs {
			continue
		}
		// Parse length-prefixed frames from this segment.
		buf := seg.buf
		for len(buf) >= 4 {
			size := binary.BigEndian.Uint32(buf[:4])
			if int(size) > len(buf)-4 {
				break
			}
			data := buf[4 : 4+size]
			events = append(events, data)
			buf = buf[4+size:]
		}
		nextTs = seg.stopTsNs + 1
	}

	return ReadResult{Events: events, NextTs: nextTs}
}

// IsInBuffer returns true if sinceNs falls within the oldest non-empty segment's
// time range, meaning the caller can be served entirely from the ring buffer.
func (lb *LogBuffer) IsInBuffer(sinceNs int64) bool {
	lb.segmentsMu.RLock()
	defer lb.segmentsMu.RUnlock()

	// Walk from oldest to newest to find the first non-empty segment.
	for i := 0; i < lb.maxSegmentCount; i++ {
		idx := (lb.lastSegmentIdx + 1 + i) % lb.maxSegmentCount
		seg := lb.segments[idx]
		if seg == nil || len(seg.buf) == 0 {
			continue
		}
		return sinceNs >= seg.startTsNs
	}
	return false
}

// StartFlushDaemon starts a background goroutine that periodically flushes
// old segments via flushFn. It stops when ctx is cancelled or Stop is called.
func (lb *LogBuffer) StartFlushDaemon(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(lb.flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				lb.flushOldSegments(ctx)
			case <-ctx.Done():
				lb.flushOldSegments(ctx)
				return
			case <-lb.stopCh:
				return
			}
		}
	}()
}

// flushOldSegments iterates non-current, non-flushed segments and calls flushFn
// synchronously. It is safe to call concurrently.
func (lb *LogBuffer) flushOldSegments(ctx context.Context) {
	if lb.flushFn == nil {
		return
	}

	lb.segmentsMu.Lock()
	// Collect segments to flush (all except the current active one).
	type segInfo struct {
		startTsNs int64
		stopTsNs  int64
		buf       []byte
		seg       *LogSegment
	}
	var toFlush []segInfo
	for i := 0; i < lb.maxSegmentCount; i++ {
		if i == lb.lastSegmentIdx {
			continue
		}
		seg := lb.segments[i]
		if seg == nil || seg.isFlushed || len(seg.buf) == 0 {
			continue
		}
		buf := make([]byte, len(seg.buf))
		copy(buf, seg.buf)
		toFlush = append(toFlush, segInfo{
			startTsNs: seg.startTsNs,
			stopTsNs:  seg.stopTsNs,
			buf:       buf,
			seg:       seg,
		})
		seg.isFlushed = true
	}
	lb.segmentsMu.Unlock()

	for _, info := range toFlush {
		if err := lb.flushFn(ctx, info.startTsNs, info.stopTsNs, info.buf); err != nil {
			lb.logger.Error("log_buffer: periodic flush failed", "err", err)
			// Revert isFlushed so the segment can be retried.
			lb.segmentsMu.Lock()
			info.seg.isFlushed = false
			lb.segmentsMu.Unlock()
		}
	}
}

// Stop signals the flush daemon to stop. Safe to call multiple times.
func (lb *LogBuffer) Stop() {
	lb.stopOnce.Do(func() {
		close(lb.stopCh)
	})
}
