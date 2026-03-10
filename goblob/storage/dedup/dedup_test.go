package dedup

import (
	"context"
	"errors"
	"testing"
)

type kvMem struct {
	m map[string][]byte
}

func newKVMem() *kvMem {
	return &kvMem{m: map[string][]byte{}}
}

func (kv *kvMem) KvGet(ctx context.Context, key []byte) ([]byte, error) {
	_ = ctx
	v, ok := kv.m[string(key)]
	if !ok {
		return nil, errors.New("not found")
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (kv *kvMem) KvPut(ctx context.Context, key []byte, value []byte) error {
	_ = ctx
	v := make([]byte, len(value))
	copy(v, value)
	kv.m[string(key)] = v
	return nil
}

func (kv *kvMem) KvDelete(ctx context.Context, key []byte) error {
	_ = ctx
	delete(kv.m, string(key))
	return nil
}

func TestDeduplicatorLookupOrCreateAndRefCount(t *testing.T) {
	ctx := context.Background()
	kv := newKVMem()
	d := NewDeduplicator(kv)

	fid, hit, err := d.LookupOrCreate(ctx, []byte("same-data"))
	if err != nil {
		t.Fatalf("LookupOrCreate miss: %v", err)
	}
	if hit || fid != "" {
		t.Fatalf("expected miss, got hit=%v fid=%q", hit, fid)
	}

	if err := d.RecordStored(ctx, "1,abc", []byte("same-data")); err != nil {
		t.Fatalf("RecordStored: %v", err)
	}
	fid, hit, err = d.LookupOrCreate(ctx, []byte("same-data"))
	if err != nil {
		t.Fatalf("LookupOrCreate hit: %v", err)
	}
	if !hit || fid != "1,abc" {
		t.Fatalf("expected hit fid=1,abc got hit=%v fid=%q", hit, fid)
	}

	ref, err := d.GetRefCount(ctx, "1,abc")
	if err != nil {
		t.Fatalf("GetRefCount: %v", err)
	}
	if ref != 2 {
		t.Fatalf("ref=%d want 2", ref)
	}
}

func TestDeduplicatorDecrementCleanup(t *testing.T) {
	ctx := context.Background()
	kv := newKVMem()
	d := NewDeduplicator(kv)
	if err := d.RecordStored(ctx, "1,abc", []byte("payload")); err != nil {
		t.Fatalf("RecordStored: %v", err)
	}

	remaining, err := d.DecrementRefCount(ctx, "1,abc")
	if err != nil {
		t.Fatalf("DecrementRefCount: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining=%d want 0", remaining)
	}
	if _, ok := kv.m[refKey("1,abc")]; ok {
		t.Fatal("expected ref key cleaned up")
	}
}
