package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"GoBlob/goblob/server"
)

type MasterCommand struct {
	host               string
	port               int
	grpcPort           int
	metaDir            string
	peers              string
	volumeSizeLimitMB  uint
	defaultReplication string
	garbageThreshold   float64
	dataCenter         string
	rack               string
	maintenanceScripts string
	maintenanceSleep   time.Duration
	maintenanceFiler   string
}

func init() {
	Register(&MasterCommand{})
}

func (c *MasterCommand) Name() string     { return "master" }
func (c *MasterCommand) Synopsis() string { return "run master server" }
func (c *MasterCommand) Usage() string {
	return "blob master -port 9333 -mdir /tmp/goblob/master"
}

func (c *MasterCommand) SetFlags(fs *flag.FlagSet) {
	def := server.DefaultMasterOption()
	fs.StringVar(&c.host, "ip", "127.0.0.1", "bind host")
	fs.IntVar(&c.port, "port", def.Port, "master HTTP port")
	fs.IntVar(&c.grpcPort, "grpc.port", def.GRPCPort, "master gRPC port")
	fs.StringVar(&c.metaDir, "mdir", def.MetaDir, "master metadata directory")
	fs.StringVar(&c.peers, "peers", "", "comma-separated peer gRPC addresses")
	fs.UintVar(&c.volumeSizeLimitMB, "volumeSizeLimitMB", uint(def.VolumeSizeLimitMB), "max volume size in MB")
	fs.StringVar(&c.defaultReplication, "replication", def.DefaultReplication, "default replication")
	fs.Float64Var(&c.garbageThreshold, "garbageThreshold", def.GarbageThreshold, "garbage threshold")
	fs.StringVar(&c.dataCenter, "dataCenter", def.DataCenter, "data center")
	fs.StringVar(&c.rack, "rack", def.Rack, "rack")
	fs.StringVar(&c.maintenanceScripts, "maintenanceScripts", def.MaintenanceScripts, "maintenance script file path")
	fs.DurationVar(&c.maintenanceSleep, "maintenanceSleep", def.MaintenanceSleep, "maintenance script interval")
	fs.StringVar(&c.maintenanceFiler, "maintenanceFiler", "127.0.0.1:8888", "filer HTTP address used by maintenance shell")
}

func (c *MasterCommand) Run(ctx context.Context, args []string) error {
	_ = args
	opt := server.DefaultMasterOption()
	opt.Host = c.host
	opt.Port = c.port
	opt.GRPCPort = c.grpcPort
	opt.MetaDir = c.metaDir
	opt.Peers = splitCSV(c.peers)
	opt.VolumeSizeLimitMB = uint32(c.volumeSizeLimitMB)
	opt.DefaultReplication = c.defaultReplication
	opt.GarbageThreshold = c.garbageThreshold
	opt.DataCenter = c.dataCenter
	opt.Rack = c.rack
	opt.MaintenanceScripts = c.maintenanceScripts
	opt.MaintenanceSleep = c.maintenanceSleep

	rt, err := startMasterRuntime(opt)
	if err != nil {
		return err
	}
	reload := func() error {
		secCfg, err := loadSecurityConfig()
		if err != nil {
			return err
		}
		rt.server.ReloadSecurityConfig(secCfg)
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

	script, err := loadMaintenanceScript(opt.MaintenanceScripts)
	if err != nil {
		shutdownCtx, cancel := shutdownCtx()
		rt.shutdown(shutdownCtx)
		cancel()
		return err
	}
	stopMaintenance := startMaintenanceScheduler(
		ctx,
		rt.server,
		defaultMaintenanceShell(fmt.Sprintf("%s:%d", c.host, c.port), c.maintenanceFiler),
		script,
		opt.MaintenanceSleep,
		os.Stderr,
	)
	defer stopMaintenance()

	<-ctx.Done()
	shutdownCtx, cancel := shutdownCtx()
	defer cancel()
	rt.shutdown(shutdownCtx)
	return nil
}
