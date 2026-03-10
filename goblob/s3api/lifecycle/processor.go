package lifecycle

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Store abstracts object listing and mutation for lifecycle processing.
type Store interface {
	ListBuckets(ctx context.Context) ([]string, error)
	LoadLifecycleConfig(ctx context.Context, bucket string) (*LifecycleConfiguration, error)
	ListObjects(ctx context.Context, bucket string) ([]ObjectMeta, error)
	DeleteObject(ctx context.Context, bucket, key string) error
	TransitionObject(ctx context.Context, bucket, key, storageClass string) error
}

// Processor runs lifecycle rules for buckets.
type Processor struct {
	store    Store
	interval time.Duration
	nowFn    func() time.Time
}

func NewProcessor(store Store) *Processor {
	return &Processor{store: store, interval: 24 * time.Hour, nowFn: time.Now}
}

func (p *Processor) SetInterval(interval time.Duration) {
	if p == nil || interval <= 0 {
		return
	}
	p.interval = interval
}

func (p *Processor) Start(ctx context.Context) {
	if p == nil || p.store == nil {
		return
	}
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = p.ProcessAllBuckets(ctx)
		}
	}
}

func (p *Processor) ProcessAllBuckets(ctx context.Context) error {
	if p == nil || p.store == nil {
		return nil
	}
	buckets, err := p.store.ListBuckets(ctx)
	if err != nil {
		return err
	}
	for _, bucket := range buckets {
		if err := p.ProcessBucket(ctx, bucket); err != nil {
			// Continue processing remaining buckets.
			continue
		}
	}
	return nil
}

func (p *Processor) ProcessBucket(ctx context.Context, bucket string) error {
	if p == nil || p.store == nil {
		return nil
	}
	cfg, err := p.store.LoadLifecycleConfig(ctx, bucket)
	if err != nil || cfg == nil {
		return err
	}
	objects, err := p.store.ListObjects(ctx, bucket)
	if err != nil {
		return err
	}
	for _, obj := range objects {
		for _, rule := range cfg.Rules {
			if strings.TrimSpace(rule.Status) != "Enabled" {
				continue
			}
			if !matchesFilter(obj, rule.Filter) {
				continue
			}
			if rule.Expiration != nil && shouldExpire(p.nowFn(), obj, rule.Expiration) {
				if err := p.store.DeleteObject(ctx, bucket, obj.Key); err != nil {
					return err
				}
				break
			}
			if rule.Transition != nil && shouldTransition(p.nowFn(), obj, rule.Transition) {
				if err := p.store.TransitionObject(ctx, bucket, obj.Key, rule.Transition.StorageClass); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func matchesFilter(obj ObjectMeta, filter Filter) bool {
	if filter.Prefix != "" && !strings.HasPrefix(obj.Key, filter.Prefix) {
		return false
	}
	if filter.Tag.Key != "" {
		if obj.Tags == nil {
			return false
		}
		if got := obj.Tags[filter.Tag.Key]; got != filter.Tag.Value {
			return false
		}
	}
	return true
}

func shouldExpire(now time.Time, obj ObjectMeta, expiration *Expiration) bool {
	if expiration == nil {
		return false
	}
	objTime := time.Unix(obj.LastModified, 0)
	if expiration.Days > 0 {
		return objTime.Before(now.AddDate(0, 0, -expiration.Days))
	}
	if expiration.Date != "" {
		cutoff, err := time.Parse(time.RFC3339, expiration.Date)
		if err != nil {
			return false
		}
		return objTime.Before(cutoff)
	}
	return false
}

func shouldTransition(now time.Time, obj ObjectMeta, transition *Transition) bool {
	if transition == nil || transition.Days <= 0 {
		return false
	}
	objTime := time.Unix(obj.LastModified, 0)
	return objTime.Before(now.AddDate(0, 0, -transition.Days))
}

func ValidateConfiguration(cfg *LifecycleConfiguration) error {
	if cfg == nil {
		return fmt.Errorf("lifecycle configuration is required")
	}
	for i, rule := range cfg.Rules {
		if strings.TrimSpace(rule.Status) == "" {
			return fmt.Errorf("rule[%d]: status is required", i)
		}
		if rule.Expiration == nil && rule.Transition == nil {
			return fmt.Errorf("rule[%d]: either expiration or transition must be set", i)
		}
	}
	return nil
}
