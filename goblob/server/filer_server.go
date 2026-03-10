package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"log/slog"

	"GoBlob/goblob/config"
	"GoBlob/goblob/filer"
	"GoBlob/goblob/log_buffer"
	"GoBlob/goblob/obs"
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

	// Create log buffer
	logBuf := log_buffer.NewLogBuffer(
		3,           // maxSegmentCount
		2*1024*1024, // maxSegmentBytes
		time.Duration(opt.LogFlushIntervalSeconds)*time.Second, // flushInterval
		nil, // notifyFn (set later)
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
	// Start log buffer flush daemon
	fs.logBuffer.StartFlushDaemon(fs.ctx)

	// Start lightweight metadata aggregation loop for listener fan-out bookkeeping.
	go fs.metadataAggregatorLoop()
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
	path := normalizeAbsolutePath(r.URL.Path)

	// TODO: wire storage/dedup.Deduplicator here once chunked volume storage is
	// implemented. The dedup package is ready; it requires a volume fid per chunk
	// to store hash→fid mappings. Inline content (<64KB) does not benefit from it.

	// For Phase 4, only support inline storage (<64KB)
	if r.ContentLength > 64*1024 {
		w.WriteHeader(http.StatusNotImplemented)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "chunked files not supported yet"})
		return
	}

	// Read data
	data, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer r.Body.Close()

	// Create entry
	entry := &filer.Entry{
		FullPath: filer.FullPath(path),
		Attr: filer.Attr{
			Mode:     0644,
			Mtime:    time.Now(),
			FileSize: uint64(len(data)),
			Mime:     r.Header.Get("Content-Type"),
		},
		Content: data,
	}

	// Create entry in store
	err = fs.filer.CreateEntry(fs.ctx, entry)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Append to log buffer
	fs.logBuffer.AppendEntry([]byte(fmt.Sprintf("create:%s", path)))

	// Notify listeners
	fs.listenersMu.Lock()
	fs.listenersCond.Broadcast()
	fs.listenersMu.Unlock()

	// Return success
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"name": path,
		"size": len(data),
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
	path := normalizeAbsolutePath(r.URL.Path)

	entry, err := fs.filer.FindEntry(fs.ctx, filer.FullPath(path))
	if err != nil {
		if err == filer.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	// Check for inline content
	if len(entry.Content) > 0 {
		// Serve inline content
		if entry.Attr.Mime != "" {
			w.Header().Set("Content-Type", entry.Attr.Mime)
		}
		w.Header().Set("Content-Length", strconv.FormatInt(int64(len(entry.Content)), 10))
		w.Header().Set("Last-Modified", entry.Attr.Mtime.UTC().Format(http.TimeFormat))
		_, _ = w.Write(entry.Content)
		return
	}

	// Chunked files not supported yet (Phase 5)
	w.WriteHeader(http.StatusNotImplemented)
	json.NewEncoder(w).Encode(map[string]string{"error": "chunked files not supported yet"})
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
	path := normalizeAbsolutePath(r.URL.Path)

	// Delete entry
	err := fs.filer.DeleteEntry(fs.ctx, filer.FullPath(path))
	if err != nil {
		if err == filer.ErrNotFound {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	// Append to log buffer
	fs.logBuffer.AppendEntry([]byte(fmt.Sprintf("delete:%s", path)))

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

func (fs *FilerServer) metadataAggregatorLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	sinceNs := int64(0)
	for {
		select {
		case <-fs.ctx.Done():
			return
		case <-ticker.C:
			result := fs.logBuffer.ReadFromBuffer(sinceNs)
			if len(result.Events) == 0 {
				continue
			}
			if result.NextTs > sinceNs {
				sinceNs = result.NextTs
			}

			fs.listenersMu.Lock()
			listenerCount := len(fs.knownListeners)
			fs.listenersMu.Unlock()
			if listenerCount > 0 {
				fs.logger.Debug("metadata events aggregated", "events", len(result.Events), "listeners", listenerCount)
			}
		}
	}
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
