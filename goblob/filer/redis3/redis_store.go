package redis3

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"GoBlob/goblob/filer"
)

// Redis3Store implements filer.FilerStore on Redis.
type Redis3Store struct {
	client *redis.Client

	namespace string
	dirsKey   string
}

func NewRedis3Store(config map[string]string) (*Redis3Store, error) {
	s := &Redis3Store{}
	return s, s.Initialize(config, "redis3.")
}

func (s *Redis3Store) GetName() string { return "redis3" }

func (s *Redis3Store) Initialize(config map[string]string, prefix string) error {
	addr := strings.TrimSpace(config[prefix+"address"])
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	password := config[prefix+"password"]
	db := parseIntOrDefault(config[prefix+"database"], 0)
	namespace := strings.TrimSpace(config[prefix+"key_prefix"])
	if namespace == "" {
		namespace = "goblob:filer"
	}

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return fmt.Errorf("redis3: ping: %w", err)
	}

	if s.client != nil {
		_ = s.client.Close()
	}
	s.client = client
	s.namespace = namespace
	s.dirsKey = namespace + ":dirs"
	return nil
}

func (s *Redis3Store) Shutdown() {
	if s == nil || s.client == nil {
		return
	}
	_ = s.client.Close()
}

func (s *Redis3Store) InsertEntry(ctx context.Context, entry *filer.Entry) error {
	return s.upsert(ctx, entry)
}

func (s *Redis3Store) UpdateEntry(ctx context.Context, entry *filer.Entry) error {
	return s.upsert(ctx, entry)
}

func (s *Redis3Store) upsert(ctx context.Context, entry *filer.Entry) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if entry == nil {
		return fmt.Errorf("redis3: entry is nil")
	}
	dir, name := entry.FullPath.DirAndName()
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("redis3: marshal entry: %w", err)
	}

	pipe := s.client.Pipeline()
	pipe.HSet(ctx, s.metaKey(dir), name, data)
	pipe.SAdd(ctx, s.dirsKey, string(dir))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis3: upsert entry: %w", err)
	}
	return nil
}

func (s *Redis3Store) FindEntry(ctx context.Context, fp filer.FullPath) (*filer.Entry, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	dir, name := fp.DirAndName()
	data, err := s.client.HGet(ctx, s.metaKey(dir), name).Bytes()
	if err == redis.Nil {
		return nil, filer.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis3: find entry: %w", err)
	}
	var entry filer.Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("redis3: unmarshal entry: %w", err)
	}
	return &entry, nil
}

func (s *Redis3Store) DeleteEntry(ctx context.Context, fp filer.FullPath) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	dir, name := fp.DirAndName()
	key := s.metaKey(dir)

	pipe := s.client.Pipeline()
	pipe.HDel(ctx, key, name)
	hLenCmd := pipe.HLen(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis3: delete entry: %w", err)
	}
	if hLenCmd.Val() == 0 {
		_, _ = s.client.SRem(ctx, s.dirsKey, string(dir)).Result()
	}
	return nil
}

func (s *Redis3Store) DeleteFolderChildren(ctx context.Context, fp filer.FullPath) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	dirs, err := s.client.SMembers(ctx, s.dirsKey).Result()
	if err != nil {
		return fmt.Errorf("redis3: list dirs: %w", err)
	}

	prefix := string(fp)
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	pipe := s.client.Pipeline()
	for _, dir := range dirs {
		if dir == string(fp) || strings.HasPrefix(dir, prefix) {
			pipe.Del(ctx, s.metaKey(filer.FullPath(dir)))
			pipe.SRem(ctx, s.dirsKey, dir)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis3: delete folder children: %w", err)
	}
	return nil
}

func (s *Redis3Store) ListDirectoryEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, fn func(*filer.Entry) bool) (string, error) {
	return s.listEntries(ctx, dirPath, startFileName, includeStart, limit, "", fn)
}

func (s *Redis3Store) ListDirectoryPrefixedEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, prefix string, fn func(*filer.Entry) bool) (string, error) {
	return s.listEntries(ctx, dirPath, startFileName, includeStart, limit, prefix, fn)
}

func (s *Redis3Store) listEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, prefix string, fn func(*filer.Entry) bool) (string, error) {
	if err := s.ensureReady(); err != nil {
		return "", err
	}
	items, err := s.client.HGetAll(ctx, s.metaKey(dirPath)).Result()
	if err != nil {
		return "", fmt.Errorf("redis3: list entries: %w", err)
	}

	names := make([]string, 0, len(items))
	for name := range items {
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	last := ""
	var count int64
	for _, name := range names {
		if startFileName != "" {
			if includeStart {
				if name < startFileName {
					continue
				}
			} else if name <= startFileName {
				continue
			}
		}

		var entry filer.Entry
		if err := json.Unmarshal([]byte(items[name]), &entry); err != nil {
			return last, fmt.Errorf("redis3: unmarshal list entry: %w", err)
		}
		last = name
		count++
		if fn != nil && !fn(&entry) {
			break
		}
		if limit > 0 && count >= limit {
			break
		}
	}
	return last, nil
}

func (s *Redis3Store) BeginTransaction(ctx context.Context) (context.Context, error) { return ctx, nil }
func (s *Redis3Store) CommitTransaction(ctx context.Context) error                   { return nil }
func (s *Redis3Store) RollbackTransaction(ctx context.Context) error                 { return nil }

func (s *Redis3Store) KvPut(ctx context.Context, key []byte, value []byte) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if err := s.client.Set(ctx, s.kvKey(key), value, 0).Err(); err != nil {
		return fmt.Errorf("redis3: kv put: %w", err)
	}
	return nil
}

func (s *Redis3Store) KvGet(ctx context.Context, key []byte) ([]byte, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	value, err := s.client.Get(ctx, s.kvKey(key)).Bytes()
	if err == redis.Nil {
		return nil, filer.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis3: kv get: %w", err)
	}
	return value, nil
}

func (s *Redis3Store) KvDelete(ctx context.Context, key []byte) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if err := s.client.Del(ctx, s.kvKey(key)).Err(); err != nil {
		return fmt.Errorf("redis3: kv delete: %w", err)
	}
	return nil
}

func (s *Redis3Store) metaKey(dir filer.FullPath) string {
	return s.namespace + ":meta:" + string(dir)
}

func (s *Redis3Store) kvKey(key []byte) string {
	return s.namespace + ":kv:" + hex.EncodeToString(key)
}

func (s *Redis3Store) ensureReady() error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis3: store not initialized")
	}
	return nil
}

func parseIntOrDefault(value string, def int) int {
	if strings.TrimSpace(value) == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return def
	}
	return n
}
