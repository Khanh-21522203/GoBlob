package filer

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

// mockKVStore is an in-memory FilerStore for tests.
// Only the KV methods are implemented; all others are no-ops.
type mockKVStore struct {
	mu sync.Mutex
	kv map[string][]byte
}

func newMockKVStore() *mockKVStore {
	return &mockKVStore{kv: make(map[string][]byte)}
}

func (m *mockKVStore) KvPut(_ context.Context, key []byte, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	m.kv[string(key)] = cp
	return nil
}

func (m *mockKVStore) KvGet(_ context.Context, key []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.kv[string(key)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nil
}

func (m *mockKVStore) KvDelete(_ context.Context, key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.kv, string(key))
	return nil
}

// Unused FilerStore methods — satisfy the interface.
func (m *mockKVStore) GetName() string                                    { return "mock" }
func (m *mockKVStore) Initialize(_ map[string]string, _ string) error    { return nil }
func (m *mockKVStore) Shutdown()                                          {}
func (m *mockKVStore) InsertEntry(_ context.Context, _ *Entry) error     { return nil }
func (m *mockKVStore) UpdateEntry(_ context.Context, _ *Entry) error     { return nil }
func (m *mockKVStore) FindEntry(_ context.Context, _ FullPath) (*Entry, error) {
	return nil, ErrNotFound
}
func (m *mockKVStore) DeleteEntry(_ context.Context, _ FullPath) error { return nil }
func (m *mockKVStore) DeleteFolderChildren(_ context.Context, _ FullPath) error { return nil }
func (m *mockKVStore) ListDirectoryEntries(_ context.Context, _ FullPath, _ string, _ bool, _ int64, _ func(*Entry) bool) (string, error) {
	return "", nil
}
func (m *mockKVStore) ListDirectoryPrefixedEntries(_ context.Context, _ FullPath, _ string, _ bool, _ int64, _ string, _ func(*Entry) bool) (string, error) {
	return "", nil
}
func (m *mockKVStore) BeginTransaction(_ context.Context) (context.Context, error) {
	return context.Background(), nil
}
func (m *mockKVStore) CommitTransaction(_ context.Context) error    { return nil }
func (m *mockKVStore) RollbackTransaction(_ context.Context) error  { return nil }

// newTestDLM creates a DistributedLockManager with a fresh mock store.
func newTestDLM() (*DistributedLockManager, *mockKVStore) {
	store := newMockKVStore()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	dlm := NewDistributedLockManager(store, logger)
	return dlm, store
}

func TestTryLockFresh(t *testing.T) {
	dlm, _ := newTestDLM()
	ctx := context.Background()

	result, err := dlm.TryLock(ctx, "mylock", "ownerA", int64(time.Second), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Acquired {
		t.Fatal("expected lock to be acquired")
	}
	if result.RenewToken == "" {
		t.Fatal("expected non-empty RenewToken")
	}
}

func TestTryLockRenewal(t *testing.T) {
	dlm, _ := newTestDLM()
	ctx := context.Background()

	// Acquire initial lock.
	r1, err := dlm.TryLock(ctx, "mylock", "ownerA", int64(time.Second), "")
	if err != nil || !r1.Acquired {
		t.Fatalf("initial lock failed: err=%v acquired=%v", err, r1.Acquired)
	}

	// Renew with the returned token.
	r2, err := dlm.TryLock(ctx, "mylock", "ownerA", int64(time.Second), r1.RenewToken)
	if err != nil {
		t.Fatalf("renewal error: %v", err)
	}
	if !r2.Acquired {
		t.Fatal("expected renewal to succeed")
	}
	if r2.RenewToken == r1.RenewToken {
		t.Fatal("expected a new RenewToken on renewal")
	}
}

func TestTryLockWrongToken(t *testing.T) {
	dlm, _ := newTestDLM()
	ctx := context.Background()

	_, err := dlm.TryLock(ctx, "mylock", "ownerA", int64(time.Second), "")
	if err != nil {
		t.Fatalf("initial lock failed: %v", err)
	}

	// Try to renew with wrong token — owner matches but token is wrong.
	_, err = dlm.TryLock(ctx, "mylock", "ownerA", int64(time.Second), "wrongtoken")
	if err == nil {
		t.Fatal("expected ErrNotLockOwner, got nil")
	}
	if err != ErrNotLockOwner {
		t.Fatalf("expected ErrNotLockOwner, got: %v", err)
	}
}

func TestTryLockContention(t *testing.T) {
	dlm, _ := newTestDLM()
	ctx := context.Background()

	// Owner A acquires the lock.
	_, err := dlm.TryLock(ctx, "mylock", "ownerA", int64(time.Second), "")
	if err != nil {
		t.Fatalf("ownerA lock failed: %v", err)
	}

	// Owner B tries to acquire — should fail with Acquired=false, CurrentOwner=ownerA.
	rB, err := dlm.TryLock(ctx, "mylock", "ownerB", int64(time.Second), "")
	if err != nil {
		t.Fatalf("unexpected error for ownerB: %v", err)
	}
	if rB.Acquired {
		t.Fatal("ownerB should not acquire the lock held by ownerA")
	}
	if rB.CurrentOwner != "ownerA" {
		t.Fatalf("expected CurrentOwner=ownerA, got %q", rB.CurrentOwner)
	}
}

func TestTryLockExpiry(t *testing.T) {
	dlm, _ := newTestDLM()
	ctx := context.Background()

	// Lock with 1 nanosecond expiry — effectively already expired by the time we check.
	_, err := dlm.TryLock(ctx, "mylock", "ownerA", 1, "")
	if err != nil {
		t.Fatalf("initial lock failed: %v", err)
	}

	// Sleep to ensure expiry.
	time.Sleep(10 * time.Millisecond)

	// A different owner should now be able to acquire.
	r2, err := dlm.TryLock(ctx, "mylock", "ownerB", int64(time.Second), "")
	if err != nil {
		t.Fatalf("post-expiry lock failed: %v", err)
	}
	if !r2.Acquired {
		t.Fatal("expected lock to be acquired after expiry")
	}
}

func TestUnlockSuccess(t *testing.T) {
	dlm, _ := newTestDLM()
	ctx := context.Background()

	r, err := dlm.TryLock(ctx, "mylock", "ownerA", int64(time.Second), "")
	if err != nil || !r.Acquired {
		t.Fatalf("lock failed: err=%v acquired=%v", err, r.Acquired)
	}

	if err := dlm.Unlock(ctx, "mylock", r.RenewToken); err != nil {
		t.Fatalf("unlock failed: %v", err)
	}

	locked, _, err := dlm.IsLocked(ctx, "mylock")
	if err != nil {
		t.Fatalf("IsLocked error: %v", err)
	}
	if locked {
		t.Fatal("expected lock to be released")
	}
}

func TestUnlockWrongToken(t *testing.T) {
	dlm, _ := newTestDLM()
	ctx := context.Background()

	_, err := dlm.TryLock(ctx, "mylock", "ownerA", int64(time.Second), "")
	if err != nil {
		t.Fatalf("lock failed: %v", err)
	}

	err = dlm.Unlock(ctx, "mylock", "wrongtoken")
	if err != ErrNotLockOwner {
		t.Fatalf("expected ErrNotLockOwner, got: %v", err)
	}
}

func TestUnlockNotFound(t *testing.T) {
	dlm, _ := newTestDLM()
	ctx := context.Background()

	err := dlm.Unlock(ctx, "nonexistent", "anytoken")
	if err != ErrLockNotFound {
		t.Fatalf("expected ErrLockNotFound, got: %v", err)
	}
}
