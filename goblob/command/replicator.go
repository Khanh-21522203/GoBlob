package command

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	asyncrepl "GoBlob/goblob/replication/async"
)

// ReplicatorCommand starts one or more async cross-region replication pipelines.
type ReplicatorCommand struct {
	sourceFiler   string
	targetFiler   string
	sourceCluster string
	targetCluster string
	pathPrefix    string
	batchSize     int
	flushInterval time.Duration
	metricsPort   int
}

func init() {
	Register(&ReplicatorCommand{})
}

func (c *ReplicatorCommand) Name() string     { return "replicator" }
func (c *ReplicatorCommand) Synopsis() string { return "run async cross-region replication" }
func (c *ReplicatorCommand) Usage() string {
	return "blob replicator -sourceFiler localhost:18888 -targetFiler remote:18888 -sourceCluster region-a -targetCluster region-b"
}

func (c *ReplicatorCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.sourceFiler, "sourceFiler", "localhost:18888", "source filer gRPC address (host:grpcPort)")
	fs.StringVar(&c.targetFiler, "targetFiler", "", "target filer gRPC address (host:grpcPort)")
	fs.StringVar(&c.sourceCluster, "sourceCluster", "region-a", "source cluster name")
	fs.StringVar(&c.targetCluster, "targetCluster", "region-b", "target cluster name")
	fs.StringVar(&c.pathPrefix, "pathPrefix", "/", "path prefix to replicate (e.g. /buckets)")
	fs.IntVar(&c.batchSize, "batchSize", 64, "max events processed per batch")
	fs.DurationVar(&c.flushInterval, "flushInterval", 5*time.Second, "flush interval for batched events")
	fs.IntVar(&c.metricsPort, "metricsPort", 0, "metrics/debug HTTP port (disabled when 0)")
}

func (c *ReplicatorCommand) Run(ctx context.Context, args []string) error {
	_ = args

	if strings.TrimSpace(c.targetFiler) == "" {
		return fmt.Errorf("-targetFiler is required")
	}
	if strings.TrimSpace(c.targetCluster) == "" {
		return fmt.Errorf("-targetCluster is required")
	}

	cfg := asyncrepl.Config{
		SourceCluster: c.sourceCluster,
		SourceFiler:   c.sourceFiler,
		TargetCluster: c.targetCluster,
		TargetFiler:   c.targetFiler,
		PathPrefix:    c.pathPrefix,
		BatchSize:     c.batchSize,
		FlushInterval: c.flushInterval,
	}

	r, err := asyncrepl.NewReplicator(cfg)
	if err != nil {
		return fmt.Errorf("create replicator: %w", err)
	}

	metricsRT := startMetricsRuntime("", c.metricsPort, "", "")

	fmt.Printf("replicator started: %s -> %s (pathPrefix=%s)\n", c.sourceCluster, c.targetCluster, c.pathPrefix)

	if err := r.Start(ctx); err != nil && ctx.Err() == nil {
		shutdownCtx, cancel := shutdownCtx()
		metricsRT.Shutdown(shutdownCtx)
		cancel()
		return fmt.Errorf("replicator: %w", err)
	}

	shutdownCtx, cancel := shutdownCtx()
	defer cancel()
	metricsRT.Shutdown(shutdownCtx)
	return nil
}
