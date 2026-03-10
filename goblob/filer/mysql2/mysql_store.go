package mysql2

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"GoBlob/goblob/filer"
)

type txContextKey struct{}

const maxKVKeyHexLen = 1024

// MySQL2Store implements filer.FilerStore on MySQL.
type MySQL2Store struct {
	db *sql.DB
}

func NewMySQL2Store(config map[string]string) (*MySQL2Store, error) {
	s := &MySQL2Store{}
	return s, s.Initialize(config, "mysql2.")
}

func (s *MySQL2Store) GetName() string { return "mysql2" }

func (s *MySQL2Store) Initialize(config map[string]string, prefix string) error {
	dsn := buildDSN(config, prefix)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("mysql2: open: %w", err)
	}

	if maxIdle := parseIntOrDefault(config[prefix+"connection_max_idle"], 10); maxIdle > 0 {
		db.SetMaxIdleConns(maxIdle)
	}
	if maxOpen := parseIntOrDefault(config[prefix+"connection_max_open"], 100); maxOpen > 0 {
		db.SetMaxOpenConns(maxOpen)
	}
	if maxLifetime := parseIntOrDefault(config[prefix+"connection_max_lifetime_seconds"], 300); maxLifetime > 0 {
		db.SetConnMaxLifetime(time.Duration(maxLifetime) * time.Second)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("mysql2: ping: %w", err)
	}

	if err := ensureSchema(ctx, db); err != nil {
		_ = db.Close()
		return err
	}

	if s.db != nil {
		_ = s.db.Close()
	}
	s.db = db
	return nil
}

func (s *MySQL2Store) Shutdown() {
	if s == nil || s.db == nil {
		return
	}
	_ = s.db.Close()
}

func ensureSchema(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS filer_meta (
			dir_hash BIGINT NOT NULL,
			dir VARCHAR(1024) NOT NULL,
			name VARCHAR(255) NOT NULL,
			meta LONGBLOB NOT NULL,
			PRIMARY KEY (dir_hash, dir, name),
			INDEX idx_filer_meta_dir_name (dir_hash, dir, name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS filer_kv (
			k VARCHAR(1024) PRIMARY KEY,
			v LONGBLOB NOT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("mysql2: ensure schema: %w", err)
		}
	}
	return nil
}

func (s *MySQL2Store) InsertEntry(ctx context.Context, entry *filer.Entry) error {
	return s.upsertEntry(ctx, entry)
}

func (s *MySQL2Store) UpdateEntry(ctx context.Context, entry *filer.Entry) error {
	return s.upsertEntry(ctx, entry)
}

func (s *MySQL2Store) upsertEntry(ctx context.Context, entry *filer.Entry) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if entry == nil {
		return fmt.Errorf("mysql2: entry is nil")
	}
	dir, name := entry.FullPath.DirAndName()
	meta, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("mysql2: marshal entry: %w", err)
	}

	_, err = s.execContext(ctx,
		`INSERT INTO filer_meta (dir_hash, dir, name, meta) VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE meta = VALUES(meta)`,
		dirHash(dir), string(dir), name, meta,
	)
	if err != nil {
		return fmt.Errorf("mysql2: upsert entry: %w", err)
	}
	return nil
}

func (s *MySQL2Store) FindEntry(ctx context.Context, fp filer.FullPath) (*filer.Entry, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	dir, name := fp.DirAndName()
	var data []byte
	err := s.queryRowContext(ctx,
		`SELECT meta FROM filer_meta WHERE dir_hash = ? AND dir = ? AND name = ?`,
		dirHash(dir), string(dir), name,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, filer.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mysql2: find entry: %w", err)
	}

	var entry filer.Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("mysql2: unmarshal entry: %w", err)
	}
	return &entry, nil
}

func (s *MySQL2Store) DeleteEntry(ctx context.Context, fp filer.FullPath) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	dir, name := fp.DirAndName()
	_, err := s.execContext(ctx,
		`DELETE FROM filer_meta WHERE dir_hash = ? AND dir = ? AND name = ?`,
		dirHash(dir), string(dir), name,
	)
	if err != nil {
		return fmt.Errorf("mysql2: delete entry: %w", err)
	}
	return nil
}

func (s *MySQL2Store) DeleteFolderChildren(ctx context.Context, fp filer.FullPath) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	prefix := string(fp)
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	_, err := s.execContext(ctx,
		`DELETE FROM filer_meta WHERE dir = ? OR dir LIKE ?`,
		string(fp), prefix+"%",
	)
	if err != nil {
		return fmt.Errorf("mysql2: delete folder children: %w", err)
	}
	return nil
}

func (s *MySQL2Store) ListDirectoryEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, fn func(*filer.Entry) bool) (string, error) {
	return s.listEntries(ctx, dirPath, startFileName, includeStart, limit, "", fn)
}

func (s *MySQL2Store) ListDirectoryPrefixedEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, prefix string, fn func(*filer.Entry) bool) (string, error) {
	return s.listEntries(ctx, dirPath, startFileName, includeStart, limit, prefix, fn)
}

