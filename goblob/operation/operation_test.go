package operation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"GoBlob/goblob/core/types"
)

func TestAssignSuccess(t *testing.T) {
	expected := AssignedFileId{
		Fid:       "3,0000000001637037000000d6",
		Url:       "127.0.0.1:8080",
		PublicUrl: "localhost:8080",
		Count:     1,
		Auth:      "jwt-token",
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
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	masterAddr := strings.TrimPrefix(srv.URL, "http://")
	result, err := Assign(context.Background(), masterAddr, &UploadOption{Collection: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Fid != expected.Fid {
		t.Fatalf("expected fid %q, got %q", expected.Fid, result.Fid)
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

func TestLookupVolumeWithCache(t *testing.T) {
	var hitCount int32
	expectedLocations := []VolumeLocation{
		{Url: "127.0.0.1:8080", PublicUrl: "localhost:8080"},
		{Url: "127.0.0.1:8081", PublicUrl: "localhost:8081"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitCount, 1)
		if r.URL.Path != "/dir/lookup" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		result := LookupResult{VolumeId: "3", Locations: expectedLocations}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer srv.Close()

	cache := NewVolumeLocationCache(time.Minute)
	masterAddr := strings.TrimPrefix(srv.URL, "http://")
	_, err := LookupVolumeId(context.Background(), masterAddr, types.VolumeId(3), cache)
	if err != nil {
		t.Fatalf("first lookup failed: %v", err)
	}
	_, err = LookupVolumeId(context.Background(), masterAddr, types.VolumeId(3), cache)
	if err != nil {
		t.Fatalf("second lookup failed: %v", err)
	}

	if got := atomic.LoadInt32(&hitCount); got != 1 {
		t.Fatalf("expected 1 master hit with cache, got %d", got)
	}
}

func TestVolumeLocationCacheTTL(t *testing.T) {
	cache := NewVolumeLocationCache(20 * time.Millisecond)
	vid := types.VolumeId(9)
	cache.Set(vid, []VolumeLocation{{Url: "a:1"}})
	if _, ok := cache.Get(vid); !ok {
		t.Fatal("expected cache hit before TTL")
	}
	time.Sleep(30 * time.Millisecond)
	if _, ok := cache.Get(vid); ok {
		t.Fatal("expected cache miss after TTL")
	}
}

func TestUploadSuccess(t *testing.T) {
	expected := UploadResult{
		Name:  "test.txt",
		Size:  13,
		ETag:  "abc123",
		Mime:  "text/plain",
		Fid:   "3,0000000001637037000000d6",
		Error: "",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Fatalf("expected multipart/form-data")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testtoken" {
			t.Fatalf("expected auth header, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	body := strings.NewReader("hello, world!")
	result, err := Upload(context.Background(), srv.URL, "test.txt", body, 13, false, "text/plain", nil, "testtoken")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ETag != expected.ETag {
		t.Fatalf("expected etag=%q got=%q", expected.ETag, result.ETag)
	}
}

func TestUploadWithRetry(t *testing.T) {
	var attempt int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&attempt, 1)
		if current < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"busy"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"size":5,"eTag":"ok"}`))
	}))
	defer srv.Close()

	result, err := UploadWithRetry(context.Background(), srv.URL+"/3,abc", "x.txt", []byte("hello"), "text/plain", "", 3)
	if err != nil {
		t.Fatalf("expected success, got err %v", err)
	}
	if result.ETag != "ok" {
		t.Fatalf("unexpected result %+v", result)
	}
	if got := atomic.LoadInt32(&attempt); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestDeleteSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("expected DELETE")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	if err := Delete(context.Background(), srv.URL+"/3,abc", "jwt"); err != nil {
		t.Fatalf("unexpected delete error: %v", err)
	}
}

func TestChunkUpload(t *testing.T) {
	var idSeq int32
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("expected PUT")
		}
		ct := r.Header.Get("Content-Type")
		mt, params, err := mime.ParseMediaType(ct)
		if err != nil || mt != "multipart/form-data" {
			t.Fatalf("expected multipart content type, got %q err=%v", ct, err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		part, err := mr.NextPart()
		if err != nil {
			t.Fatalf("read multipart part: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read chunk body: %v", err)
		}
		_ = part.Close()

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(UploadResult{
			Size: uint32(len(data)),
			ETag: fmt.Sprintf("etag-%d", len(data)),
		})
	}))
	defer uploadSrv.Close()

	assignSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next := atomic.AddInt32(&idSeq, 1)
		fid := fmt.Sprintf("3,%016x%08x", next, next)
		resp := AssignedFileId{
			Fid: fid,
			Url: strings.TrimPrefix(uploadSrv.URL, "http://"),
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer assignSrv.Close()

	data := bytesRepeat('a', 23)
	results, err := ChunkUpload(
		context.Background(),
		strings.TrimPrefix(assignSrv.URL, "http://"),
		strings.NewReader(string(data)),
		int64(len(data)),
		8,
		&UploadOption{},
	)
	if err != nil {
		t.Fatalf("chunk upload failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(results))
	}
	if results[0].Offset != 0 || results[1].Offset != 8 || results[2].Offset != 16 {
		t.Fatalf("unexpected chunk offsets: %+v", results)
	}
}

func bytesRepeat(ch byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = ch
	}
	return out
}
