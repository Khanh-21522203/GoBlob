package command

import (
	"context"
	"flag"
	"path/filepath"
	"strings"

	"GoBlob/goblob/server"
)

type FilerCommand struct {
	host                    string
	port                    int
	grpcPort                int
	masters                 string
	defaultStoreDir         string
	defaultReplication      string
	defaultCollection       string
	maxFileSizeMB           int
	concurrentUploadLimitMB int64
	bucketsFolder           string
	dataCenter              string
	rack                    string
}

func init() {
	Register(&FilerCommand{})
}

func (c *FilerCommand) Name() string     { return "filer" }
func (c *FilerCommand) Synopsis() string { return "run filer server" }
func (c *FilerCommand) Usage() string {
	return "blob filer -masters localhost:9333"
}

func (c *FilerCommand) SetFlags(fs *flag.FlagSet) {
	def := server.DefaultFilerOption()
	fs.StringVar(&c.host, "ip", "127.0.0.1", "bind host")
	fs.IntVar(&c.port, "port", def.Port, "filer HTTP port")
	fs.IntVar(&c.grpcPort, "grpc.port", def.GRPCPort, "filer gRPC port")
	fs.StringVar(&c.masters, "masters", strings.Join(def.Masters, ","), "comma-separated master HTTP addresses")
	fs.StringVar(&c.defaultStoreDir, "storeDir", def.DefaultStoreDir, "filer metadata store directory")
	fs.StringVar(&c.defaultReplication, "defaultReplication", def.DefaultReplication, "default replication")
	fs.StringVar(&c.defaultCollection, "defaultCollection", def.DefaultCollection, "default collection")
	fs.IntVar(&c.maxFileSizeMB, "maxMB", def.MaxFileSizeMB, "max file size in MB")
	fs.Int64Var(&c.concurrentUploadLimitMB, "concurrentUploadLimitMB", def.ConcurrentUploadLimitMB, "upload throttle MB")
	fs.StringVar(&c.bucketsFolder, "bucketsFolder", def.BucketsFolder, "buckets root path")
	fs.StringVar(&c.dataCenter, "dataCenter", def.DataCenter, "data center")
	fs.StringVar(&c.rack, "rack", def.Rack, "rack")
}

func (c *FilerCommand) Run(ctx context.Context, args []string) error {
	_ = args
	opt := server.DefaultFilerOption()
	opt.Host = c.host
	opt.Port = c.port
	opt.GRPCPort = c.grpcPort
	opt.Masters = splitCSV(c.masters)
	opt.DefaultStoreDir = c.defaultStoreDir
	opt.DefaultReplication = c.defaultReplication
	opt.DefaultCollection = c.defaultCollection
	opt.MaxFileSizeMB = c.maxFileSizeMB
	opt.ConcurrentUploadLimitMB = c.concurrentUploadLimitMB
	opt.BucketsFolder = c.bucketsFolder
	opt.DataCenter = c.dataCenter
	opt.Rack = c.rack

	if opt.DefaultStoreDir == "" {
		opt.DefaultStoreDir = filepath.Join(".", "tmp", "filer")
	}

	rt, err := startFilerRuntime(opt)
	if err != nil {
		return err
	}
	reload := func() error {
		secCfg, err := loadSecurityConfig()
		if err != nil {
			return err
		}
		rt.server.ReloadConfig(secCfg)
		return nil
	}
	if err := reload(); err != nil {
		shutdownCtx, cancel := shutdownCtx()
		rt.shutdown(shutdownCtx)
		cancel()
		return err
	}
	setReloadHook(reload)
	defer setReloadHook(nil)

	<-ctx.Done()
	shutdownCtx, cancel := shutdownCtx()
	defer cancel()
	rt.shutdown(shutdownCtx)
	return nil
}
