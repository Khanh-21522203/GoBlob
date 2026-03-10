package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"log/slog"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/replication"
	"GoBlob/goblob/security"
	"GoBlob/goblob/storage"
)

// VolumeServer is the data plane server storing blobs and serving read/write requests.
type VolumeServer struct {
	option            *VolumeServerOption
	store             *storage.Store
	guard             *security.Guard
	grpcServer        *grpc.Server
	replicator        replication.Replicator
	replicaLocations  *replication.ReplicaLocations
	currentMaster     types.ServerAddress
	masterMu          sync.RWMutex
	inFlightUpload    int64
	inFlightDownload  int64
	uploadLimitCond   *sync.Cond
	downloadLimitCond *sync.Cond
	isHeartbeating    atomic.Bool
	stopChan          chan struct{}
	ctx               context.Context
	cancel            context.CancelFunc
	logger            *slog.Logger
}

// NewVolumeServer creates and initializes a new VolumeServer.
func NewVolumeServer(adminMux, publicMux *http.ServeMux, grpcServer *grpc.Server, opt *VolumeServerOption) (*VolumeServer, error) {
	ctx, cancel := context.WithCancel(context.Background())

	logger := slog.Default().With("server", "volume")

	// Create storage store
	var needleMapKind storage.NeedleMapKind
	switch opt.IndexType {
	case "mem":
		needleMapKind = storage.NeedleMapInMemory
	default:
		needleMapKind = storage.NeedleMapInMemory
	}

	storeDirs := make([]storage.DiskDirectoryConfig, len(opt.Directories))
	for i, d := range opt.Directories {
		storeDirs[i] = storage.DiskDirectoryConfig{
			Directory:      d.Path,
			MaxVolumeCount: int(d.MaxVolumeCount),
			DiskType:       types.DiskType(d.DiskType),
		}
	}

	store, err := storage.NewStore(opt.Host, opt.Port, storeDirs, needleMapKind)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	// Create guard (empty whitelist = allow all)
	guard := security.NewGuard("", "", "")

	// Create replicator
	replicator := replication.NewHTTPReplicator()
	replicaLocations := replication.NewReplicaLocations()

	// Create limit conds
	uploadMu := &sync.Mutex{}
	downloadMu := &sync.Mutex{}

	vs := &VolumeServer{
		option:            opt,
		store:             store,
		guard:             guard,
		grpcServer:        grpcServer,
		replicator:        replicator,
		replicaLocations:  replicaLocations,
		inFlightUpload:    0,
		inFlightDownload:  0,
		uploadLimitCond:   sync.NewCond(uploadMu),
		downloadLimitCond: sync.NewCond(downloadMu),
		stopChan:          make(chan struct{}),
		ctx:               ctx,
		cancel:            cancel,
		logger:            logger,
	}

	// Register HTTP routes
	vs.registerRoutes(adminMux, publicMux)

	return vs, nil
}

// registerRoutes registers HTTP handlers for the volume server.
func (vs *VolumeServer) registerRoutes(adminMux, publicMux *http.ServeMux) {
	// Admin routes (internal)
	adminMux.HandleFunc("PUT /{fid}", vs.handleWrite)
	adminMux.HandleFunc("GET /{fid}", vs.handleRead)
	adminMux.HandleFunc("DELETE /{fid}", vs.handleDelete)
	adminMux.HandleFunc("GET /status", vs.handleStatus)

	// Public routes (external) - no status endpoint on public
	publicMux.HandleFunc("GET /{fid}", vs.handleRead)
}

// Start starts the volume server.
func (vs *VolumeServer) Start() {
	// Start heartbeat goroutine
	go vs.heartbeatLoop()
}

// Shutdown gracefully shuts down the volume server.
func (vs *VolumeServer) Shutdown() {
	close(vs.stopChan)

	// Wait a bit for master to reroute traffic
	time.Sleep(time.Duration(vs.option.PreStopSeconds) * time.Second)

	// Stop gRPC server
	if vs.grpcServer != nil {
		vs.grpcServer.GracefulStop()
	}

	// Close store
	if vs.store != nil {
		vs.store.Close()
	}

	vs.cancel()
}

