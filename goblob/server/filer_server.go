package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"

	"google.golang.org/grpc"

	"GoBlob/goblob/config"
	"GoBlob/goblob/core/types"
	"GoBlob/goblob/filer"
	"GoBlob/goblob/log_buffer"
	"GoBlob/goblob/obs"
	"GoBlob/goblob/operation"
	"GoBlob/goblob/pb/filer_pb"
	"GoBlob/goblob/pb/iam_pb"
	"GoBlob/goblob/security"
)

// FilerServer is the metadata server for the GoBlob filesystem.
type FilerServer struct {
	option            *FilerOption
	filer             *filer.Filer
	filerGuard        *security.Guard
	volumeGuard       *security.Guard
	grpcServer        *grpc.Server
	knownListeners    map[int32]bool
	listenersMu       sync.Mutex
	listenersCond     *sync.Cond
	inFlightDataSize  int64
	inFlightLimitCond *sync.Cond
	logBuffer         *log_buffer.LogBuffer
	dlm               *filer.DistributedLockManager
	ctx               context.Context
	cancel            context.CancelFunc
	logger            *slog.Logger
}

// NewFilerServer creates and initializes a new FilerServer.
func NewFilerServer(defaultMux, readonlyMux *http.ServeMux, grpcServer *grpc.Server, opt *FilerOption) (*FilerServer, error) {
	ctx, cancel := context.WithCancel(context.Background())

	logger := obs.New("filer").With("server", "filer")

	// Create guards
	filerGuard := security.NewGuard("", "", "")
	volumeGuard := security.NewGuard("", "", "")

	// notifyFn is set after filerServer is constructed so it can reference listenersCond.
	// Placeholder until wired below.
	var notifyFn func()

	// Create log buffer
	logBuf := log_buffer.NewLogBuffer(
		3,           // maxSegmentCount
		2*1024*1024, // maxSegmentBytes
		time.Duration(opt.LogFlushIntervalSeconds)*time.Second, // flushInterval
		func() {
			if notifyFn != nil {
				notifyFn()
			}
		},
		nil, // flushFn (set later)
		logger,
	)

	// Create lock manager
	dlm := filer.NewDistributedLockManager(nil, logger) // store set later

	filerServer := &FilerServer{
		option:           opt,
		filerGuard:       filerGuard,
		volumeGuard:      volumeGuard,
		grpcServer:       grpcServer,
		knownListeners:   make(map[int32]bool),
		inFlightDataSize: 0,
		logBuffer:        logBuf,
		dlm:              dlm,
		ctx:              ctx,
		cancel:           cancel,
		logger:           logger,
	}
	filerServer.listenersCond = sync.NewCond(&filerServer.listenersMu)
	filerServer.inFlightLimitCond = sync.NewCond(&sync.Mutex{})

	// Wire notifyFn: broadcast listenersCond whenever a new event is appended to the
	// log buffer so SubscribeMetadata subscribers wake up promptly instead of polling.
	notifyFn = func() {
		filerServer.listenersCond.Broadcast()
	}

	// Register HTTP routes
	filerServer.registerRoutes(defaultMux, readonlyMux)
	if grpcServer != nil {
		filer_pb.RegisterFilerServiceServer(grpcServer, NewFilerGRPCServer(filerServer))
		iam_pb.RegisterIAMServiceServer(grpcServer, NewIAMGRPCServer(filerServer))
	}

	return filerServer, nil
}

// registerRoutes registers HTTP handlers for the filer server.
func (fs *FilerServer) registerRoutes(defaultMux, readonlyMux *http.ServeMux) {
	// Read/write routes
	defaultMux.HandleFunc("POST /{path...}", fs.instrumentHTTP(fs.handleFileUpload))
	defaultMux.HandleFunc("GET /{path...}", fs.instrumentHTTP(fs.handleFileDownload))
	defaultMux.HandleFunc("DELETE /{path...}", fs.instrumentHTTP(fs.handleDeleteEntry))
	defaultMux.HandleFunc("GET /healthz", fs.instrumentHTTP(fs.handleHealthz))
	defaultMux.HandleFunc("GET /health", fs.instrumentHTTP(fs.handleHealthz))
	defaultMux.HandleFunc("GET /ready", fs.instrumentHTTP(fs.handleReady))

	// Read-only routes
	readonlyMux.HandleFunc("GET /{path...}", fs.instrumentHTTP(fs.handleFileDownload))
}

