package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// ValidateMasterConfig validates master configuration.
func ValidateMasterConfig(cfg *MasterConfig) error {
	// Validate peers: count must be 0 or odd (1, 3, 5); empty/["none"] means single-master
	if len(cfg.Peers) > 0 {
		hasNone := false
		for _, peer := range cfg.Peers {
			if peer == "none" {
				hasNone = true
				break
			}
		}
		if !hasNone && len(cfg.Peers)%2 == 0 {
			return fmt.Errorf("master peers count must be odd (Raft requirement), got %d", len(cfg.Peers))
		}
	}

	// Validate MetaDir: must be writable
	if cfg.MetaDir != "" {
		if err := os.MkdirAll(cfg.MetaDir, 0755); err != nil {
			return fmt.Errorf("failed to create meta dir %s: %w", cfg.MetaDir, err)
		}

		// Try to write a temp file
		testFile := filepath.Join(cfg.MetaDir, ".write_test")
		f, err := os.Create(testFile)
		if err != nil {
			return fmt.Errorf("meta dir %s is not writable: %w", cfg.MetaDir, err)
		}
		f.Close()
		os.Remove(testFile)
	}

	// Validate port ranges
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("invalid master port: %d", cfg.Port)
	}

	if cfg.GRPCPort < 1 || cfg.GRPCPort > 65535 {
		return fmt.Errorf("invalid master grpc port: %d", cfg.GRPCPort)
	}

	return nil
}

// ValidateVolumeConfig validates volume server configuration.
func ValidateVolumeConfig(cfg *VolumeServerConfig) error {
	// At least 1 directory required
	if len(cfg.Directories) == 0 {
		return fmt.Errorf("at least one directory must be configured")
	}

	// Validate IndexType
	validIndexTypes := map[string]bool{
		"memory": true, "leveldb": true, "leveldbMedium": true, "leveldbLarge": true,
	}
	if !validIndexTypes[cfg.IndexType] {
		return fmt.Errorf("invalid index_type: %s (must be memory, leveldb, leveldbMedium, or leveldbLarge)", cfg.IndexType)
	}

	// Validate ReadMode
	validReadModes := map[string]bool{
		"local": true, "proxy": true, "redirect": true,
	}
	if !validReadModes[cfg.ReadMode] {
		return fmt.Errorf("invalid read_mode: %s (must be local, proxy, or redirect)", cfg.ReadMode)
	}

	// Validate each directory
	for i, dir := range cfg.Directories {
		if dir.Path == "" {
			return fmt.Errorf("directory %d: path cannot be empty", i)
		}

		// Check if directory exists or can be created
		if err := os.MkdirAll(dir.Path, 0755); err != nil {
			return fmt.Errorf("directory %d: failed to create %s: %w", i, dir.Path, err)
		}

		// Validate MaxVolumeCount
		if dir.MaxVolumeCount <= 0 {
			return fmt.Errorf("directory %d: max_volume_count must be positive", i)
		}

		// Validate MinFreeSpacePercent
		if dir.MinFreeSpacePercent < 0 || dir.MinFreeSpacePercent > 100 {
			return fmt.Errorf("directory %d: min_free_space_percent must be between 0 and 100", i)
		}
	}

	// Validate masters
	if len(cfg.Masters) == 0 {
		return fmt.Errorf("at least one master address is required")
	}

	return nil
}

// ValidateFilerConfig validates filer configuration.
func ValidateFilerConfig(cfg *FilerConfig) error {
	// Validate masters
	if len(cfg.Masters) == 0 {
		return fmt.Errorf("at least one master address is required")
	}

	// Validate DefaultStoreDir
	if cfg.DefaultStoreDir != "" {
		if err := os.MkdirAll(cfg.DefaultStoreDir, 0755); err != nil {
			return fmt.Errorf("failed to create default_store_dir %s: %w", cfg.DefaultStoreDir, err)
		}
	}

	// Validate MaxFileSizeMB
	if cfg.MaxFileSizeMB <= 0 {
		return fmt.Errorf("max_file_size_mb must be positive")
	}

	// Validate MaxFilenameLength
	if cfg.MaxFilenameLength == 0 {
		return fmt.Errorf("max_filename_length must be positive")
	}

	// Validate port ranges
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("invalid filer port: %d", cfg.Port)
	}

	if cfg.GRPCPort < 1 || cfg.GRPCPort > 65535 {
		return fmt.Errorf("invalid filer grpc port: %d", cfg.GRPCPort)
	}

	return nil
}
