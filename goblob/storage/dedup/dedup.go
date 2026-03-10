package dedup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// KVStore is the minimum filer KV contract required for dedup metadata.
type KVStore interface {
	KvGet(ctx context.Context, key []byte) ([]byte, error)
	KvPut(ctx context.Context, key []byte, value []byte) error
	KvDelete(ctx context.Context, key []byte) error
}

// Deduplicator tracks content hash -> fid and per-fid reference counts.
type Deduplicator struct {
	kv KVStore
	mu sync.Mutex
}

func NewDeduplicator(kv KVStore) *Deduplicator {
	return &Deduplicator{kv: kv}
}

func (d *Deduplicator) ComputeHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// LookupOrCreate returns existing fid if the data hash already exists.
// hit=true means a dedup hit happened and refcount has been incremented.
func (d *Deduplicator) LookupOrCreate(ctx context.Context, data []byte) (fid string, hit bool, err error) {
	if d == nil || d.kv == nil {
		return "", false, errors.New("deduplicator store is not configured")
	}
	hash := d.ComputeHash(data)

	d.mu.Lock()
	defer d.mu.Unlock()

	existingFid, err := d.kv.KvGet(ctx, []byte(hashKey(hash)))
	if err != nil {
		if isNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	fid = string(existingFid)
	if fid == "" {
		return "", false, nil
	}
	if err := d.incrementRefCountLocked(ctx, fid); err != nil {
		return "", false, err
	}
	return fid, true, nil
}

// RecordStored creates hash and refcount metadata after a dedup miss write.
func (d *Deduplicator) RecordStored(ctx context.Context, fid string, data []byte) error {
	if d == nil || d.kv == nil {
		return errors.New("deduplicator store is not configured")
	}
	if strings.TrimSpace(fid) == "" {
		return fmt.Errorf("fid is required")
	}
	hash := d.ComputeHash(data)

	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.kv.KvPut(ctx, []byte(hashKey(hash)), []byte(fid)); err != nil {
		return err
	}
	if err := d.kv.KvPut(ctx, []byte(refKey(fid)), []byte("1")); err != nil {
		return err
	}
	if err := d.kv.KvPut(ctx, []byte(reverseKey(fid)), []byte(hash)); err != nil {
		return err
	}
	return nil
}

func (d *Deduplicator) incrementRefCountLocked(ctx context.Context, fid string) error {
	ref, err := d.currentRefCountLocked(ctx, fid)
	if err != nil {
		if isNotFound(err) {
			ref = 0
		} else {
			return err
		}
	}
	ref++
	return d.kv.KvPut(ctx, []byte(refKey(fid)), []byte(strconv.Itoa(ref)))
}

func (d *Deduplicator) currentRefCountLocked(ctx context.Context, fid string) (int, error) {
	raw, err := d.kv.KvGet(ctx, []byte(refKey(fid)))
	if err != nil {
		return 0, err
	}
	if len(raw) == 0 {
		return 0, nil
	}
	count, convErr := strconv.Atoi(string(raw))
	if convErr != nil {
		return 0, convErr
	}
	if count < 0 {
		count = 0
	}
	return count, nil
}

// GetRefCount returns the current reference count for fid.
func (d *Deduplicator) GetRefCount(ctx context.Context, fid string) (int, error) {
	if d == nil || d.kv == nil {
		return 0, errors.New("deduplicator store is not configured")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	count, err := d.currentRefCountLocked(ctx, fid)
	if err != nil {
		if isNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	return count, nil
}

// DecrementRefCount decrements refcount and cleans hash metadata at zero.
func (d *Deduplicator) DecrementRefCount(ctx context.Context, fid string) (int, error) {
	if d == nil || d.kv == nil {
		return 0, errors.New("deduplicator store is not configured")
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	count, err := d.currentRefCountLocked(ctx, fid)
	if err != nil {
		if isNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	if count <= 1 {
		if err := d.kv.KvDelete(ctx, []byte(refKey(fid))); err != nil && !isNotFound(err) {
			return 0, err
		}
		hashRaw, hashErr := d.kv.KvGet(ctx, []byte(reverseKey(fid)))
		if hashErr == nil {
			_ = d.kv.KvDelete(ctx, []byte(hashKey(string(hashRaw))))
			_ = d.kv.KvDelete(ctx, []byte(reverseKey(fid)))
		}
		return 0, nil
	}
	count--
	if err := d.kv.KvPut(ctx, []byte(refKey(fid)), []byte(strconv.Itoa(count))); err != nil {
		return 0, err
	}
	return count, nil
}

func hashKey(hash string) string {
	return "dedup:" + hash
}

func refKey(fid string) string {
	return "dedup_ref:" + fid
}

func reverseKey(fid string) string {
	return "dedup_rev:" + fid
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}
