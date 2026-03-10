package filer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Sentinel errors for lock operations.
var (
	ErrLockHeldByOther = errors.New("lock held by another owner")
	ErrNotLockOwner    = errors.New("renewToken mismatch")
	ErrLockNotFound    = errors.New("lock not found")
)

// lockEntry is stored in the KV store for each lock.
type lockEntry struct {
	Owner      string `json:"owner"`
	ExpireAtNs int64  `json:"expireAtNs"` // Unix nanoseconds
	RenewToken string `json:"renewToken"`
}

// LockResult is returned by TryLock.
type LockResult struct {
	Acquired     bool
	RenewToken   string
	CurrentOwner string
}

// DistributedLockManager implements named, expiring distributed locks
// backed by a FilerStore KV store.
type DistributedLockManager struct {
	filerStore FilerStore
	mu         sync.Mutex
	logger     *slog.Logger
}

// NewDistributedLockManager creates a new DistributedLockManager.
func NewDistributedLockManager(store FilerStore, logger *slog.Logger) *DistributedLockManager {
	return &DistributedLockManager{
		filerStore: store,
		logger:     logger,
	}
}

// lockKey returns the KV key for a lock name.
// Format: "__lock__:<lockName>"
func lockKey(name string) []byte {
	return []byte("__lock__:" + name)
}

// generateToken generates a cryptographically random hex token (64 chars).
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// TryLock attempts to acquire or renew a named lock.
// expireNs: lock duration in nanoseconds
// renewToken: if non-empty, this is a renewal attempt
func (dlm *DistributedLockManager) TryLock(ctx context.Context, name, owner string, expireNs int64, renewToken string) (LockResult, error) {
	dlm.mu.Lock()
	defer dlm.mu.Unlock()

	key := lockKey(name)
	now := time.Now().UnixNano()

	data, err := dlm.filerStore.KvGet(ctx, key)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return LockResult{}, fmt.Errorf("KvGet: %w", err)
	}

	if data != nil && !errors.Is(err, ErrNotFound) {
		// Parse existing entry.
		var entry lockEntry
		if jsonErr := json.Unmarshal(data, &entry); jsonErr != nil {
			return LockResult{}, fmt.Errorf("json.Unmarshal: %w", jsonErr)
		}

		if entry.ExpireAtNs > now {
			// Lock is still valid.
			if entry.Owner != owner && entry.RenewToken != renewToken {
				// Different owner and token doesn't match — lock held by other.
				return LockResult{Acquired: false, CurrentOwner: entry.Owner}, nil
			}
			if renewToken != "" && entry.RenewToken != renewToken {
				// Renewal attempt with wrong token.
				return LockResult{}, ErrNotLockOwner
			}
		}
		// Otherwise: lock expired, or same owner, or valid renewal — fall through to acquire.
	}

	// Generate new token.
	newToken, err := generateToken()
	if err != nil {
		return LockResult{}, err
	}

	newEntry := lockEntry{
		Owner:      owner,
		ExpireAtNs: now + expireNs,
		RenewToken: newToken,
	}
	encoded, err := json.Marshal(newEntry)
	if err != nil {
		return LockResult{}, fmt.Errorf("json.Marshal: %w", err)
	}
	if err := dlm.filerStore.KvPut(ctx, key, encoded); err != nil {
		return LockResult{}, fmt.Errorf("KvPut: %w", err)
	}

	dlm.logger.Debug("lock acquired", "name", name, "owner", owner)
	return LockResult{Acquired: true, RenewToken: newToken}, nil
}

// Unlock releases a lock, verifying the renew token.
func (dlm *DistributedLockManager) Unlock(ctx context.Context, name string, renewToken string) error {
	dlm.mu.Lock()
	defer dlm.mu.Unlock()

	key := lockKey(name)

	data, err := dlm.filerStore.KvGet(ctx, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrLockNotFound
		}
		return fmt.Errorf("KvGet: %w", err)
	}
	if data == nil {
		return ErrLockNotFound
	}

	var entry lockEntry
	if jsonErr := json.Unmarshal(data, &entry); jsonErr != nil {
		return fmt.Errorf("json.Unmarshal: %w", jsonErr)
	}

	if entry.RenewToken != renewToken {
		return ErrNotLockOwner
	}

	if err := dlm.filerStore.KvDelete(ctx, key); err != nil {
		return fmt.Errorf("KvDelete: %w", err)
	}

	dlm.logger.Debug("lock released", "name", name)
	return nil
}

// IsLocked returns whether the lock is currently held and by whom.
func (dlm *DistributedLockManager) IsLocked(ctx context.Context, name string) (bool, string, error) {
	dlm.mu.Lock()
	defer dlm.mu.Unlock()

	key := lockKey(name)

	data, err := dlm.filerStore.KvGet(ctx, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("KvGet: %w", err)
	}
	if data == nil {
		return false, "", nil
	}

	var entry lockEntry
	if jsonErr := json.Unmarshal(data, &entry); jsonErr != nil {
		return false, "", fmt.Errorf("json.Unmarshal: %w", jsonErr)
	}

	if entry.ExpireAtNs < time.Now().UnixNano() {
		return false, "", nil
	}

	return true, entry.Owner, nil
}

// CleanExpiredLocks is a no-op for backends that don't support prefix scanning.
// The caller is responsible for scheduling this periodically if needed.
func (dlm *DistributedLockManager) CleanExpiredLocks(_ context.Context) error {
	// LevelDB2 backend does not support efficient KV prefix scanning.
	// Callers may implement their own cleanup strategy if needed.
	return nil
}
