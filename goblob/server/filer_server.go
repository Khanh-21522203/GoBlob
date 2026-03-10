package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"log/slog"

	"GoBlob/goblob/filer"
	"GoBlob/goblob/log_buffer"
	"GoBlob/goblob/security"
)

// FilerServer is the metadata server for the GoBlob filesystem.
type FilerServer struct {
	option             *FilerOption
	filer              *filer.Filer
	filerGuard         *security.Guard
	volumeGuard        *security.Guard
	grpcServer         *grpc.Server
	knownListeners     map[int32]bool
	listenersMu        sync.Mutex
	listenersCond      *sync.Cond
	inFlightDataSize   int64
	inFlightLimitCond  *sync.Cond
	logBuffer          *log_buffer.LogBuffer
	dlm                *filer.DistributedLockManager
	ctx                context.Context
	cancel             context.CancelFunc
	logger             *slog.Logger
}

// NewFilerServer creates and initializes a new FilerServer.
func NewFilerServer(defaultMux, readonlyMux *http.ServeMux, grpcServer *grpc.Server, opt *FilerOption) (*FilerServer, error) {
	ctx, cancel := context.WithCancel(context.Background())

	logger := slog.Default().With("server", "filer")

	// Create guards
	filerGuard := security.NewGuard("", "", "")
	volumeGuard := security.NewGuard("", "", "")

	// Create log buffer
	logBuf := log_buffer.NewLogBuffer(
		3,                      // maxSegmentCount
		2*1024*1024,             // maxSegmentBytes
		time.Duration(opt.LogFlushIntervalSeconds)*time.Second, // flushInterval
		nil,                    // notifyFn (set later)
		nil,                    // flushFn (set later)
		logger,
	)

	// Create lock manager
	dlm := filer.NewDistributedLockManager(nil, logger) // store set later

	// Create limit cond
	listenersMu := &sync.Mutex{}
	inFlightMu := &sync.Mutex{}

	filerServer := &FilerServer{
		option:            opt,
		filerGuard:        filerGuard,
		volumeGuard:       volumeGuard,
		grpcServer:        grpcServer,
		knownListeners:    make(map[int32]bool),
		listenersMu:       *listenersMu,
		listenersCond:     sync.NewCond(listenersMu),
		inFlightDataSize:  0,
		inFlightLimitCond: sync.NewCond(inFlightMu),
		logBuffer:         logBuf,
		dlm:               dlm,
		ctx:               ctx,
		cancel:            cancel,
		logger:            logger,
	}

	// Register HTTP routes
	filerServer.registerRoutes(defaultMux, readonlyMux)

	return filerServer, nil
}

// registerRoutes registers HTTP handlers for the filer server.
func (fs *FilerServer) registerRoutes(defaultMux, readonlyMux *http.ServeMux) {
	// Read/write routes
	defaultMux.HandleFunc("POST /{path}", fs.handleFileUpload)
	defaultMux.HandleFunc("GET /{path}", fs.handleFileDownload)
	defaultMux.HandleFunc("DELETE /{path}", fs.handleDeleteEntry)
	defaultMux.HandleFunc("GET /healthz", fs.handleHealthz)

	// Read-only routes
	readonlyMux.HandleFunc("GET /{path}", fs.handleFileDownload)
}

// SetStore sets the filer store.
func (fs *FilerServer) SetStore(store filer.FilerStore) {
	if fs.filer == nil {
		fs.filer = filer.NewFiler(store, fs.option.Masters, fs.logger)
	} else {
		fs.filer.SetStore(store)
	}
}

// Start starts the filer server.
func (fs *FilerServer) Start() {
	// Start log buffer flush daemon
	fs.logBuffer.StartFlushDaemon(fs.ctx)

	// TODO: Start metadata aggregator (Phase 5)
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

// handleFileUpload handles POST /{path} requests.
func (fs *FilerServer) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	// Throttle in-flight data
	if fs.option.ConcurrentUploadLimitMB > 0 {
		limit := fs.option.ConcurrentUploadLimitMB * 1024 * 1024
		fs.inFlightLimitCond.L.Lock()
		for atomic.LoadInt64(&fs.inFlightDataSize) > limit {
			fs.inFlightLimitCond.Wait()
		}
		atomic.AddInt64(&fs.inFlightDataSize, r.ContentLength)
		fs.inFlightLimitCond.L.Unlock()

		defer func() {
			atomic.AddInt64(&fs.inFlightDataSize, -r.ContentLength)
			fs.inFlightLimitCond.Signal()
		}()
	}

	// Check authorization
	if !fs.filerGuard.FilerAllowed(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Get path from URL
	path := "/" + r.URL.Path[1:]

	// For Phase 4, only support inline storage (<64KB)
	if r.ContentLength > 64*1024 {
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{"error": "chunked files not supported yet"})
		return
	}

	// Read data
	data, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
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
	if fs.filer != nil {
		err = fs.filer.CreateEntry(fs.ctx, entry)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
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
	// Check authorization
	if !fs.filerGuard.FilerAllowed(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Get path from URL
	path := "/" + r.URL.Path[1:]

	// Find entry
	if fs.filer == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

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
		w.Write(entry.Content)
		return
	}

	// Chunked files not supported yet (Phase 5)
	w.WriteHeader(http.StatusNotImplemented)
	json.NewEncoder(w).Encode(map[string]string{"error": "chunked files not supported yet"})
}

// handleDeleteEntry handles DELETE /{path} requests.
func (fs *FilerServer) handleDeleteEntry(w http.ResponseWriter, r *http.Request) {
	// Check authorization
	if !fs.filerGuard.FilerAllowed(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Get path from URL
	path := "/" + r.URL.Path[1:]

	// Delete entry
	if fs.filer != nil {
		err := fs.filer.DeleteEntry(fs.ctx, filer.FullPath(path))
		if err != nil {
			if err == filer.ErrNotFound {
				w.WriteHeader(http.StatusNotFound)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
	}

	// Append to log buffer
	fs.logBuffer.AppendEntry([]byte(fmt.Sprintf("delete:%s", path)))

	w.WriteHeader(http.StatusNoContent)
}

// handleHealthz handles GET /healthz requests.
func (fs *FilerServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
