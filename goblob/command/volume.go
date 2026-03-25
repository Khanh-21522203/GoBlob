package command

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"GoBlob/goblob/server"
)

type VolumeCommand struct {
	host                      string
	port                      int
	grpcPort                  int
	metricsPort               int
	masters                   string
	dirs                      []string
	maxVolumes                int
	readMode                  string
	fileSizeLimitMB           int64
	concurrentUploadLimitMB   int64
	concurrentDownloadLimitMB int64
	dataCenter                string
	rack                      string
	pushgatewayURL            string
	pushgatewayJob            string
	ratePerSecond             float64
}

func init() {
	Register(&VolumeCommand{})
}

func (c *VolumeCommand) Name() string     { return "volume" }
func (c *VolumeCommand) Synopsis() string { return "run volume server" }
func (c *VolumeCommand) Usage() string {
	return "blob volume -masters localhost:9333 -dir ./tmp/volume"
}

func (c *VolumeCommand) SetFlags(fs *flag.FlagSet) {
	def := server.DefaultVolumeServerOption()
	c.dirs = []string{}
	fs.StringVar(&c.host, "ip", "127.0.0.1", "bind host")
	fs.IntVar(&c.port, "port", def.Port, "volume HTTP port")
	fs.IntVar(&c.grpcPort, "grpc.port", def.GRPCPort, "volume gRPC port")
	fs.IntVar(&c.metricsPort, "metricsPort", 0, "metrics/debug HTTP port (disabled when 0)")
	fs.StringVar(&c.masters, "masters", strings.Join(def.Masters, ","), "comma-separated master HTTP addresses")
	fs.Var(newStringSliceFlag(&c.dirs), "dir", "volume data directory (repeatable)")
	fs.IntVar(&c.maxVolumes, "max", 7, "max volumes per directory")
	fs.StringVar(&c.readMode, "readMode", def.ReadMode, "read mode: local/proxy/redirect")
	fs.Int64Var(&c.fileSizeLimitMB, "fileSizeLimitMB", def.FileSizeLimitMB, "max file size in MB")
	fs.Int64Var(&c.concurrentUploadLimitMB, "concurrentUploadLimitMB", def.ConcurrentUploadLimitMB, "upload throttle MB")
	fs.Int64Var(&c.concurrentDownloadLimitMB, "concurrentDownloadLimitMB", def.ConcurrentDownloadLimitMB, "download throttle MB")
	fs.StringVar(&c.dataCenter, "dataCenter", def.DataCenter, "data center")
	fs.StringVar(&c.rack, "rack", def.Rack, "rack")
	fs.StringVar(&c.pushgatewayURL, "pushgatewayURL", "", "pushgateway base URL")
	fs.StringVar(&c.pushgatewayJob, "pushgatewayJob", "goblob-volume", "pushgateway job name")
	fs.Float64Var(&c.ratePerSecond, "ratePerSecond", 0, "per-IP HTTP rate limit (req/sec); 0 = default 200")
}

func (c *VolumeCommand) Run(ctx context.Context, args []string) error {
	_ = args
	if c.readMode != "local" && c.readMode != "proxy" && c.readMode != "redirect" {
		return fmt.Errorf("invalid readMode %q", c.readMode)
	}
	opt := server.DefaultVolumeServerOption()
	opt.Host = c.host
	opt.Port = c.port
	opt.GRPCPort = c.grpcPort
	opt.Masters = splitCSV(c.masters)
	if len(c.dirs) == 0 {
		c.dirs = []string{"./tmp/volume"}
	}
	opt.Directories = make([]server.DiskDirectoryConfig, 0, len(c.dirs))
	for _, dir := range c.dirs {
		opt.Directories = append(opt.Directories, server.DiskDirectoryConfig{Path: dir, MaxVolumeCount: int32(c.maxVolumes)})
	}
	opt.ReadMode = c.readMode
	opt.FileSizeLimitMB = c.fileSizeLimitMB
	opt.ConcurrentUploadLimitMB = c.concurrentUploadLimitMB
	opt.ConcurrentDownloadLimitMB = c.concurrentDownloadLimitMB
	opt.DataCenter = c.dataCenter
	opt.Rack = c.rack
	opt.PublicUrl = fmt.Sprintf("%s:%d", c.host, c.port)
	opt.RatePerSecond = c.ratePerSecond

	rt, err := startVolumeRuntime(opt)
	if err != nil {
		return err
	}
	metricsRT := startMetricsRuntime(c.host, c.metricsPort, c.pushgatewayURL, c.pushgatewayJob)
	reload := func() error {
		secCfg, err := loadSecurityConfig()
		if err != nil {
			return err
		}
		rt.server.ReloadSecurityConfig(secCfg)
		return rt.server.LoadNewVolumes()
	}
	if err := reload(); err != nil {
		shutdownCtx, cancel := shutdownCtx()
		metricsRT.Shutdown(shutdownCtx)
		rt.Shutdown(shutdownCtx)
		cancel()
		return err
	}
	setReloadHook(reload)
	defer setReloadHook(nil)

	<-ctx.Done()
	shutdownCtx, cancel := shutdownCtx()
	defer cancel()
	metricsRT.Shutdown(shutdownCtx)
	rt.Shutdown(shutdownCtx)
	return nil
}
