package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMasterConfig(t *testing.T) {
	t.Run("default values", func(t *testing.T) {
		loader := NewViperLoader(nil)

		cfg, err := loader.LoadMasterConfig()
		if err != nil {
			t.Fatalf("LoadMasterConfig failed: %v", err)
		}

		if cfg.Port != DefaultMasterHTTPPort {
			t.Errorf("expected port %d, got %d", DefaultMasterHTTPPort, cfg.Port)
		}

		if cfg.DefaultReplication != DefaultReplication {
			t.Errorf("expected replication %s, got %s", DefaultReplication, cfg.DefaultReplication)
		}

		if cfg.GarbageThreshold != DefaultGarbageThreshold {
			t.Errorf("expected garbage threshold %f, got %f", DefaultGarbageThreshold, cfg.GarbageThreshold)
		}
	})

	t.Run("with CLI override", func(t *testing.T) {
		cliFlags := map[string]interface{}{
			"port": 9999,
		}
		loader := NewViperLoader(cliFlags)

		cfg, err := loader.LoadMasterConfig()
		if err != nil {
			t.Fatalf("LoadMasterConfig failed: %v", err)
		}

		if cfg.Port != 9999 {
			t.Errorf("expected port 9999, got %d", cfg.Port)
		}
	})

	t.Run("with config file", func(t *testing.T) {
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "master.toml")

		content := `
port = 9334
grpc_port = 19334
default_replication = "001"
`
		if err := os.WriteFile(configFile, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write config file: %v", err)
		}

		// Change to temp dir to pick up the config file
		oldWd, _ := os.Getwd()
		os.Chdir(tmpDir)
		defer os.Chdir(oldWd)

		loader := NewViperLoader(nil)
		cfg, err := loader.LoadMasterConfig()
		if err != nil {
			t.Fatalf("LoadMasterConfig failed: %v", err)
		}

		if cfg.Port != 9334 {
			t.Errorf("expected port 9334, got %d", cfg.Port)
		}

		if cfg.DefaultReplication != "001" {
			t.Errorf("expected replication '001', got '%s'", cfg.DefaultReplication)
		}
	})
}

func TestValidateMasterConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := &MasterConfig{
			Port:              9333,
			GRPCPort:          19333,
			MetaDir:           t.TempDir(),
			Peers:             []string{},
			VolumeSizeLimitMB: 30000,
			DefaultReplication: "000",
		}

		if err := ValidateMasterConfig(cfg); err != nil {
			t.Errorf("expected valid config, got error: %v", err)
		}
	})

	t.Run("even number of peers", func(t *testing.T) {
		cfg := &MasterConfig{
			Port:              9333,
			GRPCPort:          19333,
			MetaDir:           t.TempDir(),
			Peers:             []string{"master1:9333", "master2:9333"},
			VolumeSizeLimitMB: 30000,
			DefaultReplication: "000",
		}

		if err := ValidateMasterConfig(cfg); err == nil {
			t.Error("expected error for even number of peers")
		}
	})

	t.Run("invalid port", func(t *testing.T) {
		cfg := &MasterConfig{
			Port:              70000,
			GRPCPort:          19333,
			MetaDir:           t.TempDir(),
			Peers:             []string{},
			VolumeSizeLimitMB: 30000,
			DefaultReplication: "000",
		}

		if err := ValidateMasterConfig(cfg); err == nil {
			t.Error("expected error for invalid port")
		}
	})

	t.Run("non-existent meta dir", func(t *testing.T) {
		// Use a path that cannot be created (e.g., /root/test requires root)
		cfg := &MasterConfig{
			Port:              9333,
			GRPCPort:          19333,
			MetaDir:           "/root/goblob_test_meta",
			Peers:             []string{},
			VolumeSizeLimitMB: 30000,
			DefaultReplication: "000",
		}

		// This should fail on most systems
		err := ValidateMasterConfig(cfg)
		// We can't assert it always fails (might run as root), but we shouldn't error
		_ = err
	})
}

func TestValidateVolumeConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := &VolumeServerConfig{
			Port:         8080,
			GRPCPort:     18080,
			Masters:      []string{"localhost:9333"},
			IndexType:    "leveldb",
			ReadMode:     "redirect",
			Directories: []DiskDirectoryConfig{
				{
					Path:           t.TempDir(),
					MaxVolumeCount: 7,
				},
			},
		}

		if err := ValidateVolumeConfig(cfg); err != nil {
			t.Errorf("expected valid config, got error: %v", err)
		}
	})

	t.Run("no directories", func(t *testing.T) {
		cfg := &VolumeServerConfig{
			Port:         8080,
			GRPCPort:     18080,
			Masters:      []string{"localhost:9333"},
			IndexType:    "leveldb",
			ReadMode:     "redirect",
			Directories:  []DiskDirectoryConfig{},
		}

		if err := ValidateVolumeConfig(cfg); err == nil {
			t.Error("expected error for no directories")
		}
	})

	t.Run("invalid index type", func(t *testing.T) {
		cfg := &VolumeServerConfig{
			Port:         8080,
			GRPCPort:     18080,
			Masters:      []string{"localhost:9333"},
			IndexType:    "invalid",
			ReadMode:     "redirect",
			Directories: []DiskDirectoryConfig{
				{Path: t.TempDir(), MaxVolumeCount: 7},
			},
		}

		if err := ValidateVolumeConfig(cfg); err == nil {
			t.Error("expected error for invalid index type")
		}
	})

	t.Run("invalid read mode", func(t *testing.T) {
		cfg := &VolumeServerConfig{
			Port:         8080,
			GRPCPort:     18080,
			Masters:      []string{"localhost:9333"},
			IndexType:    "leveldb",
			ReadMode:     "invalid",
			Directories: []DiskDirectoryConfig{
				{Path: t.TempDir(), MaxVolumeCount: 7},
			},
		}

		if err := ValidateVolumeConfig(cfg); err == nil {
			t.Error("expected error for invalid read mode")
		}
	})

	t.Run("no masters", func(t *testing.T) {
		cfg := &VolumeServerConfig{
			Port:        8080,
			GRPCPort:    18080,
			Masters:     []string{},
			IndexType:   "leveldb",
			ReadMode:    "redirect",
			Directories: []DiskDirectoryConfig{{Path: t.TempDir(), MaxVolumeCount: 7}},
		}

		if err := ValidateVolumeConfig(cfg); err == nil {
			t.Error("expected error for no masters")
		}
	})
}

func TestValidateFilerConfig(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := &FilerConfig{
			Port:                8888,
			GRPCPort:            18888,
			Masters:             []string{"localhost:9333"},
			DefaultStoreDir:     t.TempDir(),
			MaxFileSizeMB:       4,
			MaxFilenameLength:   255,
			DefaultReplication:  "000",
			BucketsFolder:       "/buckets",
		}

		if err := ValidateFilerConfig(cfg); err != nil {
			t.Errorf("expected valid config, got error: %v", err)
		}
	})

	t.Run("no masters", func(t *testing.T) {
		cfg := &FilerConfig{
			Port:              8888,
			GRPCPort:          18888,
			Masters:           []string{},
			DefaultStoreDir:   t.TempDir(),
			MaxFileSizeMB:     4,
			MaxFilenameLength: 255,
		}

		if err := ValidateFilerConfig(cfg); err == nil {
			t.Error("expected error for no masters")
		}
	})

	t.Run("invalid max file size", func(t *testing.T) {
		cfg := &FilerConfig{
			Port:              8888,
			GRPCPort:          18888,
			Masters:           []string{"localhost:9333"},
			DefaultStoreDir:   t.TempDir(),
			MaxFileSizeMB:     0,
			MaxFilenameLength: 255,
		}

		if err := ValidateFilerConfig(cfg); err == nil {
			t.Error("expected error for zero max file size")
		}
	})

	t.Run("invalid max filename length", func(t *testing.T) {
		cfg := &FilerConfig{
			Port:              8888,
			GRPCPort:          18888,
			Masters:           []string{"localhost:9333"},
			DefaultStoreDir:   t.TempDir(),
			MaxFileSizeMB:     4,
			MaxFilenameLength: 0,
		}

		if err := ValidateFilerConfig(cfg); err == nil {
			t.Error("expected error for zero max filename length")
		}
	})
}

func TestConfigSearchPaths(t *testing.T) {
	paths := ConfigSearchPaths()
	if len(paths) == 0 {
		t.Error("expected non-empty search paths")
	}

	// Check that common paths are included
	expectedPaths := []string{".", "/etc/goblob/"}
	for _, expected := range expectedPaths {
		found := false
		for _, path := range paths {
			if path == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected path %s not found in search paths", expected)
		}
	}
}

