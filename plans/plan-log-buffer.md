# Feature: Log Buffer (LocalMetaLogBuffer)

## 1. Purpose

The Log Buffer is the in-memory event bus that powers the Filer's metadata subscription system. Every file create/update/delete event is appended to the buffer. gRPC `SubscribeMetadata` and `SubscribeLocalMetadata` streams drain events from the buffer in real-time. Periodically, the buffer flushes accumulated events to volume servers as needle writes, creating a persistent log that new subscribers can replay to catch up on history they missed.

The `MetaAggregator` (peer filer sync) and the S3 gateway's bucket config cache both depend on the Log Buffer for event delivery.

## 2. Responsibilities

- **Event append**: Accept serialized `SubscribeMetadataResponse` protobuf messages and store them in a ring buffer
- **Live delivery**: Wake all waiting `SubscribeMetadata` gRPC streams when new events arrive
- **ReadAfter**: Return all events buffered after a given nanosecond timestamp (for subscriber catch-up within the buffer window)
- **Flush to volume**: Periodically write accumulated events to a volume server as a blob needle, creating a persistent log segment that survives buffer eviction
- **Log segment lookup**: Allow late-joining subscribers to read historical events from volume server log segments before the in-memory buffer window
- **Buffer rotation**: When the buffer is full, evict the oldest segment and replace with a fresh one; the evicted segment must already be flushed to volume

## 3. Non-Responsibilities

