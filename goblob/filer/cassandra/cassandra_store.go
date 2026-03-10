package cassandra

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gocql/gocql"

	"GoBlob/goblob/filer"
)

// CassandraStore implements filer.FilerStore on Cassandra.
type CassandraStore struct {
	session  *gocql.Session
	keyspace string
}

func NewCassandraStore(config map[string]string) (*CassandraStore, error) {
	s := &CassandraStore{}
	return s, s.Initialize(config, "cassandra.")
}

func (s *CassandraStore) GetName() string { return "cassandra" }

func (s *CassandraStore) Initialize(config map[string]string, prefix string) error {
	hosts := parseHosts(config[prefix+"hosts"])
	if len(hosts) == 0 {
		hosts = []string{"127.0.0.1"}
	}
	port := parseIntOrDefault(config[prefix+"port"], 9042)
	keyspace := strings.TrimSpace(config[prefix+"keyspace"])
	if keyspace == "" {
		keyspace = "goblob"
	}

	baseCluster := gocql.NewCluster(hosts...)
	baseCluster.Port = port
	baseCluster.Timeout = 10 * time.Second
	baseCluster.ConnectTimeout = 10 * time.Second
	applyAuth(baseCluster, config, prefix)

	bootstrap, err := baseCluster.CreateSession()
	if err != nil {
		return fmt.Errorf("cassandra: connect bootstrap: %w", err)
	}
	defer bootstrap.Close()

	createKeyspaceCQL := fmt.Sprintf(`CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class':'SimpleStrategy','replication_factor':1}`,
		sanitizeIdentifier(keyspace),
	)
	if err := bootstrap.Query(createKeyspaceCQL).Exec(); err != nil {
		return fmt.Errorf("cassandra: create keyspace: %w", err)
	}

	cluster := gocql.NewCluster(hosts...)
	cluster.Port = port
	cluster.Keyspace = keyspace
	cluster.Timeout = 10 * time.Second
	cluster.ConnectTimeout = 10 * time.Second
	cluster.Consistency = gocql.Quorum
	applyAuth(cluster, config, prefix)

	session, err := cluster.CreateSession()
	if err != nil {
		return fmt.Errorf("cassandra: connect keyspace: %w", err)
	}

	if err := ensureSchema(session); err != nil {
		session.Close()
		return err
	}

	if s.session != nil {
		s.session.Close()
	}
	s.session = session
	s.keyspace = keyspace
	return nil
}

func applyAuth(cluster *gocql.ClusterConfig, config map[string]string, prefix string) {
	username := strings.TrimSpace(config[prefix+"username"])
	if username == "" {
		return
	}
	cluster.Authenticator = gocql.PasswordAuthenticator{
		Username: username,
		Password: config[prefix+"password"],
	}
}

