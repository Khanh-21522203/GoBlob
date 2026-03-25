package command

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"google.golang.org/grpc"

	"GoBlob/goblob/filer"
	"GoBlob/goblob/filer/storeloader"
	"GoBlob/goblob/security"
	"GoBlob/goblob/server"
)

// RunnableServer represents a started server component that exposes its bound
// address and can be gracefully shut down.
type RunnableServer interface {
	// Addr returns the HTTP address the server is listening on (host:port).
	Addr() string
	// Shutdown gracefully stops the server within the given context deadline.
	Shutdown(ctx context.Context) error
}

// ServerDeps holds optional pre-configured dependencies that override the
// defaults used by the build* functions. All fields are optional; zero values
// trigger production defaults (allocate a fresh listener, create a new
// gRPC server, etc.). Tests inject pre-allocated listeners to avoid port
// conflicts and to control the bound address.
type ServerDeps struct {
	HTTPListener net.Listener
	GRPCListener net.Listener
}

// Builder vars — tests replace these to inject stubs without changing Run.
var (
	DefaultMasterBuilder func(*server.MasterOption, ServerDeps) (RunnableServer, error)       = buildMasterRuntime
	DefaultVolumeBuilder func(*server.VolumeServerOption, ServerDeps) (RunnableServer, error) = buildVolumeRuntime
	DefaultFilerBuilder  func(*server.FilerOption, ServerDeps) (RunnableServer, error)        = buildFilerRuntime
)

// masterRuntime wraps a running master server, its HTTP and gRPC listeners.
type masterRuntime struct {
	server     *server.MasterServer
	httpServer *http.Server
	grpcServer *grpc.Server
	addr       string
}

func (rt *masterRuntime) Addr() string { return rt.addr }

func (rt *masterRuntime) Shutdown(ctx context.Context) error {
	if rt == nil {
		return nil
	}
	if rt.server != nil {
		rt.server.Shutdown()
	}
	if rt.httpServer != nil {
		_ = rt.httpServer.Shutdown(ctx)
	}
	if rt.grpcServer != nil {
		rt.grpcServer.GracefulStop()
	}
	return nil
}

