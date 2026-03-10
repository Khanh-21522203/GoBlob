package postgres2

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"GoBlob/goblob/filer"
)

func TestPostgres2StoreInsertFindAndKV(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := &Postgres2Store{db: db}
	ctx := context.Background()

	entry := &filer.Entry{
		FullPath: "/unit/file.txt",
		Attr: filer.Attr{
			Mtime:    time.Now(),
			FileSize: 4,
		},
		Content: []byte("data"),
	}

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO filer_meta (dir_hash, dir, name, meta) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (dir_hash, dir, name) DO UPDATE SET meta = EXCLUDED.meta`)).
		WithArgs(sqlmock.AnyArg(), "/unit", "file.txt", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := store.InsertEntry(ctx, entry); err != nil {
		t.Fatalf("InsertEntry: %v", err)
	}

	meta, _ := json.Marshal(entry)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT meta FROM filer_meta WHERE dir_hash = $1 AND dir = $2 AND name = $3`)).
		WithArgs(sqlmock.AnyArg(), "/unit", "file.txt").
		WillReturnRows(sqlmock.NewRows([]string{"meta"}).AddRow(meta))

	got, err := store.FindEntry(ctx, "/unit/file.txt")
	if err != nil {
		t.Fatalf("FindEntry: %v", err)
	}
	if string(got.Content) != "data" {
		t.Fatalf("unexpected content %q", string(got.Content))
	}

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO filer_kv (k, v) VALUES ($1, $2)
		 ON CONFLICT (k) DO UPDATE SET v = EXCLUDED.v`)).
		WithArgs("616263", []byte("v")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.KvPut(ctx, []byte("abc"), []byte("v")); err != nil {
		t.Fatalf("KvPut: %v", err)
	}

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT v FROM filer_kv WHERE k = $1`)).
		WithArgs("616263").
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow([]byte("v")))
	value, err := store.KvGet(ctx, []byte("abc"))
	if err != nil {
		t.Fatalf("KvGet: %v", err)
	}
	if string(value) != "v" {
		t.Fatalf("unexpected kv value %q", string(value))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestBuildConnStringDefaults(t *testing.T) {
	got := buildConnString(map[string]string{}, "postgres2.")
	if got == "" {
		t.Fatal("empty conn string")
	}
	for _, required := range []string{"host=127.0.0.1", "port=5432", "dbname=goblob", "user=postgres", "sslmode=disable"} {
		if !regexp.MustCompile(regexp.QuoteMeta(required)).MatchString(got) {
			t.Fatalf("conn string missing %q: %s", required, got)
		}
	}
}
