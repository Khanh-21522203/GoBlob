package async

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/obs"
	"GoBlob/goblob/pb/filer_pb"
)

var (
	registerMetricsOnce sync.Once

	ReplicationLagSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "goblob",
		Subsystem: "replication",
		Name:      "lag_seconds",
		Help:      "Replication lag in seconds.",
	}, []string{"target_cluster"})
	ReplicatedEntriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "goblob",
		Subsystem: "replication",
		Name:      "replicated_entries_total",
		Help:      "Total replicated metadata events.",
	}, []string{"target_cluster", "status"})
)

func init() {
	registerMetricsOnce.Do(func() {
		prometheus.MustRegister(ReplicationLagSeconds, ReplicatedEntriesTotal)
	})
}

// Config controls one source->target async replication pipeline.
type Config struct {
	SourceCluster string
	SourceFiler   string
	TargetCluster string
	TargetFiler   string
	BatchSize     int
	FlushInterval time.Duration
	PathPrefix    string
}

type filerClient interface {
	SubscribeMetadata(ctx context.Context, in *filer_pb.SubscribeMetadataRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[filer_pb.SubscribeMetadataResponse], error)
	LookupDirectoryEntry(ctx context.Context, in *filer_pb.LookupDirectoryEntryRequest, opts ...grpc.CallOption) (*filer_pb.LookupDirectoryEntryResponse, error)
	CreateEntry(ctx context.Context, in *filer_pb.CreateEntryRequest, opts ...grpc.CallOption) (*filer_pb.CreateEntryResponse, error)
	UpdateEntry(ctx context.Context, in *filer_pb.UpdateEntryRequest, opts ...grpc.CallOption) (*filer_pb.UpdateEntryResponse, error)
	DeleteEntry(ctx context.Context, in *filer_pb.DeleteEntryRequest, opts ...grpc.CallOption) (*filer_pb.DeleteEntryResponse, error)
	LookupVolume(ctx context.Context, in *filer_pb.LookupVolumeRequest, opts ...grpc.CallOption) (*filer_pb.LookupVolumeResponse, error)
	AssignVolume(ctx context.Context, in *filer_pb.AssignVolumeRequest, opts ...grpc.CallOption) (*filer_pb.AssignVolumeResponse, error)
}

// Status describes the latest replication state for one target cluster.
type Status struct {
	TargetCluster string
	LagSeconds    float64
	LastEventTsNs int64
	Replicated    uint64
	LastError     string
	UpdatedAt     time.Time
}

var (
	statusMu sync.RWMutex
	statuses = map[string]Status{}
)

// SnapshotStatuses returns a copy of all known replication statuses.
func SnapshotStatuses() []Status {
	statusMu.RLock()
	defer statusMu.RUnlock()
	out := make([]Status, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, st)
	}
	return out
}

func setStatus(st Status) {
	statusMu.Lock()
	defer statusMu.Unlock()
	statuses[st.TargetCluster] = st
}

// Replicator consumes source filer metadata events and applies them to target filer.
type Replicator struct {
	config Config

	source filerClient
	target filerClient
	http   *http.Client

	lagMetric     prometheus.Gauge
	entriesMetric *prometheus.CounterVec

	logger *obsLogger
}

type obsLogger struct{}

func (l *obsLogger) Info(msg string, args ...any) { obs.New("replication").Info(msg, args...) }
func (l *obsLogger) Warn(msg string, args ...any) { obs.New("replication").Warn(msg, args...) }

func NewReplicator(config Config) (*Replicator, error) {
	if config.TargetCluster == "" {
		return nil, fmt.Errorf("target cluster is required")
	}
	if config.SourceFiler == "" {
		return nil, fmt.Errorf("source filer is required")
	}
	if config.TargetFiler == "" {
		return nil, fmt.Errorf("target filer is required")
	}
	r := &Replicator{
		config:        config,
		lagMetric:     ReplicationLagSeconds.WithLabelValues(config.TargetCluster),
		entriesMetric: ReplicatedEntriesTotal,
		http:          &http.Client{Timeout: 30 * time.Second},
		logger:        &obsLogger{},
	}
	return r, nil
}

// SetClients overrides gRPC clients for tests or advanced embedding.
func (r *Replicator) SetClients(source, target filerClient) {
	r.source = source
	r.target = target
}

func (r *Replicator) Start(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if r.source == nil || r.target == nil {
		source, sourceConn, err := dialFiler(r.config.SourceFiler)
		if err != nil {
			return err
		}
		defer sourceConn.Close()
		target, targetConn, err := dialFiler(r.config.TargetFiler)
		if err != nil {
			return err
		}
		defer targetConn.Close()
		r.source = source
		r.target = target
	}

	clientID := int32(time.Now().UnixNano() & 0x7fffffff)
	stream, err := r.source.SubscribeMetadata(ctx, &filer_pb.SubscribeMetadataRequest{
		PathPrefix: r.config.PathPrefix,
		SinceNs:    0,
		ClientId:   clientID,
		ClientName: "replicator-" + r.config.SourceCluster,
	})
	if err != nil {
		return fmt.Errorf("subscribe metadata: %w", err)
	}

	for {
		event, err := stream.Recv()
		if err == io.EOF || errors.Is(err, context.Canceled) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("receive metadata event: %w", err)
		}
		if err := r.handleEvent(ctx, event); err != nil {
			r.record("error", event.GetTsNs(), err)
			r.logger.Warn("failed to replicate event", "error", err)
			continue
		}
		r.record("ok", event.GetTsNs(), nil)
	}
}

