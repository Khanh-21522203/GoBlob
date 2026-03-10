package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMaintenanceScriptDefault(t *testing.T) {
	script, err := loadMaintenanceScript("")
	if err != nil {
		t.Fatalf("loadMaintenanceScript returned error: %v", err)
	}
	if !strings.Contains(script, "volume.balance") {
		t.Fatalf("default maintenance script missing volume.balance: %q", script)
	}
}

func TestLoadMaintenanceScriptFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "maintenance.txt")
	want := "volume.vacuum\ns3.clean.uploads -olderThan=1h\n"
	if err := os.WriteFile(p, []byte(want), 0644); err != nil {
		t.Fatalf("write script file: %v", err)
	}
	got, err := loadMaintenanceScript(p)
	if err != nil {
		t.Fatalf("loadMaintenanceScript returned error: %v", err)
	}
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Fatalf("unexpected script content: %q want %q", got, want)
	}
}