// SetStore sets the filer store.
func (fs *FilerServer) SetStore(store filer.FilerStore) {
	if fs.filer == nil {
		fs.filer = filer.NewFiler(store, fs.option.Masters, fs.logger)
	} else {
		fs.filer.SetStore(store)
	}
	fs.dlm = filer.NewDistributedLockManager(store, fs.logger)
	fs.filer.Dlm = fs.dlm
}

// Start starts the filer server.
func (fs *FilerServer) Start() {
	// Start log buffer flush daemon; notifyFn will wake gRPC subscribers on each append.
	fs.logBuffer.StartFlushDaemon(fs.ctx)
}

// Shutdown gracefully shuts down the filer server.
func (fs *FilerServer) Shutdown() {
	fs.cancel()

	// Stop log buffer
	fs.logBuffer.Stop()

	// Stop gRPC server
	if fs.grpcServer != nil {
		fs.grpcServer.GracefulStop()
	}
}

// ReloadConfig applies updated runtime configuration that can be safely reloaded in-place.
func (fs *FilerServer) ReloadConfig(secCfg *config.SecurityConfig) {
	if fs == nil || secCfg == nil {
		return
	}
	if fs.filerGuard != nil {
		fs.filerGuard.SetWhiteList(secCfg.Guard.WhiteList)
		fs.filerGuard.SetSigningKey(secCfg.JWT.Signing.Key)
		fs.filerGuard.SetFilerKey(secCfg.JWT.FilerSigning.Key)
	}
	if fs.volumeGuard != nil {
		fs.volumeGuard.SetWhiteList(secCfg.Guard.WhiteList)
		fs.volumeGuard.SetSigningKey(secCfg.JWT.Signing.Key)
		fs.volumeGuard.SetFilerKey(secCfg.JWT.FilerSigning.Key)
	}
}

// handleFileUpload handles POST /{path} requests.
func (fs *FilerServer) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if !fs.requireFiler(w) {
		return
	}

	contentLength := r.ContentLength
	if contentLength < 0 {
		contentLength = 0
	}

	// Throttle in-flight data
	if fs.option.ConcurrentUploadLimitMB > 0 {
		limit := fs.option.ConcurrentUploadLimitMB * 1024 * 1024
		fs.inFlightLimitCond.L.Lock()
		for atomic.LoadInt64(&fs.inFlightDataSize) > limit {
			fs.inFlightLimitCond.Wait()
		}
		atomic.AddInt64(&fs.inFlightDataSize, contentLength)
		fs.inFlightLimitCond.L.Unlock()

		defer func() {
			atomic.AddInt64(&fs.inFlightDataSize, -contentLength)
			fs.inFlightLimitCond.L.Lock()
			fs.inFlightLimitCond.Signal()
			fs.inFlightLimitCond.L.Unlock()
		}()
	}

	// Check authorization
	if !fs.filerGuard.FilerAllowed(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Get path from URL
	filePath := normalizeAbsolutePath(r.URL.Path)
	defer r.Body.Close()

	const inlineLimit = 64 * 1024

	entry := &filer.Entry{
		FullPath: filer.FullPath(filePath),
		Attr: filer.Attr{
			Mode:  0644,
			Mtime: time.Now(),
			Mime:  r.Header.Get("Content-Type"),
		},
	}

	if r.ContentLength > inlineLimit || r.ContentLength < 0 {
		// Large file: split into chunks and store on volume servers.
		if len(fs.filer.MasterAddresses) == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "no master addresses configured"})
			return
		}
		masterAddr := fs.filer.MasterAddresses[0]
		opt := &operation.UploadOption{Master: masterAddr}
		results, err := operation.ChunkUpload(fs.ctx, masterAddr, r.Body, r.ContentLength, 8*1024*1024, opt)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		chunks := make([]*filer.FileChunk, 0, len(results))
		var totalSize int64
		for _, cr := range results {
			chunks = append(chunks, &filer.FileChunk{
				FileId:       cr.FileId,
				Offset:       cr.Offset,
				Size:         cr.Size,
				ModifiedTsNs: cr.ModifiedTsNs,
				ETag:         cr.ETag,
			})
			totalSize += cr.Size
		}
		entry.Chunks = chunks
		entry.Attr.FileSize = uint64(totalSize)
	} else {
		// Small file: store inline.
		data, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		entry.Content = data
		entry.Attr.FileSize = uint64(len(data))
	}

	// Create entry in store
	err := fs.filer.CreateEntry(fs.ctx, entry)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Append to log buffer (notifyFn broadcasts listenersCond automatically).
	fs.logBuffer.AppendEntry([]byte(fmt.Sprintf("create:%s", filePath)))

	// Return success
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"name": filePath,
		"size": entry.Attr.FileSize,
	})
}