func ensureSchema(session *gocql.Session) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS filer_meta (
			dir text,
			name text,
			meta blob,
			PRIMARY KEY (dir, name)
		) WITH CLUSTERING ORDER BY (name ASC)`,
		`CREATE TABLE IF NOT EXISTS filer_kv (
			k text PRIMARY KEY,
			v blob
		)`,
		`CREATE TABLE IF NOT EXISTS filer_dirs (
			dir text PRIMARY KEY
		)`,
	}
	for _, stmt := range stmts {
		if err := session.Query(stmt).Exec(); err != nil {
			return fmt.Errorf("cassandra: ensure schema: %w", err)
		}
	}
	return nil
}

func (s *CassandraStore) Shutdown() {
	if s == nil || s.session == nil {
		return
	}
	s.session.Close()
}

func (s *CassandraStore) InsertEntry(ctx context.Context, entry *filer.Entry) error {
	return s.upsert(ctx, entry)
}

func (s *CassandraStore) UpdateEntry(ctx context.Context, entry *filer.Entry) error {
	return s.upsert(ctx, entry)
}

func (s *CassandraStore) upsert(ctx context.Context, entry *filer.Entry) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if entry == nil {
		return fmt.Errorf("cassandra: entry is nil")
	}
	dir, name := entry.FullPath.DirAndName()
	meta, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("cassandra: marshal entry: %w", err)
	}

	if err := s.session.Query(`INSERT INTO filer_meta (dir, name, meta) VALUES (?, ?, ?)`, string(dir), name, meta).
		WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("cassandra: upsert entry: %w", err)
	}
	if err := s.session.Query(`INSERT INTO filer_dirs (dir) VALUES (?)`, string(dir)).
		WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("cassandra: add dir index: %w", err)
	}
	return nil
}

func (s *CassandraStore) FindEntry(ctx context.Context, fp filer.FullPath) (*filer.Entry, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	dir, name := fp.DirAndName()
	var data []byte
	err := s.session.Query(`SELECT meta FROM filer_meta WHERE dir = ? AND name = ?`, string(dir), name).
		WithContext(ctx).Scan(&data)
	if err == gocql.ErrNotFound {
		return nil, filer.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cassandra: find entry: %w", err)
	}
	var entry filer.Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("cassandra: unmarshal entry: %w", err)
	}
	return &entry, nil
}

func (s *CassandraStore) DeleteEntry(ctx context.Context, fp filer.FullPath) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	dir, name := fp.DirAndName()
	if err := s.session.Query(`DELETE FROM filer_meta WHERE dir = ? AND name = ?`, string(dir), name).
		WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("cassandra: delete entry: %w", err)
	}
	return nil
}

func (s *CassandraStore) DeleteFolderChildren(ctx context.Context, fp filer.FullPath) error {
	if err := s.ensureReady(); err != nil {
		return err
	}

	dirs, err := s.listDirs(ctx)
	if err != nil {
		return err
	}
	prefix := string(fp)
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	for _, dir := range dirs {
		if dir == string(fp) || strings.HasPrefix(dir, prefix) {
			if err := s.session.Query(`DELETE FROM filer_meta WHERE dir = ?`, dir).WithContext(ctx).Exec(); err != nil {
				return fmt.Errorf("cassandra: delete folder children: %w", err)
			}
			if err := s.session.Query(`DELETE FROM filer_dirs WHERE dir = ?`, dir).WithContext(ctx).Exec(); err != nil {
				return fmt.Errorf("cassandra: delete dir index: %w", err)
			}
		}
	}
	return nil
}

func (s *CassandraStore) ListDirectoryEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, fn func(*filer.Entry) bool) (string, error) {
	return s.listEntries(ctx, dirPath, startFileName, includeStart, limit, "", fn)
}

func (s *CassandraStore) ListDirectoryPrefixedEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, prefix string, fn func(*filer.Entry) bool) (string, error) {
	return s.listEntries(ctx, dirPath, startFileName, includeStart, limit, prefix, fn)
}

func (s *CassandraStore) listEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, prefix string, fn func(*filer.Entry) bool) (string, error) {
	if err := s.ensureReady(); err != nil {
		return "", err
	}

	iter := s.session.Query(`SELECT name, meta FROM filer_meta WHERE dir = ?`, string(dirPath)).WithContext(ctx).Iter()
	defer iter.Close()

	type namedEntry struct {
		name string
		meta []byte
	}
	entries := make([]namedEntry, 0)

	var name string
	var meta []byte
	for iter.Scan(&name, &meta) {
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		if startFileName != "" {
			if includeStart {
				if name < startFileName {
					continue
				}
			} else if name <= startFileName {
				continue
			}
		}
		copied := append([]byte(nil), meta...)
		entries = append(entries, namedEntry{name: name, meta: copied})
	}
	if err := iter.Close(); err != nil {
		return "", fmt.Errorf("cassandra: list entries: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	last := ""
	var count int64
	for _, item := range entries {
		var entry filer.Entry
		if err := json.Unmarshal(item.meta, &entry); err != nil {
			return last, fmt.Errorf("cassandra: unmarshal list entry: %w", err)
		}
		last = item.name
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

func (s *CassandraStore) BeginTransaction(ctx context.Context) (context.Context, error) {
	return ctx, nil
}
func (s *CassandraStore) CommitTransaction(ctx context.Context) error   { return nil }
func (s *CassandraStore) RollbackTransaction(ctx context.Context) error { return nil }

func (s *CassandraStore) KvPut(ctx context.Context, key []byte, value []byte) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if err := s.session.Query(`INSERT INTO filer_kv (k, v) VALUES (?, ?)`, hex.EncodeToString(key), value).
		WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("cassandra: kv put: %w", err)
	}
	return nil
}

func (s *CassandraStore) KvGet(ctx context.Context, key []byte) ([]byte, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	var value []byte
	err := s.session.Query(`SELECT v FROM filer_kv WHERE k = ?`, hex.EncodeToString(key)).
		WithContext(ctx).Scan(&value)
	if err == gocql.ErrNotFound {
		return nil, filer.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cassandra: kv get: %w", err)
	}
	return value, nil
}

func (s *CassandraStore) KvDelete(ctx context.Context, key []byte) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if err := s.session.Query(`DELETE FROM filer_kv WHERE k = ?`, hex.EncodeToString(key)).
		WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("cassandra: kv delete: %w", err)
	}
	return nil
}

func (s *CassandraStore) listDirs(ctx context.Context) ([]string, error) {
	iter := s.session.Query(`SELECT dir FROM filer_dirs`).WithContext(ctx).Iter()
	defer iter.Close()

	var dirs []string
	var dir string
	for iter.Scan(&dir) {
		dirs = append(dirs, dir)
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("cassandra: list dirs: %w", err)
	}
	return dirs, nil
}

func (s *CassandraStore) ensureReady() error {
	if s == nil || s.session == nil {
		return fmt.Errorf("cassandra: store not initialized")
	}
	return nil
}

func parseHosts(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
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

func sanitizeIdentifier(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "goblob"
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return "goblob"
	}
	return name
}