func (s *MySQL2Store) listEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, namePrefix string, fn func(*filer.Entry) bool) (string, error) {
	if err := s.ensureReady(); err != nil {
		return "", err
	}

	args := []any{dirHash(dirPath), string(dirPath)}
	query := `SELECT name, meta FROM filer_meta WHERE dir_hash = ? AND dir = ?`
	if namePrefix != "" {
		query += " AND name LIKE ?"
		args = append(args, namePrefix+"%")
	}
	if startFileName != "" {
		op := ">"
		if includeStart {
			op = ">="
		}
		query += " AND name " + op + " ?"
		args = append(args, startFileName)
	}
	query += " ORDER BY name ASC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.queryContext(ctx, query, args...)
	if err != nil {
		return "", fmt.Errorf("mysql2: list entries: %w", err)
	}
	defer rows.Close()

	lastName := ""
	for rows.Next() {
		var name string
		var data []byte
		if err := rows.Scan(&name, &data); err != nil {
			return lastName, fmt.Errorf("mysql2: scan list row: %w", err)
		}
		var entry filer.Entry
		if err := json.Unmarshal(data, &entry); err != nil {
			return lastName, fmt.Errorf("mysql2: unmarshal list entry: %w", err)
		}
		lastName = name
		if fn != nil && !fn(&entry) {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return lastName, fmt.Errorf("mysql2: iterate list rows: %w", err)
	}
	return lastName, nil
}

func (s *MySQL2Store) BeginTransaction(ctx context.Context) (context.Context, error) {
	if err := s.ensureReady(); err != nil {
		return ctx, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ctx, fmt.Errorf("mysql2: begin tx: %w", err)
	}
	return context.WithValue(ctx, txContextKey{}, tx), nil
}

func (s *MySQL2Store) CommitTransaction(ctx context.Context) error {
	tx, _ := ctx.Value(txContextKey{}).(*sql.Tx)
	if tx == nil {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mysql2: commit tx: %w", err)
	}
	return nil
}

func (s *MySQL2Store) RollbackTransaction(ctx context.Context) error {
	tx, _ := ctx.Value(txContextKey{}).(*sql.Tx)
	if tx == nil {
		return nil
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("mysql2: rollback tx: %w", err)
	}
	return nil
}

func (s *MySQL2Store) KvPut(ctx context.Context, key []byte, value []byte) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	encoded := hex.EncodeToString(key)
	if len(encoded) > maxKVKeyHexLen {
		return fmt.Errorf("mysql2: kv key too long")
	}
	_, err := s.execContext(ctx,
		`INSERT INTO filer_kv (k, v) VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE v = VALUES(v)`,
		encoded, value,
	)
	if err != nil {
		return fmt.Errorf("mysql2: kv put: %w", err)
	}
	return nil
}

func (s *MySQL2Store) KvGet(ctx context.Context, key []byte) ([]byte, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	encoded := hex.EncodeToString(key)
	var value []byte
	err := s.queryRowContext(ctx, `SELECT v FROM filer_kv WHERE k = ?`, encoded).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, filer.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mysql2: kv get: %w", err)
	}
	return value, nil
}

func (s *MySQL2Store) KvDelete(ctx context.Context, key []byte) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	encoded := hex.EncodeToString(key)
	_, err := s.execContext(ctx, `DELETE FROM filer_kv WHERE k = ?`, encoded)
	if err != nil {
		return fmt.Errorf("mysql2: kv delete: %w", err)
	}
	return nil
}

func (s *MySQL2Store) ensureReady() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("mysql2: store not initialized")
	}
	return nil
}

func (s *MySQL2Store) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if tx, _ := ctx.Value(txContextKey{}).(*sql.Tx); tx != nil {
		return tx.ExecContext(ctx, query, args...)
	}
	return s.db.ExecContext(ctx, query, args...)
}

func (s *MySQL2Store) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if tx, _ := ctx.Value(txContextKey{}).(*sql.Tx); tx != nil {
		return tx.QueryContext(ctx, query, args...)
	}
	return s.db.QueryContext(ctx, query, args...)
}

func (s *MySQL2Store) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if tx, _ := ctx.Value(txContextKey{}).(*sql.Tx); tx != nil {
		return tx.QueryRowContext(ctx, query, args...)
	}
	return s.db.QueryRowContext(ctx, query, args...)
}

func buildDSN(config map[string]string, prefix string) string {
	host := strings.TrimSpace(config[prefix+"hostname"])
	if host == "" {
		host = "127.0.0.1"
	}
	port := parseIntOrDefault(config[prefix+"port"], 3306)
	database := strings.TrimSpace(config[prefix+"database"])
	if database == "" {
		database = "goblob"
	}
	username := strings.TrimSpace(config[prefix+"username"])
	if username == "" {
		username = "root"
	}
	password := config[prefix+"password"]

	q := url.Values{}
	q.Set("parseTime", "true")
	q.Set("charset", "utf8mb4")
	q.Set("collation", "utf8mb4_unicode_ci")

	if extra := strings.TrimSpace(config[prefix+"params"]); extra != "" {
		for _, pair := range strings.Split(extra, "&") {
			parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
			if len(parts) == 2 {
				q.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
			}
		}
	}

	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?%s", username, password, host, port, database, q.Encode())
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

func dirHash(dir filer.FullPath) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(dir))
	return int64(h.Sum64())
}
