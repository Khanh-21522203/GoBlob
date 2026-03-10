package filer

import (
	"context"
	"errors"
)

// ErrNotFound is returned when an entry is not found.
var ErrNotFound = errors.New("entry not found")

// FilerStore is the interface for filer metadata persistence.
type FilerStore interface {
	GetName() string
	Initialize(config map[string]string, prefix string) error
	Shutdown()

	InsertEntry(ctx context.Context, entry *Entry) error
	UpdateEntry(ctx context.Context, entry *Entry) error
	FindEntry(ctx context.Context, fp FullPath) (*Entry, error)
	DeleteEntry(ctx context.Context, fp FullPath) error
	DeleteFolderChildren(ctx context.Context, fp FullPath) error

	// ListDirectoryEntries lists entries in a directory.
	// startFileName: pagination cursor (empty = start from beginning)
	// includeStart: include the startFileName entry itself
	// limit: max entries to return
	// fn: callback for each entry; return false to stop
	// Returns the last file name (for pagination cursor), error
	ListDirectoryEntries(ctx context.Context, dirPath FullPath, startFileName string, includeStart bool, limit int64, fn func(*Entry) bool) (string, error)

	ListDirectoryPrefixedEntries(ctx context.Context, dirPath FullPath, startFileName string, includeStart bool, limit int64, prefix string, fn func(*Entry) bool) (string, error)

	BeginTransaction(ctx context.Context) (context.Context, error)
	CommitTransaction(ctx context.Context) error
	RollbackTransaction(ctx context.Context) error

	KvPut(ctx context.Context, key []byte, value []byte) error
	KvGet(ctx context.Context, key []byte) ([]byte, error)
	KvDelete(ctx context.Context, key []byte) error
}
