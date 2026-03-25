package filer

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"GoBlob/goblob/operation"
)

// Filer provides filesystem-like metadata operations on volume storage.
type Filer struct {
	UniqueFilerId      int32
	Store              FilerStore
	MasterAddresses    []string
	DirBucketsPath     string
	LocalMetaLogBuffer *LogBufferProxy
	Dlm                *DistributedLockManager
	FilerConf          *FilerConf
	// Uploader handles chunked file uploads to volume storage.
	// Injected at construction so callers can substitute a test double.
	Uploader Uploader
	// Resolver maps volume IDs to HTTP URLs for data retrieval.
	// Injected at construction so callers can substitute a test double.
	Resolver  Resolver
	ctx       context.Context
	logger    *slog.Logger
	masterIdx atomic.Uint64 // round-robin index for master selection
}

// LogBufferProxy is a placeholder for the log_buffer.LogBuffer.
// This avoids circular import between filer and log_buffer packages.
type LogBufferProxy struct{}

// NewFiler creates a new Filer instance.
func NewFiler(store FilerStore, masterAddrs []string, logger *slog.Logger) *Filer {
	return &Filer{
		UniqueFilerId:   generateUniqueFilerId(),
		Store:           store,
		MasterAddresses: masterAddrs,
		DirBucketsPath:  "/buckets",
		FilerConf:       NewFilerConf(),
		ctx:             context.Background(),
		logger:          logger,
	}
}

// SetStore sets the filer store.
func (f *Filer) SetStore(store FilerStore) {
	f.Store = store
}

// MkdirAll ensures all ancestor directories of dirPath exist, creating any that are missing.
// dirPath must be an absolute path (e.g. "/bench" or "/a/b/c").
func (f *Filer) MkdirAll(ctx context.Context, dirPath FullPath) error {
	if f.Store == nil {
		return ErrStoreNotConfigured
	}
	if dirPath == "/" || dirPath == "" {
		return nil
	}
	parts := strings.Split(strings.Trim(string(dirPath), "/"), "/")
	cur := FullPath("/")
	for _, part := range parts {
		if part == "" {
			continue
		}
		var child FullPath
		if cur == "/" {
			child = FullPath("/" + part)
		} else {
			child = FullPath(string(cur) + "/" + part)
		}
		_, err := f.Store.FindEntry(ctx, child)
		if err == nil {
			cur = child
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		// Create the missing directory.
		dirEntry := &Entry{
			FullPath: child,
			Attr: Attr{
				Mode:  os.ModeDir | 0755,
				Mtime: time.Now(),
			},
		}
		if err := f.Store.InsertEntry(ctx, dirEntry); err != nil {
			return err
		}
		cur = child
	}
	return nil
}

// CreateEntry creates a new file or directory entry.
func (f *Filer) CreateEntry(ctx context.Context, entry *Entry) error {
	if f.Store == nil {
		return ErrStoreNotConfigured
	}

	// Check if parent directory exists
	_, _ = entry.FullPath.DirAndName() // parse but don't use
	dir, _ := entry.FullPath.DirAndName()
	if dir != "/" {
		_, err := f.Store.FindEntry(ctx, dir)
		if err != nil {
			return ErrParentNotFound
		}
		// Note: checking IsDirectory would require reading the entry first
		// For Phase 4, we skip the IsDirectory check
	}

	// Insert entry
	return f.Store.InsertEntry(ctx, entry)
}

// UpdateEntry updates an existing entry.
func (f *Filer) UpdateEntry(ctx context.Context, entry *Entry) error {
	if f.Store == nil {
		return ErrStoreNotConfigured
	}

	return f.Store.UpdateEntry(ctx, entry)
}

// FindEntry finds an entry by full path.
func (f *Filer) FindEntry(ctx context.Context, fp FullPath) (*Entry, error) {
	if f.Store == nil {
		return nil, ErrStoreNotConfigured
	}

	return f.Store.FindEntry(ctx, fp)
}

// DeleteEntry deletes an entry by full path.
func (f *Filer) DeleteEntry(ctx context.Context, fp FullPath) error {
	if f.Store == nil {
		return ErrStoreNotConfigured
	}

	return f.Store.DeleteEntry(ctx, fp)
}

// ListDirectoryEntries lists entries in a directory.
func (f *Filer) ListDirectoryEntries(ctx context.Context, dirPath FullPath, startFileName string, limit int64) ([]*Entry, string, error) {
	if f.Store == nil {
		return nil, "", ErrStoreNotConfigured
	}

	var entries []*Entry
	var lastFileName string

	_, err := f.Store.ListDirectoryEntries(ctx, dirPath, startFileName, false, limit, func(entry *Entry) bool {
		entries = append(entries, entry)
		return true
	})

	if err != nil {
		return entries, "", err
	}

	// Get last file name for pagination
	if len(entries) > 0 {
		_, lastFileName = entries[len(entries)-1].FullPath.DirAndName()
	}

	return entries, lastFileName, nil
}

// AssignVolume assigns a file ID from the master server.
func (f *Filer) AssignVolume(ctx context.Context, collection, replication, ttl, dataCenter, rack string) (*operation.AssignedFileId, error) {
	if len(f.MasterAddresses) == 0 {
		return nil, ErrNoMasterConfigured
	}

	masterAddr := f.MasterAddresses[f.masterIdx.Add(1)%uint64(len(f.MasterAddresses))]

	opt := &operation.AssignOption{
		Collection:  collection,
		Replication: replication,
		Ttl:         ttl,
		Count:       1,
		DataCenter:  dataCenter,
		Rack:        rack,
	}

	return operation.Assign(ctx, masterAddr, opt)
}

// generateUniqueFilerId generates a unique filer ID.
func generateUniqueFilerId() int32 {
	return int32(time.Now().Unix())
}

// Error definitions
var (
	ErrStoreNotConfigured = &filerError{"store not configured"}
	ErrParentNotFound     = &filerError{"parent directory not found"}
	ErrParentNotDirectory = &filerError{"parent is not a directory"}
	ErrNoMasterConfigured = &filerError{"no master addresses configured"}
)

type filerError struct {
	msg string
}

func (e *filerError) Error() string {
	return e.msg
}
