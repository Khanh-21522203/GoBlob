package replication

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"GoBlob/goblob/core/types"
)

func TestReplicatedWriteNoReplicas(t *testing.T) {
	r := NewHTTPReplicator()
	req := ReplicateRequest{
		VolumeId:    1,
		NeedleId:    42,
		NeedleBytes: []byte("data"),
		JWTToken:    "",
	}
	err := r.ReplicatedWrite(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("expected nil error for empty replica list, got: %v", err)
	}
}

func TestReplicatedWriteSuccess(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	srv1 := httptest.NewServer(handler)
	defer srv1.Close()
	srv2 := httptest.NewServer(handler)
	defer srv2.Close()

	r := NewHTTPReplicator()
	req := ReplicateRequest{
		VolumeId:    2,
		NeedleId:    100,
		NeedleBytes: []byte("needle-data"),
		JWTToken:    "test-token",
	}

	addrs := []types.ServerAddress{
		types.ServerAddress(srv1.Listener.Addr().String()),
		types.ServerAddress(srv2.Listener.Addr().String()),
	}

	err := r.ReplicatedWrite(context.Background(), req, addrs)
	if err != nil {
		t.Fatalf("expected nil error when all replicas succeed, got: %v", err)
	}
}

func TestReplicatedWriteOneFailure(t *testing.T) {
	goodHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	badHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	goodSrv := httptest.NewServer(goodHandler)
	defer goodSrv.Close()
	badSrv := httptest.NewServer(badHandler)
	defer badSrv.Close()

	r := NewHTTPReplicator()
	req := ReplicateRequest{
		VolumeId:    3,
		NeedleId:    200,
		NeedleBytes: []byte("needle-data"),
	}

	addrs := []types.ServerAddress{
		types.ServerAddress(goodSrv.Listener.Addr().String()),
		types.ServerAddress(badSrv.Listener.Addr().String()),
	}

	err := r.ReplicatedWrite(context.Background(), req, addrs)
	if err == nil {
		t.Fatal("expected non-nil error when one replica returns 500, got nil")
	}
}

func TestReplicaLocationsUpdateGet(t *testing.T) {
	rl := NewReplicaLocations()
	vid := types.VolumeId(7)
	addrs := []types.ServerAddress{"host1:8080", "host2:8080"}

	rl.Update(vid, addrs)
	got := rl.Get(vid)

	if len(got) != len(addrs) {
		t.Fatalf("expected %d addresses, got %d", len(addrs), len(got))
	}
	for i, a := range addrs {
		if got[i] != a {
			t.Errorf("address[%d]: expected %s, got %s", i, a, got[i])
		}
	}
}

func TestReplicaLocationsEmpty(t *testing.T) {
	rl := NewReplicaLocations()
	got := rl.Get(types.VolumeId(999))
	if got != nil {
		t.Fatalf("expected nil for unknown VolumeId, got: %v", got)
	}
}
