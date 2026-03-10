package redis3

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"GoBlob/goblob/filer"
)

func TestRedis3StoreEntryAndKV(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	store, err := NewRedis3Store(map[string]string{
		"redis3.address": mr.Addr(),
	})
	if err != nil {
		t.Fatalf("NewRedis3Store: %v", err)
	}
	defer store.Shutdown()

	ctx := context.Background()
	entry := &filer.Entry{
		FullPath: "/photos/a.jpg",
		Attr: filer.Attr{
			Mtime:    time.Now(),
			FileSize: 3,
			Mode:     0o644,
		},
		Content: []byte("img"),
	}

	if err := store.InsertEntry(ctx, entry); err != nil {
		t.Fatalf("InsertEntry: %v", err)
	}
	got, err := store.FindEntry(ctx, "/photos/a.jpg")
	if err != nil {
		t.Fatalf("FindEntry: %v", err)
	}
	if string(got.Content) != "img" {
		t.Fatalf("unexpected content %q", string(got.Content))
	}

	if err := store.KvPut(ctx, []byte("iam"), []byte("ok")); err != nil {
		t.Fatalf("KvPut: %v", err)
	}
	value, err := store.KvGet(ctx, []byte("iam"))
	if err != nil {
		t.Fatalf("KvGet: %v", err)
	}
	if string(value) != "ok" {
		t.Fatalf("unexpected kv value %q", string(value))
	}

	if err := store.KvDelete(ctx, []byte("iam")); err != nil {
		t.Fatalf("KvDelete: %v", err)
	}
	if _, err := store.KvGet(ctx, []byte("iam")); err != filer.ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestRedis3StoreDeleteFolderChildren(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	store, err := NewRedis3Store(map[string]string{"redis3.address": mr.Addr()})
	if err != nil {
		t.Fatalf("NewRedis3Store: %v", err)
	}
	defer store.Shutdown()

	ctx := context.Background()
	entries := []*filer.Entry{
		{FullPath: "/docs/a.txt", Content: []byte("a")},
		{FullPath: "/docs/sub/b.txt", Content: []byte("b")},
		{FullPath: "/other/c.txt", Content: []byte("c")},
	}
	for _, e := range entries {
		if err := store.InsertEntry(ctx, e); err != nil {
			t.Fatalf("InsertEntry(%s): %v", e.FullPath, err)
		}
	}

	if err := store.DeleteFolderChildren(ctx, "/docs"); err != nil {
		t.Fatalf("DeleteFolderChildren: %v", err)
	}
	if _, err := store.FindEntry(ctx, "/docs/a.txt"); err != filer.ErrNotFound {
		t.Fatalf("expected docs entry deleted, got %v", err)
	}
	if _, err := store.FindEntry(ctx, "/docs/sub/b.txt"); err != filer.ErrNotFound {
		t.Fatalf("expected nested docs entry deleted, got %v", err)
	}
	if _, err := store.FindEntry(ctx, "/other/c.txt"); err != nil {
		t.Fatalf("expected other entry to remain, got %v", err)
	}
}
