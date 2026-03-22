package server

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"log/slog"

	"GoBlob/goblob/cache"
	"GoBlob/goblob/config"
	"GoBlob/goblob/core/types"
	"GoBlob/goblob/obs"
	"GoBlob/goblob/pb"
	"GoBlob/goblob/pb/master_pb"
	"GoBlob/goblob/pb/volume_server_pb"
	"GoBlob/goblob/replication"
	"GoBlob/goblob/security"
	"GoBlob/goblob/storage"
	"GoBlob/goblob/storage/needle"
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
	blobCache         cache.Cache
	stopChan          chan struct{}
	ctx               context.Context
	cancel            context.CancelFunc
	logger            *slog.Logger
}

// NewVolumeServer creates and initializes a new VolumeServer.
func NewVolumeServer(adminMux, publicMux *http.ServeMux, grpcServer *grpc.Server, opt *VolumeServerOption) (*VolumeServer, error) {
	ctx, cancel := context.WithCancel(context.Background())

	logger := obs.New("volume").With("server", "volume")

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

	// Create blob cache
	var blobCache cache.Cache
	if opt.CacheMaxEntries > 0 {
		blobCache = cache.NewLRUCache(int64(opt.CacheMaxEntries))
	}

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
		blobCache:         blobCache,
		stopChan:          make(chan struct{}),
		ctx:               ctx,
		cancel:            cancel,
		logger:            logger,
	}

	// Register HTTP routes
	vs.registerRoutes(adminMux, publicMux)
	if grpcServer != nil {
		volume_server_pb.RegisterVolumeServerServer(grpcServer, NewVolumeGRPCServer(vs))
	}

	return vs, nil
}

// registerRoutes registers HTTP handlers for the volume server.
func (vs *VolumeServer) registerRoutes(adminMux, publicMux *http.ServeMux) {
	// Admin routes (internal)
	adminMux.HandleFunc("PUT /{fid}", vs.handleWrite)
	adminMux.HandleFunc("GET /{fid}", vs.handleRead)
	adminMux.HandleFunc("DELETE /{fid}", vs.handleDelete)
	adminMux.HandleFunc("GET /status", vs.handleStatus)
	adminMux.HandleFunc("GET /health", vs.handleHealth)
	adminMux.HandleFunc("GET /ready", vs.handleReady)

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

// ReloadSecurityConfig applies updated security settings to the live volume server.
func (vs *VolumeServer) ReloadSecurityConfig(secCfg *config.SecurityConfig) {
	if vs == nil || vs.guard == nil || secCfg == nil {
		return
	}
	vs.guard.SetWhiteList(secCfg.Guard.WhiteList)
	vs.guard.SetSigningKey(secCfg.JWT.Signing.Key)
}

// LoadNewVolumes re-scans configured disk locations and mounts newly discovered volumes.
func (vs *VolumeServer) LoadNewVolumes() error {
	if vs == nil || vs.store == nil {
		return fmt.Errorf("store not initialized")
	}
	return vs.store.ReloadExistingVolumes("", types.CurrentNeedleVersion)
}

// handleWrite handles PUT /{fid} requests.
func (vs *VolumeServer) handleWrite(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Upload throttle
	if vs.option.ConcurrentUploadLimitMB > 0 {
		limit := vs.option.ConcurrentUploadLimitMB * 1024 * 1024
		incoming := r.ContentLength
		if incoming < 0 {
			incoming = 0
		}
		vs.uploadLimitCond.L.Lock()
		for atomic.LoadInt64(&vs.inFlightUpload)+incoming > limit {
			vs.uploadLimitCond.Wait()
		}
		atomic.AddInt64(&vs.inFlightUpload, incoming)
		vs.uploadLimitCond.L.Unlock()

		defer func() {
			atomic.AddInt64(&vs.inFlightUpload, -incoming)
			vs.uploadLimitCond.Signal()
		}()
	}

	// Check authorization
	if !vs.guard.Allowed(r, true) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Parse FileId from URL
	path := r.URL.Path[1:] // remove leading "/"
	fid, err := types.ParseFileId(path)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid file id"})
		return
	}

	if vs.store == nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "store not initialized"})
		return
	}

	n, contentMD5, err := buildNeedleFromRequest(r, fid, vs.option.FileSizeLimitMB)
	if err != nil {
		if err == errPayloadTooLarge {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	_, _, err = vs.store.WriteVolumeNeedle(fid.VolumeId, n)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	obs.VolumeServerNeedleWriteBytes.WithLabelValues(fmt.Sprintf("%d", fid.VolumeId)).Add(float64(len(n.Data)))
	obs.VolumeServerNeedleWriteLatency.Observe(time.Since(start).Seconds())

	isReplication := r.Header.Get("X-Replication") == "true"
	if !isReplication && vs.replicator != nil {
		replicaAddrs := vs.replicaLocations.Get(fid.VolumeId)
		filtered := make([]types.ServerAddress, 0, len(replicaAddrs))
		selfAddr := types.ServerAddress(fmt.Sprintf("%s:%d", vs.option.Host, vs.option.Port))
		for _, addr := range replicaAddrs {
			if addr == selfAddr || string(addr) == vs.option.PublicUrl {
				continue
			}
			filtered = append(filtered, addr)
		}

		needleBuf := needleBufPool.Get().(*bytes.Buffer)
		needleBuf.Reset()
		if _, err := n.WriteTo(needleBuf, types.CurrentNeedleVersion); err == nil {
			repErr := vs.replicator.ReplicatedWrite(r.Context(), replication.ReplicateRequest{
				VolumeId:    fid.VolumeId,
				NeedleId:    fid.NeedleId,
				NeedleBytes: needleBuf.Bytes(),
				JWTToken:    parseBearerToken(r.Header.Get("Authorization")),
			}, filtered)
			needleBufPool.Put(needleBuf)
			if repErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": repErr.Error()})
				return
			}
		} else {
			needleBufPool.Put(needleBuf)
		}
	}

	w.Header().Set("ETag", contentMD5)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"size": len(n.Data),
		"eTag": contentMD5,
	})
}

