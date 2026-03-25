package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"net/http"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/s3api"
	"GoBlob/goblob/server"
)

type ServerCommand struct {
	host           string
	metricsPort    int
	pushgatewayURL string
	pushgatewayJob string

	masterPort     int
	masterGRPCPort int
	masterMetaDir  string

	volumePort     int
	volumeGRPCPort int
	volumeDir      string
	volumeMax      int

	filerPort         int
	filerGRPCPort     int
	filerStoreDir     string
	filerStoreBackend string
	filerStoreConfig  []string

	enableS3 bool
	s3Port   int

	masterMaintenanceScripts string
	masterMaintenanceSleep   time.Duration
}

func init() {
	Register(&ServerCommand{})
}

func (c *ServerCommand) Name() string     { return "server" }
func (c *ServerCommand) Synopsis() string { return "run all components in one process" }
func (c *ServerCommand) Usage() string {
	return "blob server -s3=true"
}

func (c *ServerCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.host, "ip", "127.0.0.1", "bind host")
	fs.IntVar(&c.metricsPort, "metricsPort", 0, "metrics/debug HTTP port (disabled when 0)")
	fs.StringVar(&c.pushgatewayURL, "pushgatewayURL", "", "pushgateway base URL")
	fs.StringVar(&c.pushgatewayJob, "pushgatewayJob", "goblob-server", "pushgateway job name")

	fs.IntVar(&c.masterPort, "master.port", 9333, "master HTTP port")
	fs.IntVar(&c.masterGRPCPort, "master.grpc.port", 19333, "master gRPC port")
	fs.StringVar(&c.masterMetaDir, "master.mdir", filepath.Join(".", "tmp", "master"), "master metadata directory")

	fs.IntVar(&c.volumePort, "volume.port", 8080, "volume HTTP port")
	fs.IntVar(&c.volumeGRPCPort, "volume.grpc.port", 18080, "volume gRPC port")
	fs.StringVar(&c.volumeDir, "volume.dir", filepath.Join(".", "tmp", "volume"), "volume data directory")
	fs.IntVar(&c.volumeMax, "volume.max", 7, "max volumes in volume dir")

	fs.IntVar(&c.filerPort, "filer.port", 8888, "filer HTTP port")
	fs.IntVar(&c.filerGRPCPort, "filer.grpc.port", 18888, "filer gRPC port")
	fs.StringVar(&c.filerStoreDir, "filer.storeDir", filepath.Join(".", "tmp", "filer"), "filer metadata store directory")
	fs.StringVar(&c.filerStoreBackend, "filer.storeBackend", "leveldb2", "filer metadata backend (leveldb2|redis3|postgres2|mysql2|cassandra)")
	fs.Var(newStringSliceFlag(&c.filerStoreConfig), "filer.store.config", "filer backend config key=value (repeatable), e.g. redis3.address=127.0.0.1:6379")

	fs.BoolVar(&c.enableS3, "s3", true, "enable s3 gateway")
	fs.IntVar(&c.s3Port, "s3.port", int(types.DefaultS3HTTPPort), "s3 HTTP port")
	fs.StringVar(&c.masterMaintenanceScripts, "master.maintenance.scripts", "", "maintenance script file path")
	fs.DurationVar(&c.masterMaintenanceSleep, "master.maintenance.sleep", 5*time.Minute, "maintenance script interval")
}

