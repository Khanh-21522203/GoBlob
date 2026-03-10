package operation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"GoBlob/goblob/core/types"
)

func TestAssignSuccess(t *testing.T) {
	expected := AssignedFileId{
		Fid:       "3,01637037d6",
		Url:       "127.0.0.1:8080",
		PublicUrl: "localhost:8080",
		Count:     1,
		Auth:      "",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/dir/assign" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	// Strip http:// from test server URL
	masterAddr := strings.TrimPrefix(srv.URL, "http://")

	result, err := Assign(context.Background(), masterAddr, &AssignOption{Collection: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Fid != expected.Fid {
		t.Errorf("expected Fid %q, got %q", expected.Fid, result.Fid)
	}
	if result.Url != expected.Url {
		t.Errorf("expected Url %q, got %q", expected.Url, result.Url)
	}
}

func TestAssignNoWritableVolumes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	masterAddr := strings.TrimPrefix(srv.URL, "http://")

	_, err := Assign(context.Background(), masterAddr, nil)
	if err != ErrNoWritableVolumes {
		t.Fatalf("expected ErrNoWritableVolumes, got %v", err)
	}
}

func TestLookupSuccess(t *testing.T) {
	expectedLocations := []VolumeLocation{
		{Url: "127.0.0.1:8080", PublicUrl: "localhost:8080"},
		{Url: "127.0.0.1:8081", PublicUrl: "localhost:8081"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/dir/lookup" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("volumeId") != "3" {
			t.Errorf("unexpected volumeId: %s", r.URL.Query().Get("volumeId"))
		}
		result := LookupResult{
			VolumeOrFileId: "3",
			Locations:      expectedLocations,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result)
	}))
	defer srv.Close()

	masterAddr := strings.TrimPrefix(srv.URL, "http://")

	locations, err := LookupVolumeId(context.Background(), masterAddr, types.VolumeId(3))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(locations) != 2 {
		t.Fatalf("expected 2 locations, got %d", len(locations))
	}
	if locations[0].Url != expectedLocations[0].Url {
		t.Errorf("expected Url %q, got %q", expectedLocations[0].Url, locations[0].Url)
	}
}

func TestUploadSuccess(t *testing.T) {
	expected := UploadResult{
		Name:  "test.txt",
		Size:  13,
		ETag:  "abc123",
		Mime:  "text/plain",
		Fid:   "3,01637037d6",
		Error: "",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("expected multipart/form-data, got %s", ct)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer testtoken" {
			t.Errorf("expected Authorization 'Bearer testtoken', got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	body := strings.NewReader("hello, world!")
	result, err := Upload(context.Background(), srv.URL, "test.txt", body, 13, "text/plain", "testtoken")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != expected.Name {
		t.Errorf("expected Name %q, got %q", expected.Name, result.Name)
	}
	if result.Size != expected.Size {
		t.Errorf("expected Size %d, got %d", expected.Size, result.Size)
	}
	if result.ETag != expected.ETag {
		t.Errorf("expected ETag %q, got %q", expected.ETag, result.ETag)
	}
}
