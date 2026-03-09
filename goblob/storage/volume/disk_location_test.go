package volume

import (
	"os"
	"path/filepath"
	"testing"

	"GoBlob/goblob/core/types"
)

func TestNewDiskLocation(t *testing.T) {
	tmpDir := t.TempDir()

	dl, err := NewDiskLocation(tmpDir, 100)
	if err != nil {
		t.Fatalf("Failed to create disk location: %v", err)
	}
	defer dl.Close()

	if dl.Directory() != tmpDir {
		t.Errorf("Expected directory %s, got %s", tmpDir, dl.Directory())
	}

	if dl.MaxVolumeCount() != 100 {
		t.Errorf("Expected max volume count 100, got %d", dl.MaxVolumeCount())
	}

	if dl.VolumeCount() != 0 {
		t.Errorf("Expected initial volume count 0, got %d", dl.VolumeCount())
	}
}

func TestDiskLocationHasSpace(t *testing.T) {
	tmpDir := t.TempDir()

	dl, err := NewDiskLocation(tmpDir, 5)
	if err != nil {
		t.Fatalf("Failed to create disk location: %v", err)
	}
	defer dl.Close()

	// Initially should have space
	if !dl.HasSpace() {
		t.Error("Expected disk location to have space initially")
	}

	// Add volumes up to limit using AddVolumeObject
	for i := 1; i <= 4; i++ {
		v, err := NewVolume(tmpDir, "test", types.VolumeId(i), types.NeedleVersionV3)
		if err != nil {
			t.Fatalf("Failed to create volume %d: %v", i, err)
		}
		dl.AddVolumeObject(v)
	}

	// Should still have space (under limit)
	if !dl.HasSpace() {
		t.Error("Expected disk location to have space under limit")
	}

	// Add one more to reach limit
	v5, err := NewVolume(tmpDir, "test", types.VolumeId(5), types.NeedleVersionV3)
	if err != nil {
		t.Fatalf("Failed to create volume 5: %v", err)
	}
	dl.AddVolumeObject(v5)

	// Should not have space at limit
	if dl.HasSpace() {
		t.Error("Expected disk location to not have space at limit")
	}
}

func TestDiskLocationGetVolumePath(t *testing.T) {
	tmpDir := t.TempDir()

	dl, err := NewDiskLocation(tmpDir, 100)
	if err != nil {
		t.Fatalf("Failed to create disk location: %v", err)
	}
	defer dl.Close()

	id := types.VolumeId(1)

	expectedPath := filepath.Join(tmpDir, "1.dat")
	if dl.GetVolumePath(id) != expectedPath {
		t.Errorf("Expected path %s, got %s", expectedPath, dl.GetVolumePath(id))
	}

	expectedIndexPath := filepath.Join(tmpDir, "1.idx")
	if dl.GetIndexPath(id) != expectedIndexPath {
		t.Errorf("Expected path %s, got %s", expectedIndexPath, dl.GetIndexPath(id))
	}
}

func TestDiskLocationVolumeExists(t *testing.T) {
	tmpDir := t.TempDir()

	dl, err := NewDiskLocation(tmpDir, 100)
	if err != nil {
		t.Fatalf("Failed to create disk location: %v", err)
	}
	defer dl.Close()

	id := types.VolumeId(1)

	// Initially should not exist
	if dl.VolumeExists(id) {
		t.Error("Expected volume to not exist initially")
	}

	// Create a volume file
	volumePath := dl.GetVolumePath(id)
	file, err := os.Create(volumePath)
	if err != nil {
		t.Fatalf("Failed to create volume file: %v", err)
	}
	file.Close()

	// Now should exist
	if !dl.VolumeExists(id) {
		t.Error("Expected volume to exist after creation")
	}

	// Delete the file
	os.Remove(volumePath)

	// Should not exist again
	if dl.VolumeExists(id) {
		t.Error("Expected volume to not exist after deletion")
	}
}

func TestDiskLocationDeleteVolume(t *testing.T) {
	tmpDir := t.TempDir()

	dl, err := NewDiskLocation(tmpDir, 100)
	if err != nil {
		t.Fatalf("Failed to create disk location: %v", err)
	}

	id := types.VolumeId(1)

	// Create a volume via NewVolume
	v, err := NewVolume(tmpDir, "test", id, types.NeedleVersionV3)
	if err != nil {
		t.Fatalf("Failed to create volume: %v", err)
	}
	dl.AddVolumeObject(v)

	volumePath := dl.GetVolumePath(id)
	indexPath := dl.GetIndexPath(id)

	initialCount := dl.VolumeCount()

	// Delete the volume
	if err := dl.DeleteVolume(id); err != nil {
		t.Fatalf("Failed to delete volume: %v", err)
	}

	// Verify files are deleted
	if _, err := os.Stat(volumePath); !os.IsNotExist(err) {
		t.Error("Expected volume file to be deleted")
	}

	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Error("Expected index file to be deleted")
	}

	// Verify volume count decreased
	if dl.VolumeCount() >= initialCount {
		t.Errorf("Expected volume count to decrease, got %d (was %d)", dl.VolumeCount(), initialCount)
	}
}

