package tiering

import (
	"context"
	"testing"
	"time"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/filer"
)

func TestScannerScanAndTier(t *testing.T) {
	now := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	entries := []*filer.Entry{
		{
			FullPath: "/hot/file-a",
			Attr: filer.Attr{
				Mtime:    now.Add(-48 * time.Hour),
				DiskType: types.SolidStateType,
			},
		},
		{
			FullPath: "/cold/file-b",
			Attr: filer.Attr{
				Mtime:    now.Add(-240 * time.Hour),
				DiskType: types.HardDriveType,
			},
		},
	}

	tier := &Tier{AccessAge: 24 * time.Hour, ArchiveAge: 7 * 24 * time.Hour}
	scanner, err := NewScanner(tier, func(ctx context.Context, fn func(*filer.Entry) bool) error {
		_ = ctx
		for _, e := range entries {
			if !fn(e) {
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	scanner.nowFn = func() time.Time { return now }

	migrated := 0
	archived := 0
	scanner.SetMigrator(func(ctx context.Context, entry *filer.Entry, targetDiskType string) error {
		_ = ctx
		_ = targetDiskType
		migrated++
		return nil
	})
	scanner.SetArchiver(func(ctx context.Context, entry *filer.Entry) error {
		_ = ctx
		archived++
		return nil
	})

	if err := scanner.ScanAndTier(context.Background()); err != nil {
		t.Fatalf("ScanAndTier: %v", err)
	}
	if migrated != 1 {
		t.Fatalf("migrated=%d want 1", migrated)
	}
	if archived != 1 {
		t.Fatalf("archived=%d want 1", archived)
	}
}

func TestNewScannerValidateInputs(t *testing.T) {
	if _, err := NewScanner(nil, func(context.Context, func(*filer.Entry) bool) error { return nil }); err == nil {
		t.Fatal("expected nil policy error")
	}
	if _, err := NewScanner(&Tier{}, nil); err == nil {
		t.Fatal("expected nil list function error")
	}
}
