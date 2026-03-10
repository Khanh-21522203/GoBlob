package command

import (
	"context"
	"flag"
	"fmt"
	"strings"
)

// QuotaGetCommand gets user or bucket quota values.
type QuotaGetCommand struct {
	filerAddr string
	userID    string
	bucket    string
}

func init() {
	Register(&QuotaGetCommand{})
}

func (c *QuotaGetCommand) Name() string     { return "quota.get" }
func (c *QuotaGetCommand) Synopsis() string { return "get user or bucket storage quota" }
func (c *QuotaGetCommand) Usage() string {
	return "blob quota.get -filer 127.0.0.1:8888 (-user alice | -bucket photos)"
}

func (c *QuotaGetCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.filerAddr, "filer", "127.0.0.1:8888", "filer http address")
	fs.StringVar(&c.userID, "user", "", "user id")
	fs.StringVar(&c.bucket, "bucket", "", "bucket name")
}

func (c *QuotaGetCommand) Run(ctx context.Context, args []string) error {
	_ = args
	userID := strings.TrimSpace(c.userID)
	bucket := strings.TrimSpace(c.bucket)
	if (userID == "" && bucket == "") || (userID != "" && bucket != "") {
		return fmt.Errorf("exactly one of -user or -bucket must be set")
	}
	mgr, err := newQuotaManager(c.filerAddr)
	if err != nil {
		return err
	}

	if userID != "" {
		q, err := mgr.GetUserQuota(ctx, userID)
		if err != nil {
			return err
		}
		fmt.Printf("user=%s max_bytes=%d used_bytes=%d\n", userID, q.MaxBytes, q.UsedBytes)
		return nil
	}

	q, err := mgr.GetBucketQuota(ctx, bucket)
	if err != nil {
		return err
	}
	fmt.Printf("bucket=%s max_bytes=%d used_bytes=%d\n", bucket, q.MaxBytes, q.UsedBytes)
	return nil
}
