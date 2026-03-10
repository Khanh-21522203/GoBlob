//go:build integration

package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/filer"
	"GoBlob/goblob/filer/storeloader"
	"GoBlob/goblob/pb/volume_server_pb"
	"GoBlob/goblob/s3api"
	"GoBlob/goblob/server"
)

func TestEndToEndBlobUploadDownload(t *testing.T) {
	adminMux := http.NewServeMux()
	publicMux := http.NewServeMux()
	opt := server.DefaultVolumeServerOption()
	opt.Masters = nil
	opt.Directories = []server.DiskDirectoryConfig{{Path: t.TempDir(), MaxVolumeCount: 8}}
	vs, err := server.NewVolumeServer(adminMux, publicMux, nil, opt)
	if err != nil {
		t.Fatalf("NewVolumeServer: %v", err)
	}
	defer vs.Shutdown()

	grpcSvc := server.NewVolumeGRPCServer(vs)
	if _, err := grpcSvc.AllocateVolume(context.Background(), &volume_server_pb.AllocateVolumeRequest{VolumeId: 1, Collection: "default"}); err != nil {
		t.Fatalf("AllocateVolume: %v", err)
	}

	fid := types.FileId{VolumeId: 1, NeedleId: 1001, Cookie: 42}.String()
	putReq := httptest.NewRequest(http.MethodPut, "/"+fid, bytes.NewReader([]byte("phase7-data")))
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putRec := httptest.NewRecorder()
	adminMux.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusCreated {
		t.Fatalf("PUT expected 201, got %d body=%s", putRec.Code, putRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/"+fid, nil)
	getRec := httptest.NewRecorder()
	adminMux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d", getRec.Code)
	}
	if got, want := getRec.Body.String(), "phase7-data"; got != want {
		t.Fatalf("GET body=%q want %q", got, want)
	}
}

func TestFilerMetadataOps(t *testing.T) {
	defaultMux := http.NewServeMux()
	readonlyMux := http.NewServeMux()
	opt := server.DefaultFilerOption()
	opt.Masters = nil
	opt.StoreBackend = "leveldb2"
	opt.DefaultStoreDir = t.TempDir()

	fs, err := server.NewFilerServer(defaultMux, readonlyMux, nil, opt)
	if err != nil {
		t.Fatalf("NewFilerServer: %v", err)
	}
	defer fs.Shutdown()

	store, err := storeloader.LoadFilerStoreFromConfig(map[string]string{
		"backend":      "leveldb2",
		"leveldb2.dir": opt.DefaultStoreDir,
	})
	if err != nil {
		t.Fatalf("LoadFilerStoreFromConfig: %v", err)
	}
	if err := store.InsertEntry(context.Background(), &filer.Entry{
		FullPath: "/integration",
		Attr: filer.Attr{
			Mode:  os.ModeDir | 0o755,
			Mtime: time.Now(),
		},
	}); err != nil {
		t.Fatalf("create parent directory: %v", err)
	}
	fs.SetStore(store)
	fs.Start()

	createReq := httptest.NewRequest(http.MethodPost, "/integration/file.txt", bytes.NewReader([]byte("hello")))
	createRec := httptest.NewRecorder()
	defaultMux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create expected 201, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/integration/file.txt", nil)
	listRec := httptest.NewRecorder()
	defaultMux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("get expected 200, got %d", listRec.Code)
	}
	body, _ := io.ReadAll(listRec.Body)
	if string(body) != "hello" {
		t.Fatalf("unexpected filer body: %q", string(body))
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/integration/file.txt", nil)
	delRec := httptest.NewRecorder()
	defaultMux.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete expected 204, got %d", delRec.Code)
	}
}

func TestS3HealthEndpoints(t *testing.T) {
	mux := http.NewServeMux()
	s3, err := s3api.NewS3ApiServer(mux, &s3api.S3ApiServerOption{BucketsPath: "/buckets"})
	if err != nil {
		t.Fatalf("NewS3ApiServer: %v", err)
	}
	defer s3.Shutdown()

	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRec := httptest.NewRecorder()
	mux.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health expected 200, got %d", healthRec.Code)
	}

	readyReq := httptest.NewRequest(http.MethodGet, "/ready", nil)
	readyRec := httptest.NewRecorder()
	mux.ServeHTTP(readyRec, readyReq)
	if readyRec.Code != http.StatusOK {
		t.Fatalf("ready expected 200, got %d", readyRec.Code)
	}
}