// handleRead handles GET /{fid} requests.
func (vs *VolumeServer) handleRead(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

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
	fid, err := types.ParseFileId(path)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid file id"})
		return
	}

	if vs.store == nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	cacheKey := r.URL.Path[1:]

	// Cache-aside: serve from memory if available.
	if vs.blobCache != nil {
		if cached, err := vs.blobCache.Get(r.Context(), cacheKey); err == nil {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(cached)))
			w.Header().Set("ETag", fmt.Sprintf("%x", md5.Sum(cached)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached)
			obs.VolumeServerNeedleReadBytes.WithLabelValues(fmt.Sprintf("%d", fid.VolumeId)).Add(float64(len(cached)))
			obs.VolumeServerNeedleReadLatency.Observe(time.Since(start).Seconds())
			return
		}
	}

	n, err := vs.store.ReadVolumeNeedle(fid.VolumeId, fid)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Populate cache on read miss.
	if vs.blobCache != nil {
		_ = vs.blobCache.Put(r.Context(), cacheKey, n.Data)
	}

	mimeType := n.GetMime()
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(n.Data)))
	w.Header().Set("ETag", fmt.Sprintf("%x", md5.Sum(n.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(n.Data)
	obs.VolumeServerNeedleReadBytes.WithLabelValues(fmt.Sprintf("%d", fid.VolumeId)).Add(float64(len(n.Data)))
	obs.VolumeServerNeedleReadLatency.Observe(time.Since(start).Seconds())
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

	if vs.store == nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "store not initialized"})
		return
	}

	fid, _ := types.ParseFileId(path)
	if err := vs.store.DeleteVolumeNeedle(fid.VolumeId, fid.NeedleId); err != nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if vs.blobCache != nil {
		_ = vs.blobCache.Invalidate(r.Context(), path)
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

// handleStatus handles GET /status requests.
func (vs *VolumeServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	volumeCount := 0
	if vs.store != nil {
		for _, dl := range vs.store.Locations {
			volumeCount += dl.VolumeCount()
			obs.VolumeServerDiskFreeBytes.WithLabelValues(dl.Directory()).Set(float64(dl.FreeSpace()))
		}
	}
	status := map[string]interface{}{
		"version": "1.0",
		"volumes": volumeCount,
		"limit":   vs.option.FileSizeLimitMB,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (vs *VolumeServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (vs *VolumeServer) handleReady(w http.ResponseWriter, r *http.Request) {
	if vs == nil || vs.store == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("NOT READY"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("READY"))
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
	if len(vs.option.Masters) == 0 {
		return
	}

	masterAddr := vs.currentMaster
	if masterAddr == "" {
		masterAddr = types.ServerAddress(vs.option.Masters[0]).ToGrpcAddress()
	}

	vs.isHeartbeating.Store(true)
	defer vs.isHeartbeating.Store(false)

	dialOpt := grpc.WithTransportCredentials(insecure.NewCredentials())
	_ = pb.WithMasterServerClient(string(masterAddr), dialOpt, func(client master_pb.MasterServiceClient) error {
		stream, err := client.SendHeartbeat(vs.ctx)
		if err != nil {
			return err
		}

		hb := vs.BuildHeartbeat()
		if err := stream.Send(hb); err != nil {
			return err
		}

		resp, err := stream.Recv()
		if err != nil {
			return err
		}
		if resp.Leader != "" {
			vs.masterMu.Lock()
			vs.currentMaster = types.ServerAddress(resp.Leader).ToGrpcAddress()
			vs.masterMu.Unlock()
		}
		return nil
	})
}

// BuildHeartbeat creates a heartbeat message for the master server.
func (vs *VolumeServer) BuildHeartbeat() *master_pb.Heartbeat {
	volumes := make([]*master_pb.VolumeInformationMessage, 0)
	maxCounts := make([]*master_pb.MaxVolumeCounts, 0, len(vs.option.Directories))
	for _, d := range vs.option.Directories {
		maxCounts = append(maxCounts, &master_pb.MaxVolumeCounts{
			DiskType: d.DiskType,
			MaxCount: uint32(d.MaxVolumeCount),
		})
	}
	if vs.store != nil {
		for _, dl := range vs.store.Locations {
			for _, vid := range dl.GetVolumes() {
				v, ok := vs.store.GetVolume(vid)
				if !ok {
					continue
				}
				volumes = append(volumes, &master_pb.VolumeInformationMessage{
					Id:               uint32(vid),
					Size:             uint64(v.UsedSize()),
					Collection:       v.Collection(),
					FileCount:        uint64(v.NeedleCount()),
					DeleteCount:      uint64(v.DeletedCount()),
					DeletedByteCount: v.DeletedSize(),
					ReadOnly:         v.ReadOnly,
					DiskType:         string(types.DefaultDiskType),
				})
			}
		}
	}

	return &master_pb.Heartbeat{
		Ip:              vs.option.Host,
		Port:            uint32(vs.option.Port),
		PublicUrl:       vs.option.PublicUrl,
		GrpcPort:        uint32(vs.option.GRPCPort),
		DataCenter:      vs.option.DataCenter,
		Rack:            vs.option.Rack,
		Volumes:         volumes,
		MaxVolumeCounts: maxCounts,
		HasNoVolumes:    len(volumes) == 0,
	}
}

var errPayloadTooLarge = fmt.Errorf("payload too large")

// needleBufPool recycles the bytes.Buffer used to serialise needles for
// replication, avoiding a per-write heap allocation on the hot write path.
var needleBufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

func buildNeedleFromRequest(r *http.Request, fid types.FileId, maxFileSizeMB int64) (*needle.Needle, string, error) {
	limit := maxFileSizeMB * 1024 * 1024
	if limit <= 0 {
		limit = 4 * 1024 * 1024
	}

	data, filename, mimeType, err := readUploadPayload(r, limit)
	if err != nil {
		return nil, "", err
	}

	sum := md5.Sum(data)
	n := &needle.Needle{
		Id:       fid.NeedleId,
		Cookie:   fid.Cookie,
		Data:     data,
		DataSize: uint32(len(data)),
	}
	if filename != "" {
		n.SetName(filename)
	}
	if mimeType != "" {
		n.SetMime(mimeType)
	}
	n.SetLastModified(uint64(time.Now().Unix()))
	n.SetAppendAtNs()
	return n, fmt.Sprintf("%x", sum), nil
}

func readUploadPayload(r *http.Request, limit int64) ([]byte, string, string, error) {
	contentType := r.Header.Get("Content-Type")
	if mediaType, params, err := mime.ParseMediaType(contentType); err == nil && mediaType == "multipart/form-data" {
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, "", "", err
			}
			if part.FileName() == "" && part.FormName() != "file" {
				_ = part.Close()
				continue
			}
			data, readErr := io.ReadAll(io.LimitReader(part, limit+1))
			_ = part.Close()
			if readErr != nil {
				return nil, "", "", readErr
			}
			if int64(len(data)) > limit {
				return nil, "", "", errPayloadTooLarge
			}
			mimeType := part.Header.Get("Content-Type")
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			return data, part.FileName(), mimeType, nil
		}
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, "", "", err
	}
	if int64(len(data)) > limit {
		return nil, "", "", errPayloadTooLarge
	}
	mimeType := contentType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return data, "", mimeType, nil
}

func parseBearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) > len(prefix) && header[:len(prefix)] == prefix {
		return header[len(prefix):]
	}
	return ""
}