func (r *Replicator) handleEvent(ctx context.Context, event *filer_pb.SubscribeMetadataResponse) error {
	if event == nil || event.GetEventNotification() == nil {
		return nil
	}
	n := event.GetEventNotification()
	if n.GetNewEntry() != nil && n.GetOldEntry() == nil {
		return r.replicateUpsert(ctx, event.GetDirectory(), n.GetNewEntry().GetName(), event.GetTsNs())
	}
	if n.GetNewEntry() != nil && n.GetOldEntry() != nil {
		// Rename: apply destination then remove source.
		if err := r.replicateUpsert(ctx, n.GetNewParentPath(), n.GetNewEntry().GetName(), event.GetTsNs()); err != nil {
			return err
		}
		return r.replicateDelete(ctx, event.GetDirectory(), n.GetOldEntry().GetName(), event.GetTsNs())
	}
	if n.GetOldEntry() != nil && n.GetNewEntry() == nil {
		return r.replicateDelete(ctx, event.GetDirectory(), n.GetOldEntry().GetName(), event.GetTsNs())
	}
	return nil
}

func (r *Replicator) replicateUpsert(ctx context.Context, directory, name string, eventTsNs int64) error {
	if name == "" {
		return nil
	}
	srcEntry, err := lookupEntry(ctx, r.source, directory, name)
	if err != nil {
		return err
	}
	entryToWrite := cloneEntry(srcEntry)
	if err := r.replicateChunks(ctx, entryToWrite); err != nil {
		return err
	}

	targetEntry, err := lookupEntry(ctx, r.target, directory, name)
	if err == nil && isTargetNewer(targetEntry, srcEntry) {
		r.record("skipped_stale", eventTsNs, nil)
		return nil
	}
	if err != nil && !isNotFound(err) {
		return err
	}

	if targetEntry == nil {
		if _, err := r.target.CreateEntry(ctx, &filer_pb.CreateEntryRequest{Directory: directory, Entry: cloneEntry(entryToWrite)}); err != nil {
			if isAlreadyExists(err) {
				_, err = r.target.UpdateEntry(ctx, &filer_pb.UpdateEntryRequest{Directory: directory, Entry: cloneEntry(entryToWrite)})
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	_, err = r.target.UpdateEntry(ctx, &filer_pb.UpdateEntryRequest{Directory: directory, Entry: cloneEntry(entryToWrite)})
	return err
}

func (r *Replicator) replicateChunks(ctx context.Context, entry *filer_pb.Entry) error {
	if r == nil || entry == nil || len(entry.GetChunks()) == 0 {
		return nil
	}
	for _, chunk := range entry.GetChunks() {
		if chunk == nil || chunk.GetFileId() == "" {
			continue
		}
		payload, err := r.readChunkFromSource(ctx, chunk.GetFileId())
		if err != nil {
			return err
		}
		newFID, err := r.writeChunkToTarget(ctx, entry, payload)
		if err != nil {
			return err
		}
		chunk.FileId = newFID
	}
	return nil
}

func (r *Replicator) readChunkFromSource(ctx context.Context, fid string) ([]byte, error) {
	parsed, err := types.ParseFileId(fid)
	if err != nil {
		return nil, fmt.Errorf("parse source fid %q: %w", fid, err)
	}
	token := strconv.FormatUint(uint64(parsed.VolumeId), 10)
	resp, err := r.source.LookupVolume(ctx, &filer_pb.LookupVolumeRequest{VolumeIds: []string{token}})
	if err != nil {
		return nil, fmt.Errorf("lookup source volume %s: %w", token, err)
	}
	locs := resp.GetLocationsMap()[token]
	if locs == nil || len(locs.GetLocations()) == 0 {
		return nil, fmt.Errorf("no source volume locations for %s", token)
	}
	addr := locs.GetLocations()[0].GetUrl()
	if addr == "" {
		addr = locs.GetLocations()[0].GetPublicUrl()
	}
	if addr == "" {
		return nil, fmt.Errorf("empty source volume address for %s", token)
	}

	url := "http://" + addr + "/" + fid
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpClient := r.http
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	httpResp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read source chunk %s status=%d", fid, httpResp.StatusCode)
	}
	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (r *Replicator) writeChunkToTarget(ctx context.Context, entry *filer_pb.Entry, data []byte) (string, error) {
	assignReq := &filer_pb.AssignVolumeRequest{
		Count:       1,
		Collection:  entry.GetAttributes().GetCollection(),
		Replication: entry.GetAttributes().GetReplication(),
		DiskType:    entry.GetAttributes().GetDiskType(),
	}
	assignResp, err := r.target.AssignVolume(ctx, assignReq)
	if err != nil {
		return "", fmt.Errorf("assign target volume: %w", err)
	}
	if assignResp.GetError() != "" {
		return "", fmt.Errorf("assign target volume: %s", assignResp.GetError())
	}
	fid := assignResp.GetFileId()
	if fid == "" {
		return "", fmt.Errorf("empty assigned target file id")
	}
	addr := assignResp.GetUrl()
	if addr == "" {
		addr = assignResp.GetPublicUrl()
	}
	if addr == "" {
		return "", fmt.Errorf("empty assigned target volume address")
	}
	url := "http://" + addr + "/" + fid
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.ContentLength = int64(len(data))
	httpClient := r.http
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	httpResp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusCreated && httpResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("write target chunk status=%d", httpResp.StatusCode)
	}
	return fid, nil
}

func (r *Replicator) replicateDelete(ctx context.Context, directory, name string, eventTsNs int64) error {
	if name == "" {
		return nil
	}
	targetEntry, err := lookupEntry(ctx, r.target, directory, name)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if targetEntry != nil {
		targetMtimeNs := targetEntry.GetAttributes().GetMtime() * int64(time.Second)
		if targetMtimeNs > eventTsNs {
			r.record("skipped_stale", eventTsNs, nil)
			return nil
		}
	}
	_, err = r.target.DeleteEntry(ctx, &filer_pb.DeleteEntryRequest{Directory: directory, Name: name, IsDeleteData: false})
	return err
}

func (r *Replicator) record(statusLabel string, eventTsNs int64, err error) {
	if r == nil {
		return
	}
	if statusLabel != "" {
		r.entriesMetric.WithLabelValues(r.config.TargetCluster, statusLabel).Inc()
	}
	lagSeconds := 0.0
	if eventTsNs > 0 {
		lagSeconds = time.Since(time.Unix(0, eventTsNs)).Seconds()
		if lagSeconds < 0 {
			lagSeconds = 0
		}
		r.lagMetric.Set(lagSeconds)
	}
	current := Status{
		TargetCluster: r.config.TargetCluster,
		LagSeconds:    lagSeconds,
		LastEventTsNs: eventTsNs,
		UpdatedAt:     time.Now().UTC(),
	}
	if previous, ok := findStatus(r.config.TargetCluster); ok {
		current.Replicated = previous.Replicated
	}
	if statusLabel == "ok" {
		current.Replicated++
	}
	if err != nil {
		current.LastError = err.Error()
	}
	setStatus(current)
}

func findStatus(cluster string) (Status, bool) {
	statusMu.RLock()
	defer statusMu.RUnlock()
	st, ok := statuses[cluster]
	return st, ok
}

func dialFiler(addr string) (filerClient, *grpc.ClientConn, error) {
	grpcAddr := string(types.ServerAddress(addr).ToGrpcAddress())
	conn, err := grpc.NewClient(
		grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(64<<20),
			grpc.MaxCallSendMsgSize(64<<20),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("dial filer %s: %w", grpcAddr, err)
	}
	return filer_pb.NewFilerServiceClient(conn), conn, nil
}

func lookupEntry(ctx context.Context, client filerClient, directory, name string) (*filer_pb.Entry, error) {
	resp, err := client.LookupDirectoryEntry(ctx, &filer_pb.LookupDirectoryEntryRequest{Directory: cleanDir(directory), Name: name})
	if err != nil {
		return nil, err
	}
	if resp.GetEntry() == nil {
		return nil, status.Error(codes.NotFound, "entry not found")
	}
	return resp.GetEntry(), nil
}

func isNotFound(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.NotFound
}

func isAlreadyExists(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.AlreadyExists
}

func isTargetNewer(target, source *filer_pb.Entry) bool {
	if target == nil || source == nil {
		return false
	}
	return target.GetAttributes().GetMtime() > source.GetAttributes().GetMtime()
}

func cloneEntry(entry *filer_pb.Entry) *filer_pb.Entry {
	if entry == nil {
		return nil
	}
	out := *entry
	out.Chunks = append([]*filer_pb.FileChunk(nil), entry.GetChunks()...)
	out.Content = append([]byte(nil), entry.GetContent()...)
	if len(entry.GetExtended()) > 0 {
		out.Extended = make(map[string][]byte, len(entry.GetExtended()))
		for k, v := range entry.GetExtended() {
			out.Extended[k] = append([]byte(nil), v...)
		}
	}
	return &out
}

func cleanDir(dir string) string {
	if dir == "" {
		return "/"
	}
	cleaned := path.Clean("/" + dir)
	if cleaned == "." {
		return "/"
	}
	return cleaned
}