// handleFileDownload handles GET /{path} requests.
func (fs *FilerServer) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	if !fs.requireFiler(w) {
		return
	}

	// Check authorization
	if !fs.filerGuard.FilerAllowed(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Get path from URL
	filePath := normalizeAbsolutePath(r.URL.Path)

	entry, err := fs.filer.FindEntry(fs.ctx, filer.FullPath(filePath))
	if err != nil {
		if err == filer.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	if entry.Attr.Mime != "" {
		w.Header().Set("Content-Type", entry.Attr.Mime)
	}
	w.Header().Set("Last-Modified", entry.Attr.Mtime.UTC().Format(http.TimeFormat))

	// Inline content (small files).
	if len(entry.Content) > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(int64(len(entry.Content)), 10))
		_, _ = w.Write(entry.Content)
		return
	}

	// Chunked content: reassemble from volume servers in chunk order.
	if len(entry.Chunks) > 0 {
		if len(fs.filer.MasterAddresses) == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "no master addresses configured"})
			return
		}
		masterAddr := fs.filer.MasterAddresses[0]

		chunks := make([]*filer.FileChunk, len(entry.Chunks))
		copy(chunks, entry.Chunks)
		sort.Slice(chunks, func(i, j int) bool { return chunks[i].Offset < chunks[j].Offset })

		w.Header().Set("Content-Length", strconv.FormatUint(entry.Attr.FileSize, 10))
		client := &http.Client{Timeout: 30 * time.Second}

		for _, chunk := range chunks {
			// Parse volumeId from FileId ("volumeId,needleId").
			parts := strings.SplitN(chunk.FileId, ",", 2)
			if len(parts) != 2 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			vidU, err := strconv.ParseUint(parts[0], 10, 32)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			locs, err := operation.LookupVolumeId(fs.ctx, masterAddr, types.VolumeId(vidU))
			if err != nil || len(locs) == 0 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			chunkURL := "http://" + locs[0].Url + "/" + chunk.FileId
			req, err := http.NewRequestWithContext(fs.ctx, http.MethodGet, chunkURL, nil)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = io.Copy(w, resp.Body)
			_ = resp.Body.Close()
		}
		return
	}

	// Empty file.
	w.Header().Set("Content-Length", "0")
}

// handleDeleteEntry handles DELETE /{path} requests.
func (fs *FilerServer) handleDeleteEntry(w http.ResponseWriter, r *http.Request) {
	if !fs.requireFiler(w) {
		return
	}

	// Check authorization
	if !fs.filerGuard.FilerAllowed(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Get path from URL
	filePath := normalizeAbsolutePath(r.URL.Path)

	// Delete entry
	err := fs.filer.DeleteEntry(fs.ctx, filer.FullPath(filePath))
	if err != nil {
		if err == filer.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	// Append to log buffer
	fs.logBuffer.AppendEntry([]byte(fmt.Sprintf("delete:%s", filePath)))

	w.WriteHeader(http.StatusNoContent)
}

// handleHealthz handles GET /healthz requests.
func (fs *FilerServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (fs *FilerServer) handleReady(w http.ResponseWriter, r *http.Request) {
	if fs == nil || fs.filer == nil || fs.filer.Store == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("NOT READY"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("READY"))
}

func (fs *FilerServer) requireFiler(w http.ResponseWriter) bool {
	if fs.filer != nil && fs.filer.Store != nil {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "filer store not initialized"})
	return false
}

func normalizeAbsolutePath(raw string) string {
	if raw == "" {
		return "/"
	}
	return path.Clean("/" + raw)
}

type metricsStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricsStatusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (fs *FilerServer) instrumentHTTP(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		mw := &metricsStatusWriter{ResponseWriter: w, status: http.StatusOK}
		next(mw, r)
		statusCode := mw.status
		obs.FilerRequestsTotal.WithLabelValues(r.Method, strconv.Itoa(statusCode)).Inc()
		obs.FilerStoreLatency.WithLabelValues(strings.ToLower(r.Method)).Observe(time.Since(start).Seconds())
	}
}