func buildMasterRuntime(opt *server.MasterOption, deps ServerDeps) (RunnableServer, error) {
	mux := http.NewServeMux()
	grpcServer := grpc.NewServer()
	ms, err := server.NewMasterServerWithGRPC(mux, grpcServer, opt)
	if err != nil {
		return nil, err
	}

	httpLis := deps.HTTPListener
	if httpLis == nil {
		httpLis, err = net.Listen("tcp", bindAddress(opt.Host, opt.Port))
		if err != nil {
			ms.Shutdown()
			return nil, fmt.Errorf("listen master http: %w", err)
		}
	}
	grpcLis := deps.GRPCListener
	if grpcLis == nil {
		grpcLis, err = net.Listen("tcp", bindAddress(opt.Host, opt.GRPCPort))
		if err != nil {
			_ = httpLis.Close()
			ms.Shutdown()
			return nil, fmt.Errorf("listen master grpc: %w", err)
		}
	}

	httpServer := &http.Server{
		Handler: security.ApplyHardening(mux, security.HardeningOption{
			MaxBodyBytes:  100 * 1024 * 1024,
			Burst:         burstFor(opt.RatePerSecond, 200),
			RatePerSecond: opt.RatePerSecond,
		}),
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() { _ = httpServer.Serve(httpLis) }()
	go func() { _ = grpcServer.Serve(grpcLis) }()

	return &masterRuntime{
		server:     ms,
		httpServer: httpServer,
		grpcServer: grpcServer,
		addr:       httpLis.Addr().String(),
	}, nil
}

// startMasterRuntime is a convenience wrapper for the default (no-inject) case.
func startMasterRuntime(opt *server.MasterOption) (*masterRuntime, error) {
	rs, err := buildMasterRuntime(opt, ServerDeps{})
	if err != nil {
		return nil, err
	}
	return rs.(*masterRuntime), nil
}

// volumeRuntime wraps a running volume server, its HTTP and gRPC listeners.
type volumeRuntime struct {
	server          *server.VolumeServer
	adminHTTPServer *http.Server
	grpcServer      *grpc.Server
	addr            string
}

func (rt *volumeRuntime) Addr() string { return rt.addr }

func (rt *volumeRuntime) Shutdown(ctx context.Context) error {
	if rt == nil {
		return nil
	}
	if rt.server != nil {
		rt.server.Shutdown()
	}
	if rt.adminHTTPServer != nil {
		_ = rt.adminHTTPServer.Shutdown(ctx)
	}
	if rt.grpcServer != nil {
		rt.grpcServer.GracefulStop()
	}
	return nil
}

func buildVolumeRuntime(opt *server.VolumeServerOption, deps ServerDeps) (RunnableServer, error) {
	adminMux := http.NewServeMux()
	publicMux := http.NewServeMux()
	grpcServer := grpc.NewServer()
	vs, err := server.NewVolumeServer(adminMux, publicMux, grpcServer, opt)
	if err != nil {
		return nil, err
	}

	adminLis := deps.HTTPListener
	if adminLis == nil {
		adminLis, err = net.Listen("tcp", bindAddress(opt.Host, opt.Port))
		if err != nil {
			vs.Shutdown()
			return nil, fmt.Errorf("listen volume http: %w", err)
		}
	}
	grpcLis := deps.GRPCListener
	if grpcLis == nil {
		grpcLis, err = net.Listen("tcp", bindAddress(opt.Host, opt.GRPCPort))
		if err != nil {
			_ = adminLis.Close()
			vs.Shutdown()
			return nil, fmt.Errorf("listen volume grpc: %w", err)
		}
	}

	adminServer := &http.Server{
		Handler: security.ApplyHardening(adminMux, security.HardeningOption{
			MaxBodyBytes:  opt.FileSizeLimitMB * 1024 * 1024,
			Burst:         burstFor(opt.RatePerSecond, 200),
			RatePerSecond: opt.RatePerSecond,
		}),
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() { _ = adminServer.Serve(adminLis) }()
	go func() { _ = grpcServer.Serve(grpcLis) }()

	vs.Start()

	return &volumeRuntime{
		server:          vs,
		adminHTTPServer: adminServer,
		grpcServer:      grpcServer,
		addr:            adminLis.Addr().String(),
	}, nil
}

func startVolumeRuntime(opt *server.VolumeServerOption) (*volumeRuntime, error) {
	rs, err := buildVolumeRuntime(opt, ServerDeps{})
	if err != nil {
		return nil, err
	}
	return rs.(*volumeRuntime), nil
}

// filerRuntime wraps a running filer server, its HTTP and gRPC listeners.
type filerRuntime struct {
	server     *server.FilerServer
	httpServer *http.Server
	grpcServer *grpc.Server
	store      filer.FilerStore
	addr       string
}

func (rt *filerRuntime) Addr() string { return rt.addr }

func (rt *filerRuntime) Shutdown(ctx context.Context) error {
	if rt == nil {
		return nil
	}
	if rt.server != nil {
		rt.server.Shutdown()
	}
	if rt.httpServer != nil {
		_ = rt.httpServer.Shutdown(ctx)
	}
	if rt.grpcServer != nil {
		rt.grpcServer.GracefulStop()
	}
	if rt.store != nil {
		rt.store.Shutdown()
	}
	return nil
}

func buildFilerRuntime(opt *server.FilerOption, deps ServerDeps) (RunnableServer, error) {
	defaultMux := http.NewServeMux()
	readonlyMux := http.NewServeMux()
	grpcServer := grpc.NewServer()
	fs, err := server.NewFilerServer(defaultMux, readonlyMux, grpcServer, opt)
	if err != nil {
		return nil, err
	}

	storeCfg := map[string]string{
		"backend":      opt.StoreBackend,
		"store.dir":    opt.DefaultStoreDir,
		"leveldb2.dir": opt.DefaultStoreDir,
	}
	for k, v := range opt.StoreConfig {
		storeCfg[k] = v
	}
	store, err := storeloader.LoadFilerStoreFromConfig(storeCfg)
	if err != nil {
		fs.Shutdown()
		return nil, fmt.Errorf("init filer store: %w", err)
	}
	fs.SetStore(store)
	fs.Start()

	httpLis := deps.HTTPListener
	if httpLis == nil {
		httpLis, err = net.Listen("tcp", bindAddress(opt.Host, opt.Port))
		if err != nil {
			store.Shutdown()
			fs.Shutdown()
			return nil, fmt.Errorf("listen filer http: %w", err)
		}
	}
	grpcLis := deps.GRPCListener
	if grpcLis == nil {
		grpcLis, err = net.Listen("tcp", bindAddress(opt.Host, opt.GRPCPort))
		if err != nil {
			_ = httpLis.Close()
			store.Shutdown()
			fs.Shutdown()
			return nil, fmt.Errorf("listen filer grpc: %w", err)
		}
	}

	httpServer := &http.Server{
		Handler: security.ApplyHardening(defaultMux, security.HardeningOption{
			MaxBodyBytes:  int64(opt.MaxFileSizeMB) * 1024 * 1024,
			Burst:         burstFor(opt.RatePerSecond, 150),
			RatePerSecond: opt.RatePerSecond,
		}),
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() { _ = httpServer.Serve(httpLis) }()
	go func() { _ = grpcServer.Serve(grpcLis) }()

	return &filerRuntime{
		server:     fs,
		httpServer: httpServer,
		grpcServer: grpcServer,
		store:      store,
		addr:       httpLis.Addr().String(),
	}, nil
}

func startFilerRuntime(opt *server.FilerOption) (*filerRuntime, error) {
	rs, err := buildFilerRuntime(opt, ServerDeps{})
	if err != nil {
		return nil, err
	}
	return rs.(*filerRuntime), nil
}

// httpRuntime wraps a standalone HTTP service (e.g. S3 gateway, metrics).
type httpRuntime struct {
	httpServer *http.Server
	stopFn     func()
	addr       string
}

func (rt *httpRuntime) Addr() string { return rt.addr }

func (rt *httpRuntime) Shutdown(ctx context.Context) error {
	if rt == nil {
		return nil
	}
	if rt.stopFn != nil {
		rt.stopFn()
	}
	if rt.httpServer != nil {
		_ = rt.httpServer.Shutdown(ctx)
	}
	return nil
}

func startHTTPRuntime(host string, port int, handler http.Handler, stopFn func()) (*httpRuntime, error) {
	lis, err := net.Listen("tcp", bindAddress(host, port))
	if err != nil {
		if stopFn != nil {
			stopFn()
		}
		return nil, err
	}
	httpServer := &http.Server{
		Handler: security.ApplyHardening(handler, security.HardeningOption{
			MaxBodyBytes: 100 * 1024 * 1024,
			Burst:        200,
		}),
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() { _ = httpServer.Serve(lis) }()
	return &httpRuntime{httpServer: httpServer, stopFn: stopFn, addr: lis.Addr().String()}, nil
}

func shutdownCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// burstFor returns a burst size proportional to rps (rps/10, min defaultBurst).
func burstFor(rps float64, defaultBurst int) int {
	if rps <= 0 {
		return defaultBurst
	}
	b := int(rps / 10)
	if b < defaultBurst {
		return defaultBurst
	}
	return b
}