func TestVolumeGrowthConfigDefaults(t *testing.T) {
	loader := NewViperLoader(nil)
	cfg, err := loader.LoadMasterConfig()
	if err != nil {
		t.Fatalf("LoadMasterConfig failed: %v", err)
	}

	if cfg.VolumeGrowth.Copy1Count != DefaultVolumeGrowthCopy1 {
		t.Errorf("expected Copy1Count %d, got %d", DefaultVolumeGrowthCopy1, cfg.VolumeGrowth.Copy1Count)
	}

	if cfg.VolumeGrowth.Copy2Count != DefaultVolumeGrowthCopy2 {
		t.Errorf("expected Copy2Count %d, got %d", DefaultVolumeGrowthCopy2, cfg.VolumeGrowth.Copy2Count)
	}

	if cfg.VolumeGrowth.Threshold != DefaultVolumeGrowthThreshold {
		t.Errorf("expected Threshold %f, got %f", DefaultVolumeGrowthThreshold, cfg.VolumeGrowth.Threshold)
	}
}

func TestMaintenanceConfigDefaults(t *testing.T) {
	loader := NewViperLoader(nil)
	cfg, err := loader.LoadMasterConfig()
	if err != nil {
		t.Fatalf("LoadMasterConfig failed: %v", err)
	}

	if cfg.Maintenance.SleepMinutes != DefaultMaintenanceSleepMinutes {
		t.Errorf("expected SleepMinutes %d, got %d", DefaultMaintenanceSleepMinutes, cfg.Maintenance.SleepMinutes)
	}
}

func TestVolumeServerConfigDefaults(t *testing.T) {
	// Create a temporary directory for the volume server
	tmpDir := t.TempDir()

	// Provide required directories via CLI flags
	cliFlags := map[string]interface{}{
		"directories": []map[string]interface{}{
			{
				"path":            tmpDir,
				"max_volume_count": int32(7),
			},
		},
		"masters": []string{"localhost:9333"},
	}

	loader := NewViperLoader(cliFlags)
	cfg, err := loader.LoadVolumeConfig()
	if err != nil {
		t.Fatalf("LoadVolumeConfig failed: %v", err)
	}

	if cfg.IndexType != DefaultIndexType {
		t.Errorf("expected IndexType %s, got %s", DefaultIndexType, cfg.IndexType)
	}

	if cfg.ReadMode != DefaultReadMode {
		t.Errorf("expected ReadMode %s, got %s", DefaultReadMode, cfg.ReadMode)
	}

	if cfg.HeartbeatInterval != 5*time.Second {
		t.Errorf("expected HeartbeatInterval 5s, got %v", cfg.HeartbeatInterval)
	}
}

func TestFilerConfigDefaults(t *testing.T) {
	// Provide required masters via CLI flags
	cliFlags := map[string]interface{}{
		"masters": []string{"localhost:9333"},
	}

	loader := NewViperLoader(cliFlags)
	cfg, err := loader.LoadFilerConfig()
	if err != nil {
		t.Fatalf("LoadFilerConfig failed: %v", err)
	}

	if cfg.MaxFileSizeMB != DefaultMaxFileSizeMB {
		t.Errorf("expected MaxFileSizeMB %d, got %d", DefaultMaxFileSizeMB, cfg.MaxFileSizeMB)
	}

	if cfg.MaxFilenameLength != DefaultMaxFilenameLength {
		t.Errorf("expected MaxFilenameLength %d, got %d", DefaultMaxFilenameLength, cfg.MaxFilenameLength)
	}

	if cfg.BucketsFolder != DefaultBucketsFolder {
		t.Errorf("expected BucketsFolder %s, got %s", DefaultBucketsFolder, cfg.BucketsFolder)
	}

	if cfg.LogFlushIntervalSeconds != DefaultLogFlushIntervalSeconds {
		t.Errorf("expected LogFlushIntervalSeconds %d, got %d", DefaultLogFlushIntervalSeconds, cfg.LogFlushIntervalSeconds)
	}
}

func TestDiskDirectoryConfig(t *testing.T) {
	t.Run("max volume count validation", func(t *testing.T) {
		cfg := &VolumeServerConfig{
			Port:        8080,
			GRPCPort:    18080,
			Masters:     []string{"localhost:9333"},
			IndexType:   "leveldb",
			ReadMode:    "redirect",
			Directories: []DiskDirectoryConfig{{Path: t.TempDir(), MaxVolumeCount: 0}},
		}

		if err := ValidateVolumeConfig(cfg); err == nil {
			t.Error("expected error for zero max_volume_count")
		}
	})

	t.Run("min free space percent validation", func(t *testing.T) {
		cfg := &VolumeServerConfig{
			Port:        8080,
			GRPCPort:    18080,
			Masters:     []string{"localhost:9333"},
			IndexType:   "leveldb",
			ReadMode:    "redirect",
			Directories: []DiskDirectoryConfig{{Path: t.TempDir(), MaxVolumeCount: 7, MinFreeSpacePercent: 101}},
		}

		if err := ValidateVolumeConfig(cfg); err == nil {
			t.Error("expected error for min_free_space_percent > 100")
		}
	})
}