// handleWrite handles PUT /{fid} requests.
func (vs *VolumeServer) handleWrite(w http.ResponseWriter, r *http.Request) {
	// Upload throttle
	if vs.option.ConcurrentUploadLimitMB > 0 {
		limit := vs.option.ConcurrentUploadLimitMB * 1024 * 1024
		vs.uploadLimitCond.L.Lock()
		for atomic.LoadInt64(&vs.inFlightUpload) > limit {
			vs.uploadLimitCond.Wait()
		}
		atomic.AddInt64(&vs.inFlightUpload, r.ContentLength)
		vs.uploadLimitCond.L.Unlock()

		defer func() {
			atomic.AddInt64(&vs.inFlightUpload, -r.ContentLength)
			vs.uploadLimitCond.Signal()
		}()
	}

	// Check authorization
	if !vs.guard.Allowed(r, false) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Parse FileId from URL
	path := r.URL.Path[1:] // remove leading "/"
	_, err := types.ParseFileId(path)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid file id"})
		return
	}

	// Check if this is a replication request
	_ = r.Header.Get("X-Replication") == "true"

	// TODO: Create needle from request
	// For now, return 501 Not Implemented
	w.WriteHeader(http.StatusNotImplemented)
	json.NewEncoder(w).Encode(map[string]string{"error": "not implemented yet"})
}

// handleRead handles GET /{fid} requests.
func (vs *VolumeServer) handleRead(w http.ResponseWriter, r *http.Request) {
	// Download throttle
	if vs.option.ConcurrentDownloadLimitMB > 0 {
		limit := vs.option.ConcurrentDownloadLimitMB * 1024 * 1024
		vs.downloadLimitCond.L.Lock()
		for atomic.LoadInt64(&vs.inFlightDownload) > limit {
			vs.downloadLimitCond.Wait()
		}
		atomic.AddInt64(&vs.inFlightDownload, 1)
		vs.downloadLimitCond.L.Unlock()

		defer func() {
			atomic.AddInt64(&vs.inFlightDownload, -1)
			vs.downloadLimitCond.Signal()
		}()
	}

	// Check authorization
	if !vs.guard.Allowed(r, false) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Parse FileId from URL
	path := r.URL.Path[1:] // remove leading "/"
	_, err := types.ParseFileId(path)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid file id"})
		return
	}

	// Find volume
	_, ok := vs.store.GetVolume(0) // dummy check
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// TODO: Read needle and serve content
	// For now, return 501 Not Implemented
	w.WriteHeader(http.StatusNotImplemented)
}

// handleDelete handles DELETE /{fid} requests.
func (vs *VolumeServer) handleDelete(w http.ResponseWriter, r *http.Request) {
	// Check authorization
	if !vs.guard.Allowed(r, true) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Parse FileId from URL
	path := r.URL.Path[1:] // remove leading "/"
	_, err := types.ParseFileId(path)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid file id"})
		return
	}

	// Delete needle
	// TODO: implement delete
	_ = err // use the variable

	w.WriteHeader(http.StatusNotImplemented)
	json.NewEncoder(w).Encode(map[string]string{"error": "not implemented yet"})
}

// handleStatus handles GET /status requests.
func (vs *VolumeServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"version": "1.0",
		"volumes": 0,
		"limit":   vs.option.FileSizeLimitMB,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// heartbeatLoop sends heartbeats to the master server.
func (vs *VolumeServer) heartbeatLoop() {
	ticker := time.NewTicker(vs.option.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-vs.ctx.Done():
			return
		case <-ticker.C:
			vs.sendHeartbeat()
		case <-vs.stopChan:
			return
		}
	}
}

// sendHeartbeat sends a heartbeat to the current master.
func (vs *VolumeServer) sendHeartbeat() {
	// TODO: Implement gRPC heartbeat to master
	// For Phase 4, this is a placeholder
	_ = vs.option.Masters // use the variable
}

// BuildHeartbeat creates a heartbeat message for the master server.
// For Phase 4, this is a placeholder.
func (vs *VolumeServer) BuildHeartbeat() map[string]interface{} {
	return map[string]interface{}{
		"ip":        vs.option.Host,
		"port":      vs.option.Port,
		"publicUrl": vs.option.PublicUrl,
		"grpcPort":  vs.option.GRPCPort,
	}
}
