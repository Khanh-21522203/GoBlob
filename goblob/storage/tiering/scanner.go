package tiering

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/filer"
	"GoBlob/goblob/obs"
)

// ErrNilTierPolicy indicates scanner initialization with nil policy.
var ErrNilTierPolicy = errors.New("tier policy is nil")

// Scanner periodically scans filer entries and applies migration/archive rules.
type Scanner struct {
	tier      *Tier
	interval  time.Duration
	listFn    func(context.Context, func(*filer.Entry) bool) error
	migrateFn func(context.Context, *filer.Entry, string) error
	archiveFn func(context.Context, *filer.Entry) error
	nowFn     func() time.Time
	logger    *slog.Logger
}

func NewScanner(tier *Tier, listFn func(context.Context, func(*filer.Entry) bool) error) (*Scanner, error) {
	if tier == nil {
		return nil, ErrNilTierPolicy
	}
	if listFn == nil {
		return nil, errors.New("list function is required")
	}
	return &Scanner{
		tier:      tier,
		interval:  time.Hour,
		listFn:    listFn,
		migrateFn: defaultMigrate,
		archiveFn: defaultArchive,
		nowFn:     time.Now,
		logger:    obs.New("tiering"),
	}, nil
}

func (s *Scanner) SetInterval(interval time.Duration) {
	if s == nil || interval <= 0 {
		return
	}
	s.interval = interval
}

func (s *Scanner) SetMigrator(fn func(context.Context, *filer.Entry, string) error) {
	if s == nil || fn == nil {
		return
	}
	s.migrateFn = fn
}

func (s *Scanner) SetArchiver(fn func(context.Context, *filer.Entry) error) {
	if s == nil || fn == nil {
		return
	}
	s.archiveFn = fn
}

func (s *Scanner) Start(ctx context.Context) {
	if s == nil {
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.ScanAndTier(ctx); err != nil {
				s.logger.Warn("tier scan failed", "error", err)
			}
		}
	}
}

func (s *Scanner) ScanAndTier(ctx context.Context) error {
	if s == nil {
		return nil
	}
	now := s.nowFn()
	return s.listFn(ctx, func(entry *filer.Entry) bool {
		if entry == nil || entry.IsDirectory() {
			return true
		}
		age := now.Sub(entry.Attr.Mtime)

		if entry.Remote == nil && s.tier.ArchiveAge > 0 && age >= s.tier.ArchiveAge {
			if err := s.archiveFn(ctx, entry); err != nil {
				s.logger.Warn("archive failed", "path", entry.FullPath, "error", err)
			}
			return true
		}

		if entry.Attr.DiskType == types.SolidStateType && s.tier.AccessAge > 0 && age >= s.tier.AccessAge {
			if err := s.migrateFn(ctx, entry, DiskTypeHDD); err != nil {
				s.logger.Warn("migrate failed", "path", entry.FullPath, "error", err)
			}
		}
		return true
	})
}

func defaultMigrate(ctx context.Context, entry *filer.Entry, targetDiskType string) error {
	_ = ctx
	if entry == nil {
		return nil
	}
	if targetDiskType == "" {
		targetDiskType = DiskTypeHDD
	}
	entry.Attr.DiskType = types.DiskType(targetDiskType)
	return nil
}

func defaultArchive(ctx context.Context, entry *filer.Entry) error {
	_ = ctx
	if entry == nil || entry.Remote != nil {
		return nil
	}
	entry.Remote = &filer.RemoteEntry{
		StorageName: "s3",
		Key:         string(entry.FullPath),
	}
	return nil
}
