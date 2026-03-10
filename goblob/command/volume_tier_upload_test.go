package command

import (
	"context"
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

	cmd = &VolumeTierUploadCommand{sourceDir: "/tmp", cloud: "s3", bucket: "b", apply: true}
	if err := cmd.Run(context.Background(), nil); err == nil {
		t.Fatal("expected missing output dir error")
	}
}

func TestVolumeTierUploadCommandRun(t *testing.T) {
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

	cmd := &VolumeTierUploadCommand{
		sourceDir: dir,
		cloud:     "s3",
		bucket:    "archive",
		apply:     true,
		outputDir: t.TempDir(),
	}
	if err := cmd.Run(context.Background(), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cmd.outputDir, "s3", "archive", "a.dat")); err != nil {
		t.Fatalf("expected copied file a.dat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cmd.outputDir, "s3", "archive", "nested", "b.dat")); err != nil {
		t.Fatalf("expected copied file nested/b.dat: %v", err)
	}
}
