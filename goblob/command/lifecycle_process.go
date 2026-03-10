package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/s3api"
	"GoBlob/goblob/s3api/lifecycle"
)

// LifecycleProcessCommand applies lifecycle rules immediately.
type LifecycleProcessCommand struct {
	filerAddr string
	bucket    string
}

func init() {
	Register(&LifecycleProcessCommand{})
}

func (c *LifecycleProcessCommand) Name() string     { return "lifecycle.process" }
func (c *LifecycleProcessCommand) Synopsis() string { return "run lifecycle policy processing" }
func (c *LifecycleProcessCommand) Usage() string {
	return "blob lifecycle.process -filer 127.0.0.1:8888 [-bucket my-bucket]"
}

func (c *LifecycleProcessCommand) SetFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.filerAddr, "filer", "127.0.0.1:8888", "filer http address")
	fs.StringVar(&c.bucket, "bucket", "", "optional single bucket to process")
}

func (c *LifecycleProcessCommand) Run(ctx context.Context, args []string) error {
	_ = args
	addr := strings.TrimSpace(c.filerAddr)
	if addr == "" {
		return fmt.Errorf("-filer is required")
	}
	fc := s3api.NewFilerClient([]types.ServerAddress{types.ServerAddress(addr)}, "/buckets")
	store := &lifecycleStoreAdapter{filer: fc}
	processor := lifecycle.NewProcessor(store)

	bucket := strings.TrimSpace(c.bucket)
	if bucket != "" {
		if err := processor.ProcessBucket(ctx, bucket); err != nil {
			return err
		}
		fmt.Printf("lifecycle processed bucket=%s expired=%d transitioned=%d\n", bucket, store.expired, store.transitioned)
		return nil
	}

	if err := processor.ProcessAllBuckets(ctx); err != nil {
		return err
	}
	fmt.Printf("lifecycle processed all buckets expired=%d transitioned=%d\n", store.expired, store.transitioned)
	return nil
}

type lifecycleStoreAdapter struct {
	filer        *s3api.FilerClient
	expired      int
	transitioned int
}

func (s *lifecycleStoreAdapter) ListBuckets(ctx context.Context) ([]string, error) {
	return s.filer.ListBuckets(ctx)
}

func (s *lifecycleStoreAdapter) LoadLifecycleConfig(ctx context.Context, bucket string) (*lifecycle.LifecycleConfiguration, error) {
	raw, err := s.filer.GetBucketMeta(ctx, bucket, "lifecycle.json")
	if err != nil {
		if errors.Is(err, s3api.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	cfg := &lifecycle.LifecycleConfiguration{}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	return cfg, lifecycle.ValidateConfiguration(cfg)
}

func (s *lifecycleStoreAdapter) ListObjects(ctx context.Context, bucket string) ([]lifecycle.ObjectMeta, error) {
	objects, err := s.filer.ListObjects(ctx, bucket, "")
	if err != nil {
		return nil, err
	}
	out := make([]lifecycle.ObjectMeta, 0, len(objects))
	for _, obj := range objects {
		out = append(out, lifecycle.ObjectMeta{Key: obj.Key, LastModified: obj.LastModified.Unix()})
	}
	return out, nil
}

func (s *lifecycleStoreAdapter) DeleteObject(ctx context.Context, bucket, key string) error {
	if err := s.filer.DeleteObject(ctx, bucket, key); err != nil {
		return err
	}
	s.expired++
	return nil
}

func (s *lifecycleStoreAdapter) TransitionObject(ctx context.Context, bucket, key, storageClass string) error {
	err := s.filer.UpdateObjectExtended(ctx, bucket, key, func(ext map[string][]byte) (map[string][]byte, error) {
		if ext == nil {
			ext = make(map[string][]byte)
		}
		ext["s3:storage_class"] = []byte(storageClass)
		return ext, nil
	})
	if err != nil {
		return err
	}
	s.transitioned++
	return nil
}
