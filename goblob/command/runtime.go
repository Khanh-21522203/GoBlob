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

type masterRuntime struct {
	server     *server.MasterServer
	httpServer *http.Server
	grpcServer *grpc.Server
}

func startMasterRuntime(opt *server.MasterOption) (*masterRuntime, error) {
	mux := http.NewServeMux()
	grpcServer := grpc.NewServer()
	ms, err := server.NewMasterServerWithGRPC(mux, grpcServer, opt)
	if err != nil {
		return nil, err
	}

	httpLis, err := net.Listen("tcp", bindAddress(opt.Host, opt.Port))
	if err != nil {
		ms.Shutdown()
		return nil, fmt.Errorf("listen master http: %w", err)
	}
	grpcLis, err := net.Listen("tcp", bindAddress(opt.Host, opt.GRPCPort))
	if err != nil {
		_ = httpLis.Close()
		ms.Shutdown()
		return nil, fmt.Errorf("listen master grpc: %w", err)
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

	return &masterRuntime{server: ms, httpServer: httpServer, grpcServer: grpcServer}, nil
}

func (rt *masterRuntime) shutdown(ctx context.Context) {
	if rt == nil {
		return
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
}

type volumeRuntime struct {
	server          *server.VolumeServer
	adminHTTPServer *http.Server
	grpcServer      *grpc.Server
}

func startVolumeRuntime(opt *server.VolumeServerOption) (*volumeRuntime, error) {
	adminMux := http.NewServeMux()
	publicMux := http.NewServeMux()
	grpcServer := grpc.NewServer()
	vs, err := server.NewVolumeServer(adminMux, publicMux, grpcServer, opt)
	if err != nil {
		return nil, err
	}

	adminLis, err := net.Listen("tcp", bindAddress(opt.Host, opt.Port))
	if err != nil {
		vs.Shutdown()
		return nil, fmt.Errorf("listen volume http: %w", err)
	}
	grpcLis, err := net.Listen("tcp", bindAddress(opt.Host, opt.GRPCPort))
	if err != nil {
		_ = adminLis.Close()
		vs.Shutdown()
		return nil, fmt.Errorf("listen volume grpc: %w", err)
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

	return &volumeRuntime{server: vs, adminHTTPServer: adminServer, grpcServer: grpcServer}, nil
}

func (rt *volumeRuntime) shutdown(ctx context.Context) {
	if rt == nil {
		return
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
}

type filerRuntime struct {
	server     *server.FilerServer
	httpServer *http.Server
	grpcServer *grpc.Server
	store      filer.FilerStore
}

func startFilerRuntime(opt *server.FilerOption) (*filerRuntime, error) {
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

	httpLis, err := net.Listen("tcp", bindAddress(opt.Host, opt.Port))
	if err != nil {
		store.Shutdown()
		fs.Shutdown()
		return nil, fmt.Errorf("listen filer http: %w", err)
	}
	grpcLis, err := net.Listen("tcp", bindAddress(opt.Host, opt.GRPCPort))
	if err != nil {
		_ = httpLis.Close()
		store.Shutdown()
		fs.Shutdown()
		return nil, fmt.Errorf("listen filer grpc: %w", err)
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

	return &filerRuntime{server: fs, httpServer: httpServer, grpcServer: grpcServer, store: store}, nil
}

func (rt *filerRuntime) shutdown(ctx context.Context) {
	if rt == nil {
		return
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
}

type httpRuntime struct {
	httpServer *http.Server
	stopFn     func()
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
	return &httpRuntime{httpServer: httpServer, stopFn: stopFn}, nil
}

func (rt *httpRuntime) shutdown(ctx context.Context) {
	if rt == nil {
		return
	}
	if rt.stopFn != nil {
		rt.stopFn()
	}
	if rt.httpServer != nil {
		_ = rt.httpServer.Shutdown(ctx)
	}
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
