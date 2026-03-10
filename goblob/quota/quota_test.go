package quota

import (
	"context"
	"errors"
	"testing"
)

type kvStore struct {
	m map[string][]byte
}

func newKVStore() *kvStore { return &kvStore{m: map[string][]byte{}} }

func (kv *kvStore) KvGet(ctx context.Context, key []byte) ([]byte, error) {
	_ = ctx
	v, ok := kv.m[string(key)]
	if !ok {
		return nil, errors.New("not found")
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (kv *kvStore) KvPut(ctx context.Context, key []byte, value []byte) error {
	_ = ctx
	v := make([]byte, len(value))
	copy(v, value)
	kv.m[string(key)] = v
	return nil
}

func TestQuotaCheckAndUsage(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newKVStore())

	if err := m.SetUserQuota(ctx, "alice", &Quota{MaxBytes: 100, UsedBytes: 80}); err != nil {
		t.Fatalf("SetUserQuota: %v", err)
	}
	if err := m.CheckUserQuota(ctx, "alice", 30); err == nil {
		t.Fatal("expected user quota exceeded error")
	}
	if err := m.CheckUserQuota(ctx, "alice", 20); err != nil {
		t.Fatalf("unexpected check error: %v", err)
	}

	if err := m.AddUserUsage(ctx, "alice", 10); err != nil {
		t.Fatalf("AddUserUsage: %v", err)
	}
	q, err := m.GetUserQuota(ctx, "alice")
	if err != nil {
		t.Fatalf("GetUserQuota: %v", err)
	}
	if q.UsedBytes != 90 {
		t.Fatalf("UsedBytes=%d want 90", q.UsedBytes)
	}
	if err := m.SubtractUserUsage(ctx, "alice", 200); err != nil {
		t.Fatalf("SubtractUserUsage: %v", err)
	}
	q, err = m.GetUserQuota(ctx, "alice")
	if err != nil {
		t.Fatalf("GetUserQuota 2: %v", err)
	}
	if q.UsedBytes != 0 {
		t.Fatalf("UsedBytes=%d want 0", q.UsedBytes)
	}
}

func TestBucketQuota(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newKVStore())

	if err := m.SetBucketQuota(ctx, "photos", &Quota{MaxBytes: 1000, UsedBytes: 100}); err != nil {
		t.Fatalf("SetBucketQuota: %v", err)
	}
	if err := m.CheckBucketQuota(ctx, "photos", 950); err == nil {
		t.Fatal("expected bucket quota exceeded")
	}
	if err := m.AddBucketUsage(ctx, "photos", 300); err != nil {
		t.Fatalf("AddBucketUsage: %v", err)
	}
	q, err := m.GetBucketQuota(ctx, "photos")
	if err != nil {
		t.Fatalf("GetBucketQuota: %v", err)
	}
	if q.UsedBytes != 400 {
		t.Fatalf("UsedBytes=%d want 400", q.UsedBytes)
	}
}
