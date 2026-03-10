package postgres2

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"GoBlob/goblob/filer"
)

type txContextKey struct{}

// Postgres2Store implements filer.FilerStore on PostgreSQL.
type Postgres2Store struct {
	db *sql.DB
}

func NewPostgres2Store(config map[string]string) (*Postgres2Store, error) {
	s := &Postgres2Store{}
	return s, s.Initialize(config, "postgres2.")
}

func (s *Postgres2Store) GetName() string { return "postgres2" }

func (s *Postgres2Store) Initialize(config map[string]string, prefix string) error {
	connStr := buildConnString(config, prefix)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("postgres2: open: %w", err)
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
		return fmt.Errorf("postgres2: ping: %w", err)
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

func (s *Postgres2Store) Shutdown() {
	if s == nil || s.db == nil {
		return
	}
	_ = s.db.Close()
}

func ensureSchema(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS filer_meta (
			dir_hash BIGINT NOT NULL,
			dir TEXT NOT NULL,
			name TEXT NOT NULL,
			meta BYTEA NOT NULL,
			PRIMARY KEY (dir_hash, dir, name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_filer_meta_dir_name ON filer_meta (dir_hash, dir, name)`,
		`CREATE TABLE IF NOT EXISTS filer_kv (
			k TEXT PRIMARY KEY,
			v BYTEA NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("postgres2: ensure schema: %w", err)
		}
	}
	return nil
}

func (s *Postgres2Store) InsertEntry(ctx context.Context, entry *filer.Entry) error {
	return s.upsertEntry(ctx, entry)
}

func (s *Postgres2Store) UpdateEntry(ctx context.Context, entry *filer.Entry) error {
	return s.upsertEntry(ctx, entry)
}

func (s *Postgres2Store) upsertEntry(ctx context.Context, entry *filer.Entry) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	if entry == nil {
		return fmt.Errorf("postgres2: entry is nil")
	}

	dir, name := entry.FullPath.DirAndName()
	meta, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("postgres2: marshal entry: %w", err)
	}

	_, err = s.execContext(ctx,
		`INSERT INTO filer_meta (dir_hash, dir, name, meta) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (dir_hash, dir, name) DO UPDATE SET meta = EXCLUDED.meta`,
		dirHash(dir), string(dir), name, meta,
	)
	if err != nil {
		return fmt.Errorf("postgres2: upsert entry: %w", err)
	}
	return nil
}

func (s *Postgres2Store) FindEntry(ctx context.Context, fp filer.FullPath) (*filer.Entry, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	dir, name := fp.DirAndName()

	var data []byte
	err := s.queryRowContext(ctx,
		`SELECT meta FROM filer_meta WHERE dir_hash = $1 AND dir = $2 AND name = $3`,
		dirHash(dir), string(dir), name,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, filer.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres2: find entry: %w", err)
	}

	var entry filer.Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("postgres2: unmarshal entry: %w", err)
	}
	return &entry, nil
}

func (s *Postgres2Store) DeleteEntry(ctx context.Context, fp filer.FullPath) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	dir, name := fp.DirAndName()
	_, err := s.execContext(ctx,
		`DELETE FROM filer_meta WHERE dir_hash = $1 AND dir = $2 AND name = $3`,
		dirHash(dir), string(dir), name,
	)
	if err != nil {
		return fmt.Errorf("postgres2: delete entry: %w", err)
	}
	return nil
}

func (s *Postgres2Store) DeleteFolderChildren(ctx context.Context, fp filer.FullPath) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	prefix := string(fp)
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	_, err := s.execContext(ctx,
		`DELETE FROM filer_meta WHERE dir = $1 OR dir LIKE $2`,
		string(fp), prefix+"%",
	)
	if err != nil {
		return fmt.Errorf("postgres2: delete folder children: %w", err)
	}
	return nil
}

func (s *Postgres2Store) ListDirectoryEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, fn func(*filer.Entry) bool) (string, error) {
	return s.listEntries(ctx, dirPath, startFileName, includeStart, limit, "", fn)
}

func (s *Postgres2Store) ListDirectoryPrefixedEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, prefix string, fn func(*filer.Entry) bool) (string, error) {
	return s.listEntries(ctx, dirPath, startFileName, includeStart, limit, prefix, fn)
}