- Does not implement gRPC streaming (that is the Filer gRPC server)
- Does not store file metadata entries (that is the Filer Store)
- Does not manage volume assignment (that is the Filer's `AssignVolume` call)
- Does not implement the `SubscribeMetadata` RPC handler directly (that is the Filer gRPC server, which calls into this)

## 4. Architecture Design

```
+--------------------------------------------------------------+
|                     LocalMetaLogBuffer                        |
+--------------------------------------------------------------+
|                                                               |
|  Filer write path                                            |
|  (CreateEntry, UpdateEntry, DeleteEntry)                     |
|       |                                                      |
|       v                                                      |
|  AppendEntry(entry)                                          |
|       |                                                      |
|  +---------+                                                 |
|  | Ring    |  <-- segments[0..N-1]                           |
|  | Buffer  |  (each segment = list of events + minTs/maxTs) |
|  +---------+                                                 |
|       |              |                                       |
|       |              v                                       |
|       |      listenersCond.Broadcast()                       |
|       |      (wake all SubscribeMetadata goroutines)         |
|       |                                                      |
|       v                                                      |
|  Flush goroutine (every LogFlushIntervalSeconds)             |
|       |                                                      |
|       +--> Serialize events -> protobuf bytes                |
|       +--> Assign file ID from master                        |
|       +--> PUT bytes to volume server (needle write)         |
|       +--> Store log segment location in Filer KV            |
|            key: "__log__:<flushTsNs>"                        |
|            value: {fid, minTsNs, maxTsNs}                   |
|                                                              |
|  ReadAfter(sinceNs)                                          |
|       |                                                      |
|       +--> If sinceNs in buffer window: read from memory     |
|       +--> If sinceNs too old: read from volume log segments |
+--------------------------------------------------------------+
```

## 5. Core Data Structures (Go)

```go
package log_buffer

import (
    "sync"
    "time"
    "goblob/pb/filer_pb"
)

// LogBuffer is the in-memory ring buffer for metadata change events.
type LogBuffer struct {
    // segments holds the ring of log segments.
    // segments[lastSegmentIdx] is the active segment being appended to.
    segments     []*LogSegment
    segmentsMu   sync.RWMutex
    lastSegmentIdx int

    // maxSegmentCount is the number of segments to keep in memory before eviction.
    // Default: 3. Each segment holds up to maxSegmentSizeBytes bytes of events.
    maxSegmentCount int

    // maxSegmentSizeBytes is the soft limit per segment before rotation.
    // Default: 2 MB.
    maxSegmentSizeBytes int

    // flushInterval is how often to flush to volume servers.
    flushInterval time.Duration

    // notifyFn is called after every AppendEntry to wake subscribers.
    // In practice: listenersCond.Broadcast()
    notifyFn func()

    // flushFn is called to persist a full segment to volume servers.
    // Injected by FilerServer to avoid circular imports.
    flushFn FlushFn

    stopCh chan struct{}
    logger *slog.Logger
}

// LogSegment is one contiguous block of serialized metadata events.
type LogSegment struct {
    // buf holds the concatenation of length-prefixed protobuf-encoded
    // SubscribeMetadataResponse messages.
    buf []byte

    // startTsNs is the TsNs of the first event in this segment.
    startTsNs int64

    // stopTsNs is the TsNs of the last event appended.
    stopTsNs int64

    // isFlushed is true once this segment has been written to a volume server.
    isFlushed bool
}

// LogSegmentLocation is stored in Filer KV so late-joining subscribers can
// find historical log segments on volume servers.
type LogSegmentLocation struct {
    // FileId is the volume server needle ID (e.g., "5,abc123").
    FileId string

    // MinTsNs and MaxTsNs are the event timestamp range of this segment.
    MinTsNs int64
    MaxTsNs int64
}

// FlushFn is called by the flush goroutine to persist a segment.
// It uploads the raw bytes to a volume server and stores the location in Filer KV.
type FlushFn func(ctx context.Context, data []byte, startTsNs, stopTsNs int64) error

// ReadResult is returned by ReadFromBuffer.
type ReadResult struct {
    Events []*filer_pb.SubscribeMetadataResponse
    NextTs int64 // TsNs of the last event read; use as next sinceNs
}
```

## 6. Public Interfaces

```go
package log_buffer

// NewLogBuffer creates a Log Buffer with the given parameters.
//   maxSegmentCount:     number of in-memory segments to retain (default 3)
//   maxSegmentSizeBytes: soft max bytes per segment before rotation (default 2MB)
//   flushInterval:       how often to flush to volume servers
//   notifyFn:            called after every append (wake subscriber goroutines)
//   flushFn:             called when a segment is ready to be persisted
func NewLogBuffer(maxSegmentCount, maxSegmentSizeBytes int, flushInterval time.Duration, notifyFn func(), flushFn FlushFn, logger *slog.Logger) *LogBuffer

// AppendEntry serializes the entry as a SubscribeMetadataResponse and appends it to the buffer.
// It also calls notifyFn to wake any waiting gRPC stream goroutines.
func (lb *LogBuffer) AppendEntry(event *filer_pb.SubscribeMetadataResponse)

// ReadFromBuffer returns all events with TsNs > sinceNs that are still in the in-memory buffer.
// If sinceNs is older than the buffer window, callers must use ReadFromVolume to replay from disk.
func (lb *LogBuffer) ReadFromBuffer(sinceNs int64) ReadResult

// IsInBuffer reports whether sinceNs falls within the current buffer's time range.
// If false, the caller should read from volume log segments before using ReadFromBuffer.
func (lb *LogBuffer) IsInBuffer(sinceNs int64) bool

// StartFlushDaemon starts the background goroutine that flushes full segments to volume.
func (lb *LogBuffer) StartFlushDaemon(ctx context.Context)

// Stop shuts down the flush daemon and flushes any pending data synchronously.
func (lb *LogBuffer) Stop()
```

## 7. Internal Algorithms

### AppendEntry
```
AppendEntry(event):
  event.TsNs = time.Now().UnixNano()

  data = proto.Marshal(event)
  // Length-prefix the message (4-byte big-endian uint32)
  frame = append(uint32ToBytes(len(data)), data...)

  lb.segmentsMu.Lock()
  seg = lb.segments[lb.lastSegmentIdx]

  if len(seg.buf) + len(frame) > lb.maxSegmentSizeBytes:
    // Rotate: start a new segment
    lb.rotate()
    seg = lb.segments[lb.lastSegmentIdx]

  if seg.startTsNs == 0: seg.startTsNs = event.TsNs
  seg.buf = append(seg.buf, frame...)
  seg.stopTsNs = event.TsNs
  lb.segmentsMu.Unlock()

  // Wake all SubscribeMetadata goroutines
  lb.notifyFn()
```

### rotate (internal, called under segmentsMu write lock)
```
rotate():
  // Evict oldest segment (if buffer is full)
  if lb.activeSegmentCount() >= lb.maxSegmentCount:
    oldest = lb.oldestSegment()
    if !oldest.isFlushed:
      // Flush synchronously before eviction to avoid data loss
      lb.flushSegmentSync(oldest)
    lb.evictOldest()

  // Initialize new empty segment
  newSeg = &LogSegment{}
  lb.lastSegmentIdx = (lb.lastSegmentIdx + 1) % lb.maxSegmentCount
  lb.segments[lb.lastSegmentIdx] = newSeg
```

### ReadFromBuffer
```
ReadFromBuffer(sinceNs):
  lb.segmentsMu.RLock()
  defer lb.segmentsMu.RUnlock()

  events = []*filer_pb.SubscribeMetadataResponse{}
  nextTs = sinceNs

  // Scan all segments in chronological order
  for each seg in lb.segmentsInOrder():
    if seg == nil or seg.stopTsNs <= sinceNs: continue

    // Parse frames from seg.buf
    offset = 0
    for offset < len(seg.buf):
      frameLen = bytesToUint32(seg.buf[offset:offset+4])
      offset += 4
      eventBytes = seg.buf[offset:offset+int(frameLen)]
      offset += int(frameLen)

      event = &filer_pb.SubscribeMetadataResponse{}
      proto.Unmarshal(eventBytes, event)

      if event.TsNs > sinceNs:
        events = append(events, event)
        nextTs = event.TsNs

  return ReadResult{Events: events, NextTs: nextTs}
```

### Flush Daemon
```
StartFlushDaemon(ctx):
  go func():
    ticker = time.NewTicker(lb.flushInterval)
    for:
      select:
      case <-ticker.C:
        lb.flushFullSegments(ctx)
      case <-ctx.Done():
        lb.flushAllPending(ctx)  // final flush on shutdown
        return
```

### flushFullSegments
```
flushFullSegments(ctx):
  lb.segmentsMu.Lock()
  toFlush = []*LogSegment{}
  for each seg in lb.segmentsInOrder():
    if seg != nil and len(seg.buf) > 0 and !seg.isFlushed and seg != lb.currentSegment():
      toFlush = append(toFlush, seg)
  lb.segmentsMu.Unlock()

  for each seg in toFlush:
    if err = lb.flushFn(ctx, seg.buf, seg.startTsNs, seg.stopTsNs); err != nil:
      log.Error("flush failed", "err", err)
      continue
    lb.segmentsMu.Lock()
    seg.isFlushed = true
    lb.segmentsMu.Unlock()
```

### FlushFn implementation (provided by FilerServer)
```
flushFn(ctx, data, startTsNs, stopTsNs):
  // 1. Assign a file ID from master for the log needle
  assigned = filer.AssignVolume(ctx, &AssignVolumeRequest{Count: 1, Collection: "meta_log"})

  // 2. PUT the raw bytes to the volume server
  PUT http://{assigned.Url}/{assigned.FileId} with data

  // 3. Store location in Filer KV so subscribers can find it
  loc = LogSegmentLocation{FileId: assigned.FileId, MinTsNs: startTsNs, MaxTsNs: stopTsNs}
  filerStore.KvPut(ctx, logSegmentKey(startTsNs), marshalLocation(loc))
```

### ReadFromVolume (for late-joining subscribers)
```
// Called by SubscribeMetadata handler when sinceNs is before the buffer window.

ReadFromVolume(ctx, sinceNs, pathPrefix, eachEventFn):
  // Scan Filer KV for log segment locations with MaxTsNs > sinceNs
  // Key prefix: "__log_seg__:"
  // Keys are sorted by startTsNs so we can range-scan

  locations = scanLogSegmentLocations(ctx, sinceNs)
  for each loc in locations:
    // Download needle from volume server
    data = GET http://{volumeAddr}/{loc.FileId}

    // Parse frames
    offset = 0
    for offset < len(data):
      event = parseFrame(data, &offset)
      if event.TsNs <= sinceNs: continue
      if !strings.HasPrefix(event.Directory, pathPrefix): continue
      eachEventFn(event)
```

## 8. Persistence Model

**In-memory**: The ring buffer holds up to `maxSegmentCount × maxSegmentSizeBytes` bytes (default: 3 × 2 MB = 6 MB per filer instance).

**On-volume**: Flushed segments are stored as needles on volume servers. Their locations are indexed in Filer KV:

```
Key format: "__log_seg__:<startTsNs>"    (zero-padded 20-digit decimal for sort order)
Value:      JSON LogSegmentLocation{FileId, MinTsNs, MaxTsNs}
```

Flushed segments are retained for `max(LogFlushIntervalSeconds × 10, 600)` seconds, then deleted by a background goroutine that scans the Filer KV prefix and removes old entries.

## 9. Concurrency Model

| Mechanism | Usage |
|-----------|-------|
| `segmentsMu sync.RWMutex` | Protects the segments ring; append (write lock) vs read subscribers (read lock) |
| `notifyFn` (listenersCond.Broadcast) | Wakes all SubscribeMetadata goroutines after each append |
| Flush daemon goroutine | Single goroutine; no lock contention with the read path |
| Rotate under write lock | Segment rotation and eviction are done atomically under write lock |

The read path (`ReadFromBuffer`) holds a read lock for the duration of the scan. This is safe because no individual segment's `buf` slice is mutated after it is rotated out of active status — only the active segment is appended to.

## 10. Configuration

```go
// Configured via FilerOption:
type LogBufferConfig struct {
    // LogFlushIntervalSeconds is how often accumulated events are flushed to volume servers.
    // Default: 60 seconds.
    LogFlushIntervalSeconds int

    // LogBufferSegmentCount is the number of in-memory segments to retain.
    // Default: 3. Increase to support subscribers that are temporarily disconnected.
    LogBufferSegmentCount int

    // LogBufferSegmentSizeBytes is the soft size cap per segment before rotation.
    // Default: 2 MB.
    LogBufferSegmentSizeBytes int
}
```

## 11. Observability

- `obs.LogBufferAppendTotal.Inc()` per AppendEntry call
- `obs.LogBufferFlushTotal.WithLabelValues("ok"/"error").Inc()` per flush attempt
- `obs.LogBufferActiveSegments.Set(count)` — number of non-empty in-memory segments
- `obs.LogBufferBytesBuffered.Set(bytes)` — total bytes in all in-memory segments
- `obs.LogBufferFlushLatency.Observe(duration)` per flush
- Segment rotation logged at DEBUG with old segment timestamp range
- Flush errors logged at WARN with file ID and byte count

## 12. Testing Strategy

- **Unit tests**:
  - `TestAppendReadAfter`: append 5 events, ReadFromBuffer(sinceNs=0), assert all 5 returned
  - `TestReadAfterFiltered`: append events at t=1,2,3, ReadFromBuffer(sinceNs=2), assert only t=3 returned
  - `TestSegmentRotation`: fill one segment past maxSegmentSizeBytes, assert second segment created
  - `TestSegmentEviction`: fill maxSegmentCount+1 segments, assert oldest evicted and flushed
  - `TestIsInBuffer`: append events, assert IsInBuffer(recentTs)=true, IsInBuffer(oldTs)=false
  - `TestFlushDaemon`: mock flushFn, run daemon with short interval, assert flushFn called
  - `TestConcurrentAppendRead`: 10 goroutines appending while 5 goroutines reading, run with -race, assert no panics
  - `TestNotifyOnAppend`: mock notifyFn, append event, assert notifyFn called exactly once
  - `TestReadFromBufferEmptyBuffer`: ReadFromBuffer on empty buffer, assert empty result

- **Integration tests**:
  - `TestLogBufferEndToEnd`: create filer + mock volume server, append events, wait flush interval, assert events readable from volume via `ReadFromVolume`
  - `TestLateSubscriberCatchup`: start subscriber after 2 flush intervals have passed, assert it reads historical events from volume then transitions to live stream

## 13. Open Questions

None.