func TestDiskLocationGetVolumes(t *testing.T) {
	tmpDir := t.TempDir()

	dl, err := NewDiskLocation(tmpDir, 100)
	if err != nil {
		t.Fatalf("Failed to create disk location: %v", err)
	}
	defer dl.Close()

	// Create some volumes via NewVolume + AddVolumeObject
	for i := 1; i <= 5; i++ {
		v, err := NewVolume(tmpDir, "test", types.VolumeId(i), types.NeedleVersionV3)
		if err != nil {
			t.Fatalf("Failed to create volume %d: %v", i, err)
		}
		dl.AddVolumeObject(v)
	}

	// Get volumes
	volumes := dl.GetVolumes()

	if len(volumes) != 5 {
		t.Errorf("Expected 5 volumes, got %d", len(volumes))
	}

	// Verify they're sorted
	for i := 0; i < len(volumes)-1; i++ {
		if volumes[i] >= volumes[i+1] {
			t.Errorf("Volumes not sorted: %d >= %d", volumes[i], volumes[i+1])
		}
	}
}

func TestDiskLocationSetReadOnly(t *testing.T) {
	tmpDir := t.TempDir()

	dl, err := NewDiskLocation(tmpDir, 100)
	if err != nil {
		t.Fatalf("Failed to create disk location: %v", err)
	}
	defer dl.Close()

	// Initially not read-only
	if dl.IsReadOnly() {
		t.Error("Expected disk location to not be read-only initially")
	}

	// Set read-only
	dl.SetReadOnly(true)

	if !dl.IsReadOnly() {
		t.Error("Expected disk location to be read-only after SetReadOnly(true)")
	}

	// Try to delete volume while read-only
	id := types.VolumeId(1)

	// Create the volume file directly so DeleteVolume can try
	volumePath := dl.GetVolumePath(id)
	file, _ := os.Create(volumePath)
	file.Close()

	err = dl.DeleteVolume(id)
	if err == nil {
		t.Error("Expected error when deleting volume from read-only disk location")
	}
}

func TestDiskLocationUsedSize(t *testing.T) {
	tmpDir := t.TempDir()

	dl, err := NewDiskLocation(tmpDir, 100)
	if err != nil {
		t.Fatalf("Failed to create disk location: %v", err)
	}
	defer dl.Close()

	// Initially should have 0 used size
	if dl.UsedSize() != 0 {
		t.Errorf("Expected initial used size 0, got %d", dl.UsedSize())
	}

	// Create a volume file with some data
	id := types.VolumeId(1)
	volumePath := dl.GetVolumePath(id)

	data := make([]byte, 1024)
	if err := os.WriteFile(volumePath, data, 0644); err != nil {
		t.Fatalf("Failed to write volume file: %v", err)
	}

	// Rescan to update used size
	dl.calculateUsedSize()

	// Should have some used size now
	if dl.UsedSize() == 0 {
		t.Error("Expected non-zero used size after creating file")
	}
}

func TestDiskLocationFreeSpace(t *testing.T) {
	tmpDir := t.TempDir()

	dl, err := NewDiskLocation(tmpDir, 100)
	if err != nil {
		t.Fatalf("Failed to create disk location: %v", err)
	}
	defer dl.Close()

	// Free space should be reasonable
	freeSpace := dl.FreeSpace()
	if freeSpace == 0 {
		t.Error("Expected some free space")
	}

	if freeSpace > dl.MaxSize() {
		t.Errorf("Free space (%d) cannot exceed max size (%d)", freeSpace, dl.MaxSize())
	}
}

func TestDiskLocationAddRemoveVolume(t *testing.T) {
	tmpDir := t.TempDir()

	dl, err := NewDiskLocation(tmpDir, 10)
	if err != nil {
		t.Fatalf("Failed to create disk location: %v", err)
	}
	defer dl.Close()

	id := types.VolumeId(1)

	// Create volume using NewVolume + AddVolumeObject
	v, err := NewVolume(tmpDir, "test", id, types.NeedleVersionV3)
	if err != nil {
		t.Fatalf("Failed to create volume: %v", err)
	}
	dl.AddVolumeObject(v)

	volumePath := dl.GetVolumePath(id)

	initialCount := dl.VolumeCount()
	if initialCount != 1 {
		t.Errorf("Expected initial volume count 1, got %d", initialCount)
	}

	// Delete the volume
	if err := dl.DeleteVolume(id); err != nil {
		t.Fatalf("Failed to delete volume: %v", err)
	}

	// Verify volume count decreased
	if dl.VolumeCount() != 0 {
		t.Errorf("Expected volume count 0 after deletion, got %d", dl.VolumeCount())
	}

	// Verify files are deleted
	if _, err := os.Stat(volumePath); !os.IsNotExist(err) {
		t.Error("Expected volume file to be deleted")
	}
}

func TestDiskLocationString(t *testing.T) {
	tmpDir := t.TempDir()

	dl, err := NewDiskLocation(tmpDir, 100)
	if err != nil {
		t.Fatalf("Failed to create disk location: %v", err)
	}
	defer dl.Close()

	str := dl.String()
	if str == "" {
		t.Error("Expected non-empty string representation")
	}

	// Should contain directory
	if !contains(str, tmpDir) {
		t.Errorf("Expected string to contain directory %s", tmpDir)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsInner(s, substr)))
}

func containsInner(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