func (s *Postgres2Store) listEntries(ctx context.Context, dirPath filer.FullPath, startFileName string, includeStart bool, limit int64, namePrefix string, fn func(*filer.Entry) bool) (string, error) {
	if err := s.ensureReady(); err != nil {
		return "", err
	}

	args := []any{dirHash(dirPath), string(dirPath)}
	query := `SELECT name, meta FROM filer_meta WHERE dir_hash = $1 AND dir = $2`

	if namePrefix != "" {
		args = append(args, namePrefix+"%")
		query += fmt.Sprintf(" AND name LIKE $%d", len(args))
	}
	if startFileName != "" {
		op := ">"
		if includeStart {
			op = ">="
		}
		args = append(args, startFileName)
		query += fmt.Sprintf(" AND name %s $%d", op, len(args))
	}
	query += " ORDER BY name ASC"
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := s.queryContext(ctx, query, args...)
	if err != nil {
		return "", fmt.Errorf("postgres2: list entries: %w", err)
	}
	defer rows.Close()

	lastName := ""
	for rows.Next() {
		var name string
		var data []byte
		if err := rows.Scan(&name, &data); err != nil {
			return lastName, fmt.Errorf("postgres2: scan list row: %w", err)
		}
		var entry filer.Entry
		if err := json.Unmarshal(data, &entry); err != nil {
			return lastName, fmt.Errorf("postgres2: unmarshal list entry: %w", err)
		}
		lastName = name
		if fn != nil && !fn(&entry) {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return lastName, fmt.Errorf("postgres2: iterate list rows: %w", err)
	}
	return lastName, nil
}

func (s *Postgres2Store) BeginTransaction(ctx context.Context) (context.Context, error) {
	if err := s.ensureReady(); err != nil {
		return ctx, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ctx, fmt.Errorf("postgres2: begin tx: %w", err)
	}
	return context.WithValue(ctx, txContextKey{}, tx), nil
}

func (s *Postgres2Store) CommitTransaction(ctx context.Context) error {
	tx, _ := ctx.Value(txContextKey{}).(*sql.Tx)
	if tx == nil {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres2: commit tx: %w", err)
	}
	return nil
}

func (s *Postgres2Store) RollbackTransaction(ctx context.Context) error {
	tx, _ := ctx.Value(txContextKey{}).(*sql.Tx)
	if tx == nil {
		return nil
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("postgres2: rollback tx: %w", err)
	}
	return nil
}

func (s *Postgres2Store) KvPut(ctx context.Context, key []byte, value []byte) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	encodedKey := hex.EncodeToString(key)
	_, err := s.execContext(ctx,
		`INSERT INTO filer_kv (k, v) VALUES ($1, $2)
		 ON CONFLICT (k) DO UPDATE SET v = EXCLUDED.v`,
		encodedKey, value,
	)
	if err != nil {
		return fmt.Errorf("postgres2: kv put: %w", err)
	}
	return nil
}

func (s *Postgres2Store) KvGet(ctx context.Context, key []byte) ([]byte, error) {
	if err := s.ensureReady(); err != nil {
		return nil, err
	}
	encodedKey := hex.EncodeToString(key)
	var value []byte
	err := s.queryRowContext(ctx, `SELECT v FROM filer_kv WHERE k = $1`, encodedKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, filer.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("postgres2: kv get: %w", err)
	}
	return value, nil
}

func (s *Postgres2Store) KvDelete(ctx context.Context, key []byte) error {
	if err := s.ensureReady(); err != nil {
		return err
	}
	encodedKey := hex.EncodeToString(key)
	_, err := s.execContext(ctx, `DELETE FROM filer_kv WHERE k = $1`, encodedKey)
	if err != nil {
		return fmt.Errorf("postgres2: kv delete: %w", err)
	}
	return nil
}

func (s *Postgres2Store) ensureReady() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("postgres2: store not initialized")
	}
	return nil
}

func (s *Postgres2Store) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if tx, _ := ctx.Value(txContextKey{}).(*sql.Tx); tx != nil {
		return tx.ExecContext(ctx, query, args...)
	}
	return s.db.ExecContext(ctx, query, args...)
}

func (s *Postgres2Store) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if tx, _ := ctx.Value(txContextKey{}).(*sql.Tx); tx != nil {
		return tx.QueryContext(ctx, query, args...)
	}
	return s.db.QueryContext(ctx, query, args...)
}

func (s *Postgres2Store) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if tx, _ := ctx.Value(txContextKey{}).(*sql.Tx); tx != nil {
		return tx.QueryRowContext(ctx, query, args...)
	}
	return s.db.QueryRowContext(ctx, query, args...)
}

func buildConnString(config map[string]string, prefix string) string {
	host := strings.TrimSpace(config[prefix+"hostname"])
	if host == "" {
		host = "127.0.0.1"
	}
	port := parseIntOrDefault(config[prefix+"port"], 5432)
	database := strings.TrimSpace(config[prefix+"database"])
	if database == "" {
		database = "goblob"
	}
	username := strings.TrimSpace(config[prefix+"username"])
	if username == "" {
		username = "postgres"
	}
	password := config[prefix+"password"]
	sslMode := strings.TrimSpace(config[prefix+"sslmode"])
	if sslMode == "" {
		sslMode = "disable"
	}

	parts := []string{
		fmt.Sprintf("host=%s", host),
		fmt.Sprintf("port=%d", port),
		fmt.Sprintf("dbname=%s", database),
		fmt.Sprintf("user=%s", username),
		fmt.Sprintf("password=%s", password),
		fmt.Sprintf("sslmode=%s", sslMode),
	}
	return strings.Join(parts, " ")
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
