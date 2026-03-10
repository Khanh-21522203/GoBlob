package iam

import (
	"context"
	"sync"
	"testing"

	"GoBlob/goblob/filer"
	iampb "GoBlob/goblob/pb/iam_pb"
)

type mockFilerKV struct {
	mu sync.RWMutex
	kv map[string][]byte
}

func newMockFilerKV() *mockFilerKV {
	return &mockFilerKV{kv: make(map[string][]byte)}
}

func (m *mockFilerKV) KvGet(_ context.Context, key []byte) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.kv[string(key)]
	if !ok {
		return nil, filer.ErrNotFound
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (m *mockFilerKV) KvPut(_ context.Context, key []byte, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]byte, len(value))
	copy(copied, value)
	m.kv[string(key)] = copied
	return nil
}

func TestLookupByAccessKey(t *testing.T) {
	iam, err := NewIdentityAccessManagement(nil, nil)
	if err != nil {
		t.Fatalf("NewIdentityAccessManagement: %v", err)
	}
	cfg := &iampb.S3ApiConfiguration{Identities: []*iampb.Identity{
		{
			Name:        "alice",
			Credentials: []*iampb.Credential{{AccessKey: "AK1", SecretKey: "SK1"}},
			Actions:     []string{"Read"},
		},
		{
			Name:        "bob",
			Credentials: []*iampb.Credential{{AccessKey: "AK2", SecretKey: "SK2"}},
			Actions:     []string{"Write"},
		},
	}}
	if err := iam.Reload(cfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	identity, secret, ok := iam.LookupByAccessKey("AK2")
	if !ok {
		t.Fatalf("LookupByAccessKey should hit")
	}
	if identity.Name != "bob" || secret != "SK2" {
		t.Fatalf("unexpected lookup result: identity=%q secret=%q", identity.Name, secret)
	}
}

func TestIsAuthorized(t *testing.T) {
	iam, _ := NewIdentityAccessManagement(nil, nil)

	admin := &Identity{Actions: []string{"Admin"}}
	if !iam.IsAuthorized(admin, S3ActionDeleteBucket, "b", "") {
		t.Fatalf("admin should authorize all actions")
	}

	reader := &Identity{Actions: []string{"Read"}}
	if !iam.IsAuthorized(reader, S3ActionGetObject, "b", "k") {
		t.Fatalf("Read should allow get")
	}
	if iam.IsAuthorized(reader, S3ActionPutObject, "b", "k") {
		t.Fatalf("Read should not allow put")
	}

	explicit := &Identity{Actions: []string{"s3:PutObject"}}
	if !iam.IsAuthorized(explicit, S3ActionPutObject, "b", "k") {
		t.Fatalf("explicit put should allow put")
	}
	if iam.IsAuthorized(explicit, S3ActionGetObject, "b", "k") {
		t.Fatalf("explicit put should not allow get")
	}

	scoped := &Identity{Actions: []string{"s3:GetObject:photos/*"}}
	if !iam.IsAuthorized(scoped, S3ActionGetObject, "photos", "a.jpg") {
		t.Fatalf("bucket scoped rule should allow matching object")
	}
	if iam.IsAuthorized(scoped, S3ActionGetObject, "docs", "a.jpg") {
		t.Fatalf("bucket scoped rule should deny non-matching bucket")
	}
}

func TestSaveAndLoadFromFiler(t *testing.T) {
	store := newMockFilerKV()
	iam, err := NewIdentityAccessManagement(store, nil)
	if err != nil {
		t.Fatalf("NewIdentityAccessManagement: %v", err)
	}

	cfg := &iampb.S3ApiConfiguration{Identities: []*iampb.Identity{{
		Name:        "u1",
		Credentials: []*iampb.Credential{{AccessKey: "AK", SecretKey: "SK"}},
		Actions:     []string{"*"},
	}}}
	if err := iam.Reload(cfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if err := iam.SaveToFiler(context.Background()); err != nil {
		t.Fatalf("SaveToFiler: %v", err)
	}

	iam2, err := NewIdentityAccessManagement(store, nil)
	if err != nil {
		t.Fatalf("NewIdentityAccessManagement(load): %v", err)
	}
	id, secret, ok := iam2.LookupByAccessKey("AK")
	if !ok || id == nil || id.Name != "u1" || secret != "SK" {
		t.Fatalf("reloaded config mismatch: ok=%v id=%v secret=%q", ok, id, secret)
	}
}

func TestValidateConfigurationRejectsDuplicateAccessKey(t *testing.T) {
	err := ValidateConfiguration(&iampb.S3ApiConfiguration{Identities: []*iampb.Identity{
		{Name: "a", Credentials: []*iampb.Credential{{AccessKey: "AK", SecretKey: "S1"}}},
		{Name: "b", Credentials: []*iampb.Credential{{AccessKey: "AK", SecretKey: "S2"}}},
	}})
	if err == nil {
		t.Fatalf("expected duplicate access key validation error")
	}
}

func TestDeriveSigningKeyChangesByDate(t *testing.T) {
	k1 := DeriveSigningKey("secret", "20260310", "us-east-1", "s3")
	k2 := DeriveSigningKey("secret", "20260311", "us-east-1", "s3")
	if len(k1) != 32 || len(k2) != 32 {
		t.Fatalf("unexpected key length: %d %d", len(k1), len(k2))
	}
	if string(k1) == string(k2) {
		t.Fatalf("signing key should vary by date")
	}
}
