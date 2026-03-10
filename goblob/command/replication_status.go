package command

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	asyncrepl "GoBlob/goblob/replication/async"
)

// ReplicationStatusCommand prints in-process async replication statuses.
type ReplicationStatusCommand struct {
	target string
}

func init() {
	Register(&ReplicationStatusCommand{})
}

func (c *ReplicationStatusCommand) Name() string { return "replication.status" }
func (c *ReplicationStatusCommand) Synopsis() string {
	return "show async cross-region replication status"
}
func (c *ReplicationStatusCommand) Usage() string {
	return "blob replication.status [-target region-b]"
}

func (c *ReplicationStatusCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.target, "target", "", "target cluster name filter")
}

func (c *ReplicationStatusCommand) Run(ctx context.Context, args []string) error {
	_ = ctx
	_ = args

	statuses := asyncrepl.SnapshotStatuses()
	if len(statuses) == 0 {
		fmt.Println("no active replication status found")
		return nil
	}

	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].TargetCluster < statuses[j].TargetCluster
	})

	target := strings.TrimSpace(c.target)
	for _, st := range statuses {
		if target != "" && st.TargetCluster != target {
			continue
		}
		fmt.Printf("target=%s lag=%.3fs replicated=%d updated=%s", st.TargetCluster, st.LagSeconds, st.Replicated, st.UpdatedAt.Format(time.RFC3339))
		if st.LastError != "" {
			fmt.Printf(" last_error=%q", st.LastError)
		}
		fmt.Println()
	}
	return nil
}