func (c *ServerCommand) Run(ctx context.Context, args []string) error {
	_ = args
	metricsRT := startMetricsRuntime(c.host, c.metricsPort, c.pushgatewayURL, c.pushgatewayJob)
	defer func() {
		shutdownCtx, cancel := shutdownCtx()
		metricsRT.Shutdown(shutdownCtx)
		cancel()
	}()

	masterOpt := server.DefaultMasterOption()
	masterOpt.Host = c.host
	masterOpt.Port = c.masterPort
	masterOpt.GRPCPort = c.masterGRPCPort
	masterOpt.MetaDir = c.masterMetaDir

	masterRT, err := startMasterRuntime(masterOpt)
	if err != nil {
		return fmt.Errorf("start master: %w", err)
	}

	time.Sleep(1 * time.Second)

	volumeOpt := server.DefaultVolumeServerOption()
	volumeOpt.Host = c.host
	volumeOpt.Port = c.volumePort
	volumeOpt.GRPCPort = c.volumeGRPCPort
	volumeOpt.PublicUrl = fmt.Sprintf("%s:%d", c.host, c.volumePort)
	volumeOpt.Masters = []string{fmt.Sprintf("%s:%d", c.host, c.masterPort)}
	volumeOpt.Directories = []server.DiskDirectoryConfig{{Path: c.volumeDir, MaxVolumeCount: int32(c.volumeMax)}}

	volumeRT, err := startVolumeRuntime(volumeOpt)
	if err != nil {
		shutdownCtx, cancel := shutdownCtx()
		masterRT.Shutdown(shutdownCtx)
		cancel()
		return fmt.Errorf("start volume: %w", err)
	}

	time.Sleep(1 * time.Second)

	filerOpt := server.DefaultFilerOption()
	filerOpt.Host = c.host
	filerOpt.Port = c.filerPort
	filerOpt.GRPCPort = c.filerGRPCPort
	filerOpt.DefaultStoreDir = c.filerStoreDir
	filerOpt.StoreBackend = c.filerStoreBackend
	storeConfig, err := parseKeyValuePairs(c.filerStoreConfig)
	if err != nil {
		shutdownCtx, cancel := shutdownCtx()
		volumeRT.Shutdown(shutdownCtx)
		masterRT.Shutdown(shutdownCtx)
		cancel()
		return fmt.Errorf("parse filer.store.config: %w", err)
	}
	filerOpt.StoreConfig = storeConfig
	filerOpt.Masters = []string{fmt.Sprintf("%s:%d", c.host, c.masterPort)}

	filerRT, err := startFilerRuntime(filerOpt)
	if err != nil {
		shutdownCtx, cancel := shutdownCtx()
		volumeRT.Shutdown(shutdownCtx)
		masterRT.Shutdown(shutdownCtx)
		cancel()
		return fmt.Errorf("start filer: %w", err)
	}

	time.Sleep(2 * time.Second)

	var s3RT *httpRuntime
	if c.enableS3 {
		mux := http.NewServeMux()
		s3Opt := s3api.DefaultS3ApiServerOption()
		s3Opt.Port = c.s3Port
		s3Opt.Filers = []types.ServerAddress{types.ServerAddress(fmt.Sprintf("%s:%d", c.host, c.filerPort))}
		s3, err := s3api.NewS3ApiServer(mux, s3Opt)
		if err != nil {
			shutdownCtx, cancel := shutdownCtx()
			filerRT.Shutdown(shutdownCtx)
			volumeRT.Shutdown(shutdownCtx)
			masterRT.Shutdown(shutdownCtx)
			cancel()
			return fmt.Errorf("start s3: %w", err)
		}
		s3RT, err = startHTTPRuntime(c.host, c.s3Port, mux, s3.Shutdown)
		if err != nil {
			shutdownCtx, cancel := shutdownCtx()
			filerRT.Shutdown(shutdownCtx)
			volumeRT.Shutdown(shutdownCtx)
			masterRT.Shutdown(shutdownCtx)
			cancel()
			return fmt.Errorf("start s3 listener: %w", err)
		}
	}

	reload := func() error {
		secCfg, err := loadSecurityConfig()
		if err != nil {
			return err
		}
		masterRT.server.ReloadSecurityConfig(secCfg)
		volumeRT.server.ReloadSecurityConfig(secCfg)
		if err := volumeRT.server.LoadNewVolumes(); err != nil {
			return err
		}
		filerRT.server.ReloadConfig(secCfg)
		return nil
	}
	if err := reload(); err != nil {
		shutdownCtx, cancel := shutdownCtx()
		metricsRT.Shutdown(shutdownCtx)
		if s3RT != nil {
			s3RT.Shutdown(shutdownCtx)
		}
		filerRT.Shutdown(shutdownCtx)
		volumeRT.Shutdown(shutdownCtx)
		masterRT.Shutdown(shutdownCtx)
		cancel()
		return fmt.Errorf("reload security config: %w", err)
	}
	setReloadHook(reload)
	defer setReloadHook(nil)

	script, err := loadMaintenanceScript(c.masterMaintenanceScripts)
	if err != nil {
		shutdownCtx, cancel := shutdownCtx()
		metricsRT.Shutdown(shutdownCtx)
		if s3RT != nil {
			s3RT.Shutdown(shutdownCtx)
		}
		filerRT.Shutdown(shutdownCtx)
		volumeRT.Shutdown(shutdownCtx)
		masterRT.Shutdown(shutdownCtx)
		cancel()
		return err
	}
	stopMaintenance := startMaintenanceScheduler(
		ctx,
		masterRT.server,
		defaultMaintenanceShell(fmt.Sprintf("%s:%d", c.host, c.masterPort), fmt.Sprintf("%s:%d", c.host, c.filerPort)),
		script,
		c.masterMaintenanceSleep,
		os.Stderr,
	)
	defer stopMaintenance()

	<-ctx.Done()

	shutdownCtx, cancel := shutdownCtx()
	defer cancel()
	metricsRT.Shutdown(shutdownCtx)
	if s3RT != nil {
		s3RT.Shutdown(shutdownCtx)
	}
	filerRT.Shutdown(shutdownCtx)
	volumeRT.Shutdown(shutdownCtx)
	masterRT.Shutdown(shutdownCtx)
	return nil
}
