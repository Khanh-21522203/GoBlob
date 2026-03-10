package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeStore struct {
	cfg          *LifecycleConfiguration
	objects      []ObjectMeta
	deleted      []string
	transitioned map[string]string
}

func (s *fakeStore) ListBuckets(ctx context.Context) ([]string, error) {
	_ = ctx
	return []string{"b"}, nil
}

func (s *fakeStore) LoadLifecycleConfig(ctx context.Context, bucket string) (*LifecycleConfiguration, error) {
	_ = ctx
	_ = bucket
	if s.cfg == nil {
		return nil, errors.New("not found")
	}
	return s.cfg, nil
}

func (s *fakeStore) ListObjects(ctx context.Context, bucket string) ([]ObjectMeta, error) {
	_ = ctx
	_ = bucket
	return append([]ObjectMeta(nil), s.objects...), nil
}

func (s *fakeStore) DeleteObject(ctx context.Context, bucket, key string) error {
	_ = ctx
	_ = bucket
	s.deleted = append(s.deleted, key)
	return nil
}

func (s *fakeStore) TransitionObject(ctx context.Context, bucket, key, storageClass string) error {
	_ = ctx
	_ = bucket
	if s.transitioned == nil {
		s.transitioned = map[string]string{}
	}
	s.transitioned[key] = storageClass
	return nil
}

func TestProcessorProcessBucket(t *testing.T) {
	now := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	store := &fakeStore{
		cfg: &LifecycleConfiguration{Rules: []Rule{
			{
				ID:     "expire-logs",
				Status: "Enabled",
				Filter: Filter{Prefix: "logs/"},
				Expiration: &Expiration{
					Days: 30,
				},
			},
			{
				ID:     "tier-data",
				Status: "Enabled",
				Filter: Filter{Prefix: "data/"},
				Transition: &Transition{
					Days:         90,
					StorageClass: "STANDARD_IA",
				},
			},
		}},
		objects: []ObjectMeta{
			{Key: "logs/old.log", LastModified: now.AddDate(0, 0, -40).Unix()},
			{Key: "data/archive.bin", LastModified: now.AddDate(0, 0, -120).Unix()},
			{Key: "data/new.bin", LastModified: now.AddDate(0, 0, -10).Unix()},
		},
	}

	p := NewProcessor(store)
	p.nowFn = func() time.Time { return now }
	if err := p.ProcessBucket(context.Background(), "b"); err != nil {
		t.Fatalf("ProcessBucket: %v", err)
	}

	if len(store.deleted) != 1 || store.deleted[0] != "logs/old.log" {
		t.Fatalf("deleted=%v want [logs/old.log]", store.deleted)
	}
	if got := store.transitioned["data/archive.bin"]; got != "STANDARD_IA" {
		t.Fatalf("transition storage class=%q want STANDARD_IA", got)
	}
	if _, ok := store.transitioned["data/new.bin"]; ok {
		t.Fatal("did not expect transition for data/new.bin")
	}
}

func TestValidateConfiguration(t *testing.T) {
	if err := ValidateConfiguration(nil); err == nil {
		t.Fatal("expected nil config error")
	}
	if err := ValidateConfiguration(&LifecycleConfiguration{Rules: []Rule{{Status: "Enabled"}}}); err == nil {
		t.Fatal("expected missing action error")
	}
	if err := ValidateConfiguration(&LifecycleConfiguration{Rules: []Rule{{Status: "Enabled", Expiration: &Expiration{Days: 1}}}}); err != nil {
		t.Fatalf("unexpected validate error: %v", err)
	}
}
