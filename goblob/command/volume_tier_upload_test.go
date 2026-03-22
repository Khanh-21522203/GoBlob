package command

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestVolumeTierUploadCommandValidation(t *testing.T) {
	cmd := &VolumeTierUploadCommand{}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected missing source dir error")
	}

	cmd = &VolumeTierUploadCommand{sourceDir: "/tmp", cloud: "s3"}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected missing bucket error")
	}

	cmd = &VolumeTierUploadCommand{sourceDir: "/tmp", cloud: "unknown", bucket: "b"}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected unsupported cloud error")
	}

	// azure/gcs should return a clear not-implemented error
	cmd = &VolumeTierUploadCommand{sourceDir: "/tmp", cloud: "azure", bucket: "b"}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected azure not-implemented error")
	}

	// apply without s3-endpoint should error
	cmd = &VolumeTierUploadCommand{sourceDir: "/tmp", cloud: "s3", bucket: "b", apply: true}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected missing s3-endpoint error")
	}
}

func TestVolumeTierUploadCommandDryRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.dat"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Dry-run should succeed without an endpoint.
	cmd := &VolumeTierUploadCommand{
		sourceDir: dir,
		cloud:     "s3",
		bucket:    "archive",
		apply:     false,
	}
	if err := cmd.Run(context.Background(), nil); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
}

func TestVolumeTierUploadCommandS3Upload(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.dat"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "b.dat"), []byte("world"), 0o644); err != nil {
		t.Fatalf("WriteFile nested: %v", err)
	}

	// Fake S3-compatible server that accepts all PUTs.
	var received []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			received = append(received, r.URL.Path)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	cmd := &VolumeTierUploadCommand{
		sourceDir:   dir,
		cloud:       "s3",
		bucket:      "archive",
		s3Endpoint:  srv.URL,
		apply:       true,
		concurrency: 2,
	}
	if err := cmd.Run(context.Background(), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(received) != 2 {
		t.Fatalf("expected 2 PUT requests, got %d: %v", len(received), received)
	}
}
