package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// KVStore defines filer KV access methods used by quota manager.
type KVStore interface {
	KvGet(ctx context.Context, key []byte) ([]byte, error)
	KvPut(ctx context.Context, key []byte, value []byte) error
}

// Quota stores configured and consumed bytes.
type Quota struct {
	MaxBytes  int64 `json:"max_bytes"`
	UsedBytes int64 `json:"used_bytes"`
}

// QuotaExceededError is returned when adding required bytes exceeds max.
type QuotaExceededError struct {
	Scope      string
	Identifier string
	MaxBytes   int64
	UsedBytes  int64
	Required   int64
}

func (e *QuotaExceededError) Error() string {
	if e == nil {
		return "quota exceeded"
	}
	return fmt.Sprintf("quota exceeded for %s %q: max=%d used=%d required=%d", e.Scope, e.Identifier, e.MaxBytes, e.UsedBytes, e.Required)
}

// Manager persists per-user and per-bucket quotas in filer KV.
type Manager struct {
	kv KVStore
}

func NewManager(kv KVStore) *Manager {
	return &Manager{kv: kv}
}

func (m *Manager) CheckUserQuota(ctx context.Context, userID string, requiredBytes int64) error {
	if strings.TrimSpace(userID) == "" || requiredBytes <= 0 {
		return nil
	}
	q, err := m.GetUserQuota(ctx, userID)
	if err != nil {
		// Fail open if quota cannot be loaded.
		return nil
	}
	if q.MaxBytes > 0 && q.UsedBytes+requiredBytes > q.MaxBytes {
		return &QuotaExceededError{Scope: "user", Identifier: userID, MaxBytes: q.MaxBytes, UsedBytes: q.UsedBytes, Required: requiredBytes}
	}
	return nil
}

func (m *Manager) CheckBucketQuota(ctx context.Context, bucket string, requiredBytes int64) error {
	if strings.TrimSpace(bucket) == "" || requiredBytes <= 0 {
		return nil
	}
	q, err := m.GetBucketQuota(ctx, bucket)
	if err != nil {
		// Fail open if quota cannot be loaded.
		return nil
	}
	if q.MaxBytes > 0 && q.UsedBytes+requiredBytes > q.MaxBytes {
		return &QuotaExceededError{Scope: "bucket", Identifier: bucket, MaxBytes: q.MaxBytes, UsedBytes: q.UsedBytes, Required: requiredBytes}
	}
	return nil
}

func (m *Manager) AddUserUsage(ctx context.Context, userID string, bytes int64) error {
	if strings.TrimSpace(userID) == "" || bytes == 0 {
		return nil
	}
	q, err := m.GetUserQuota(ctx, userID)
	if err != nil {
		return err
	}
	q.UsedBytes += bytes
	if q.UsedBytes < 0 {
		q.UsedBytes = 0
	}
	return m.SetUserQuota(ctx, userID, q)
}

func (m *Manager) SubtractUserUsage(ctx context.Context, userID string, bytes int64) error {
	return m.AddUserUsage(ctx, userID, -bytes)
}

func (m *Manager) AddBucketUsage(ctx context.Context, bucket string, bytes int64) error {
	if strings.TrimSpace(bucket) == "" || bytes == 0 {
		return nil
	}
	q, err := m.GetBucketQuota(ctx, bucket)
	if err != nil {
		return err
	}
	q.UsedBytes += bytes
	if q.UsedBytes < 0 {
		q.UsedBytes = 0
	}
	return m.SetBucketQuota(ctx, bucket, q)
}

func (m *Manager) SubtractBucketUsage(ctx context.Context, bucket string, bytes int64) error {
	return m.AddBucketUsage(ctx, bucket, -bytes)
}

func (m *Manager) GetUserQuota(ctx context.Context, userID string) (*Quota, error) {
	return m.getQuota(ctx, userKey(userID))
}

func (m *Manager) SetUserQuota(ctx context.Context, userID string, q *Quota) error {
	return m.setQuota(ctx, userKey(userID), q)
}

func (m *Manager) GetBucketQuota(ctx context.Context, bucket string) (*Quota, error) {
	return m.getQuota(ctx, bucketKey(bucket))
}

func (m *Manager) SetBucketQuota(ctx context.Context, bucket string, q *Quota) error {
	return m.setQuota(ctx, bucketKey(bucket), q)
}

func (m *Manager) getQuota(ctx context.Context, key string) (*Quota, error) {
	if m == nil || m.kv == nil {
		return nil, errors.New("quota manager store is not configured")
	}
	raw, err := m.kv.KvGet(ctx, []byte(key))
	if err != nil {
		if isNotFound(err) {
			return &Quota{}, nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return &Quota{}, nil
	}
	q := &Quota{}
	if err := json.Unmarshal(raw, q); err != nil {
		return nil, err
	}
	if q.UsedBytes < 0 {
		q.UsedBytes = 0
	}
	return q, nil
}

func (m *Manager) setQuota(ctx context.Context, key string, q *Quota) error {
	if m == nil || m.kv == nil {
		return errors.New("quota manager store is not configured")
	}
	if q == nil {
		q = &Quota{}
	}
	if q.UsedBytes < 0 {
		q.UsedBytes = 0
	}
	payload, err := json.Marshal(q)
	if err != nil {
		return err
	}
	return m.kv.KvPut(ctx, []byte(key), payload)
}

func userKey(userID string) string {
	return "quota:user:" + strings.TrimSpace(userID)
}

func bucketKey(bucket string) string {
	return "quota:bucket:" + strings.TrimSpace(bucket)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}
