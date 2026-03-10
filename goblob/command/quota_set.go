package command

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"GoBlob/goblob/quota"
)

// QuotaSetCommand sets user or bucket max quota.
type QuotaSetCommand struct {
	filerAddr string
	userID    string
	bucket    string
	maxBytes  int64
}

func init() {
	Register(&QuotaSetCommand{})
}

func (c *QuotaSetCommand) Name() string     { return "quota.set" }
func (c *QuotaSetCommand) Synopsis() string { return "set user or bucket storage quota" }
func (c *QuotaSetCommand) Usage() string {
	return "blob quota.set -filer 127.0.0.1:8888 (-user alice | -bucket photos) -max_bytes 1048576"
}

func (c *QuotaSetCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.filerAddr, "filer", "127.0.0.1:8888", "filer http address")
	fs.StringVar(&c.userID, "user", "", "user id")
	fs.StringVar(&c.bucket, "bucket", "", "bucket name")
	fs.Int64Var(&c.maxBytes, "max_bytes", 0, "max quota in bytes (0 = unlimited)")
}

func (c *QuotaSetCommand) Run(ctx context.Context, args []string) error {
	_ = args
	if c.maxBytes < 0 {
		return fmt.Errorf("-max_bytes must be >= 0")
	}
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
		if q == nil {
			q = &quota.Quota{}
		}
		q.MaxBytes = c.maxBytes
		if err := mgr.SetUserQuota(ctx, userID, q); err != nil {
			return err
		}
		fmt.Printf("set user quota: user=%s max_bytes=%d used_bytes=%d\n", userID, q.MaxBytes, q.UsedBytes)
		return nil
	}

	q, err := mgr.GetBucketQuota(ctx, bucket)
	if err != nil {
		return err
	}
	if q == nil {
		q = &quota.Quota{}
	}
	q.MaxBytes = c.maxBytes
	if err := mgr.SetBucketQuota(ctx, bucket, q); err != nil {
		return err
	}
	fmt.Printf("set bucket quota: bucket=%s max_bytes=%d used_bytes=%d\n", bucket, q.MaxBytes, q.UsedBytes)
	return nil
}
