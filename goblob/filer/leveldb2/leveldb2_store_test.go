package leveldb2

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"GoBlob/goblob/filer"
)

func newTestStore(t *testing.T) *LevelDB2Store {
	t.Helper()
	dir := t.TempDir()
	store, err := NewLevelDB2Store(dir)
	if err != nil {
		t.Fatalf("NewLevelDB2Store: %v", err)
	}
	t.Cleanup(func() { store.Shutdown() })
	return store
}

func makeEntry(fp filer.FullPath) *filer.Entry {
	return &filer.Entry{
		FullPath: fp,
		Attr: filer.Attr{
			Mode:  os.ModeDir | 0755,
			Mtime: time.Now().Truncate(time.Second),
		},
	}
}

func TestInsertFindDelete(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	entry := makeEntry("/photos/vacation.jpg")
	entry.Attr.Mode = 0644 // regular file

	if err := store.InsertEntry(ctx, entry); err != nil {
		t.Fatalf("InsertEntry: %v", err)
	}

	got, err := store.FindEntry(ctx, "/photos/vacation.jpg")
	if err != nil {
		t.Fatalf("FindEntry: %v", err)
	}
	if got.FullPath != entry.FullPath {
		t.Errorf("FindEntry: got FullPath %q, want %q", got.FullPath, entry.FullPath)
	}

	if err := store.DeleteEntry(ctx, "/photos/vacation.jpg"); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}

	_, err = store.FindEntry(ctx, "/photos/vacation.jpg")
	if !errors.Is(err, filer.ErrNotFound) {
		t.Errorf("FindEntry after delete: got %v, want ErrNotFound", err)
	}
}

func TestUpdateEntry(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	entry := makeEntry("/docs/readme.txt")
	entry.Attr.Mode = 0644

	if err := store.InsertEntry(ctx, entry); err != nil {
		t.Fatalf("InsertEntry: %v", err)
	}

	// Update with a new mime type.
	entry.Attr.Mime = "text/plain"
	if err := store.UpdateEntry(ctx, entry); err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}

	got, err := store.FindEntry(ctx, "/docs/readme.txt")
	if err != nil {
		t.Fatalf("FindEntry: %v", err)
	}
	if got.Attr.Mime != "text/plain" {
		t.Errorf("UpdateEntry: got Mime %q, want %q", got.Attr.Mime, "text/plain")
	}
}

func TestListDirectoryEntries(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	entries := []filer.FullPath{
		"/album/a.jpg",
		"/album/b.jpg",
		"/album/c.jpg",
	}
	for _, fp := range entries {
		e := makeEntry(fp)
		e.Attr.Mode = 0644
		if err := store.InsertEntry(ctx, e); err != nil {
			t.Fatalf("InsertEntry(%s): %v", fp, err)
		}
	}

	var listed []string
	_, err := store.ListDirectoryEntries(ctx, "/album", "", true, 100, func(e *filer.Entry) bool {
		listed = append(listed, string(e.FullPath))
		return true
	})
	if err != nil {
		t.Fatalf("ListDirectoryEntries: %v", err)
	}
	if len(listed) != 3 {
		t.Errorf("ListDirectoryEntries: got %d entries, want 3; entries: %v", len(listed), listed)
	}
}

func TestListPaginationCursor(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	files := []filer.FullPath{
		"/data/file1.dat",
		"/data/file2.dat",
		"/data/file3.dat",
	}
	for _, fp := range files {
		e := makeEntry(fp)
		e.Attr.Mode = 0644
		if err := store.InsertEntry(ctx, e); err != nil {
			t.Fatalf("InsertEntry(%s): %v", fp, err)
		}
	}

	// First page: limit=2.
	var page1 []string
	cursor, err := store.ListDirectoryEntries(ctx, "/data", "", true, 2, func(e *filer.Entry) bool {
		page1 = append(page1, string(e.FullPath))
		return true
	})
	if err != nil {
		t.Fatalf("ListDirectoryEntries page1: %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("page1: got %d entries, want 2", len(page1))
	}

	// Second page: start after cursor, not including it.
	var page2 []string
	_, err = store.ListDirectoryEntries(ctx, "/data", cursor, false, 10, func(e *filer.Entry) bool {
		page2 = append(page2, string(e.FullPath))
		return true
	})
	if err != nil {
		t.Fatalf("ListDirectoryEntries page2: %v", err)
	}
	if len(page2) != 1 {
		t.Errorf("page2: got %d entries, want 1; page2=%v cursor=%q", len(page2), page2, cursor)
	}
}

func TestDeleteFolderChildren(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	children := []filer.FullPath{
		"/trash/a.txt",
		"/trash/b.txt",
		"/trash/c.txt",
	}
	for _, fp := range children {
		e := makeEntry(fp)
		e.Attr.Mode = 0644
		if err := store.InsertEntry(ctx, e); err != nil {
			t.Fatalf("InsertEntry(%s): %v", fp, err)
		}
	}

	if err := store.DeleteFolderChildren(ctx, "/trash"); err != nil {
		t.Fatalf("DeleteFolderChildren: %v", err)
	}

	for _, fp := range children {
		_, err := store.FindEntry(ctx, fp)
		if !errors.Is(err, filer.ErrNotFound) {
			t.Errorf("FindEntry(%s) after delete: got %v, want ErrNotFound", fp, err)
		}
	}
}

func TestKvPutGetDelete(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	key := []byte("config/setting")
	value := []byte("hello world")

	if err := store.KvPut(ctx, key, value); err != nil {
		t.Fatalf("KvPut: %v", err)
	}

	got, err := store.KvGet(ctx, key)
	if err != nil {
		t.Fatalf("KvGet: %v", err)
	}
	if string(got) != string(value) {
		t.Errorf("KvGet: got %q, want %q", got, value)
	}

	if err := store.KvDelete(ctx, key); err != nil {
		t.Fatalf("KvDelete: %v", err)
	}

	_, err = store.KvGet(ctx, key)
	if !errors.Is(err, filer.ErrNotFound) {
		t.Errorf("KvGet after delete: got %v, want ErrNotFound", err)
	}
}
