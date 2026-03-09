package volume

import (
	"os"
	"path/filepath"
	"testing"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/storage/needle"
)

func setupTestVolume(t *testing.T) (*Volume, string) {
	t.Helper()

	// Create temp directory
	tmpDir := t.TempDir()

	// Create volume
	v, err := NewVolume(tmpDir, "test", 1, types.NeedleVersionV3)
	if err != nil {
		t.Fatalf("Failed to create volume: %v", err)
	}

	return v, tmpDir
}

func TestNewVolume(t *testing.T) {
	v, _ := setupTestVolume(t)
	defer v.Close()

	// Check volume properties
	if v.ID() != 1 {
		t.Errorf("Expected volume ID 1, got %d", v.ID())
	}

	if v.Collection() != "test" {
		t.Errorf("Expected collection 'test', got %s", v.Collection())
	}

	if v.Version() != types.NeedleVersionV3 {
		t.Errorf("Expected version 3, got %d", v.Version())
	}

	// Check files exist
	dataFile := v.DataFileName()

	if _, err := os.Stat(dataFile); os.IsNotExist(err) {
		t.Error("Data file does not exist")
	}

	// Index file may not exist for new volumes until first write
}

func TestVolumeWriteNeedle(t *testing.T) {
	v, _ := setupTestVolume(t)
	defer v.Close()

	// Create test needle
	n := &needle.Needle{
		Cookie:   0x12345678,
		Id:       0x123456789ABCDEF0,
		DataSize: 4,
		Data:     []byte("test"),
	}
	n.SetAppendAtNs()

	// Write needle
	offset, size, err := v.WriteNeedle(n)
	if err != nil {
		t.Fatalf("Failed to write needle: %v", err)
	}

	// First needle is at actual offset 8 (after superblock), encoded = 8/8 = 1
	if offset != types.Offset(1) {
		t.Errorf("Expected offset 1 (encoded), got %d", offset)
	}

	if size == 0 {
		t.Error("Expected non-zero size")
	}

	// Verify needle count
	if v.NeedleCount() != 1 {
		t.Errorf("Expected needle count 1, got %d", v.NeedleCount())
	}
}

func TestVolumeGetNeedle(t *testing.T) {
	v, _ := setupTestVolume(t)
	defer v.Close()

	// Create test needle
	original := &needle.Needle{
		Cookie:   0x12345678,
		Id:       0x123456789ABCDEF0,
		DataSize: 4,
		Data:     []byte("test"),
	}
	original.SetName("test.txt")
	original.SetAppendAtNs()

	// Write needle
	_, _, err := v.WriteNeedle(original)
	if err != nil {
		t.Fatalf("Failed to write needle: %v", err)
	}

	// Read needle back
	retrieved, err := v.GetNeedle(original.Id)
	if err != nil {
		t.Fatalf("Failed to get needle: %v", err)
	}

	// Verify fields
	if retrieved.Id != original.Id {
		t.Errorf("Expected ID %d, got %d", original.Id, retrieved.Id)
	}

	if retrieved.DataSize != original.DataSize {
		t.Errorf("Expected DataSize %d, got %d", original.DataSize, retrieved.DataSize)
	}

	if string(retrieved.Data) != string(original.Data) {
		t.Errorf("Expected data %s, got %s", original.Data, retrieved.Data)
	}

	if retrieved.GetName() != original.GetName() {
		t.Errorf("Expected name %s, got %s", original.GetName(), retrieved.GetName())
	}
}

func TestVolumeDeleteNeedle(t *testing.T) {
	v, _ := setupTestVolume(t)
	defer v.Close()

	// Create test needle
	n := &needle.Needle{
		Cookie:   0x12345678,
		Id:       0x123456789ABCDEF0,
		DataSize: 4,
		Data:     []byte("test"),
	}
	n.SetAppendAtNs()

	// Write needle
	_, _, err := v.WriteNeedle(n)
	if err != nil {
		t.Fatalf("Failed to write needle: %v", err)
	}

	// Delete needle
	if err := v.DeleteNeedle(n.Id); err != nil {
		t.Fatalf("Failed to delete needle: %v", err)
	}

	// Verify deletion counter
	if v.DeletedCount() != 1 {
		t.Errorf("Expected deleted count 1, got %d", v.DeletedCount())
	}

	// Try to read deleted needle (should fail)
	_, err = v.GetNeedle(n.Id)
	if err == nil {
		t.Error("Expected error when reading deleted needle")
	}
}

func TestVolumeCompact(t *testing.T) {
	v, _ := setupTestVolume(t)
	defer v.Close()

	// Write multiple needles
	for i := 0; i < 10; i++ {
		n := &needle.Needle{
			Cookie:   0x12345678,
			Id:       types.NeedleId(i + 1),
			DataSize: 100,
			Data:     make([]byte, 100),
		}
		n.SetAppendAtNs()

		if _, _, err := v.WriteNeedle(n); err != nil {
			t.Fatalf("Failed to write needle %d: %v", i, err)
		}
	}

	originalSize := v.UsedSize()

	// Delete some needles
	for i := 0; i < 5; i++ {
		if err := v.DeleteNeedle(types.NeedleId(i + 1)); err != nil {
			t.Fatalf("Failed to delete needle %d: %v", i, err)
		}
	}

	// Compact volume (threshold 0.3 = 30%)
	result, err := v.Compact(0.3)
	if err != nil {
		t.Fatalf("Failed to compact volume: %v", err)
	}

	// With index-based iteration: 10 live entries (tombstones are tracked separately
	// in index as Size=0). NeedlesRead counts all index entries including tombstoned ones.
	// After 10 writes + 5 deletes: index has 10 entries (5 live + 5 tombstoned).
	if result.NeedlesRead != 10 {
		t.Errorf("Expected 10 needles read (all index entries), got %d", result.NeedlesRead)
	}

	if result.NeedlesDeleted != 5 {
		t.Errorf("Expected 5 needles deleted, got %d", result.NeedlesDeleted)
	}

	if result.NeedlesWritten != 5 {
		t.Errorf("Expected 5 needles written (6-10), got %d", result.NeedlesWritten)
	}

	if result.CompactedSize >= result.OriginalSize {
		t.Errorf("Expected compacted size < original size, got %d >= %d",
			result.CompactedSize, result.OriginalSize)
	}

	// Commit compaction
	if err := v.CommitCompact(result); err != nil {
		t.Fatalf("Failed to commit compaction: %v", err)
	}

	// Verify new size
	if v.UsedSize() >= originalSize {
		t.Errorf("Expected new size < original size, got %d >= %d",
			v.UsedSize(), originalSize)
	}

	// Verify deletion counter was reset (no deleted entries in new index)
	if v.DeletedCount() != 0 {
		t.Errorf("Expected deleted count 0 after compaction, got %d", v.DeletedCount())
	}

	// Verify remaining needles (6-10) are accessible
	for i := 6; i <= 10; i++ {
		_, err := v.GetNeedle(types.NeedleId(i))
		if err != nil {
			t.Errorf("Failed to get needle %d after compaction: %v", i, err)
		}
	}

	// Verify deleted needles (1-5) are not accessible
	for i := 1; i <= 5; i++ {
		_, err := v.GetNeedle(types.NeedleId(i))
		if err == nil {
			t.Errorf("Expected error when getting deleted needle %d", i)
		}
	}
}

func TestVolumeGarbageLevel(t *testing.T) {
	v, _ := setupTestVolume(t)
	defer v.Close()

	// Initial garbage level should be 0
	if v.GarbageLevel() != 0 {
		t.Errorf("Expected initial garbage level 0, got %.2f", v.GarbageLevel())
	}

	// Write some needles
	for i := 0; i < 10; i++ {
		n := &needle.Needle{
			Cookie:   0x12345678,
			Id:       types.NeedleId(i + 1),
			DataSize: 100,
			Data:     make([]byte, 100),
		}
		n.SetAppendAtNs()

		if _, _, err := v.WriteNeedle(n); err != nil {
			t.Fatalf("Failed to write needle: %v", err)
		}
	}

	// Delete half of them
	for i := 0; i < 5; i++ {
		if err := v.DeleteNeedle(types.NeedleId(i + 1)); err != nil {
			t.Fatalf("Failed to delete needle: %v", err)
		}
	}

	// Garbage level should be around 50%
	garbageLevel := v.GarbageLevel()
	if garbageLevel < 0.4 || garbageLevel > 0.6 {
		t.Errorf("Expected garbage level around 0.5, got %.2f", garbageLevel)
	}
}

func TestVolumeDestroy(t *testing.T) {
	v, _ := setupTestVolume(t)

	dataFile := v.DataFileName()
	indexFile := v.IndexFileName()

	// Destroy volume
	if err := v.Destroy(); err != nil {
		t.Fatalf("Failed to destroy volume: %v", err)
	}

	// Verify files are deleted
	if _, err := os.Stat(dataFile); !os.IsNotExist(err) {
		t.Error("Data file still exists after destroy")
	}

	if _, err := os.Stat(indexFile); !os.IsNotExist(err) {
		t.Error("Index file still exists after destroy")
	}
}

func TestMemDbNeedleMap(t *testing.T) {
	db := NewMemDb()
	defer db.Close()

	// Test Put and Get
	id := types.NeedleId(123)
	offset := types.Offset(1000)
	size := types.Size(512)

	if err := db.Put(id, offset, size); err != nil {
		t.Fatalf("Failed to put entry: %v", err)
	}

	retrievedOffset, retrievedSize, err := db.Get(id)
	if err != nil {
		t.Fatalf("Failed to get entry: %v", err)
	}

	if retrievedOffset != offset {
		t.Errorf("Expected offset %d, got %d", offset, retrievedOffset)
	}

	if retrievedSize != size {
		t.Errorf("Expected size %d, got %d", size, retrievedSize)
	}

	// Test Delete
	if err := db.Delete(id); err != nil {
		t.Fatalf("Failed to delete entry: %v", err)
	}

	_, _, err = db.Get(id)
	if err == nil {
		t.Error("Expected error when getting deleted entry")
	}
}

func TestMemDbLoadAndFlush(t *testing.T) {
	tmpDir := t.TempDir()
	indexFile := filepath.Join(tmpDir, "test.idx")

	// Create and populate index with auto-appending
	db, err := LoadNeedleMap(indexFile)
	if err != nil {
		t.Fatalf("LoadNeedleMap: %v", err)
	}

	for i := 1; i <= 10; i++ {
		id := types.NeedleId(i)
		offset := types.Offset(i * 100)
		size := types.Size(i * 50)
		if err := db.Put(id, offset, size); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	db.Close()

	// Load index into new map
	db2 := NewMemDb()
	if err := db2.Load(indexFile); err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer db2.Close()

	for i := 1; i <= 10; i++ {
		id := types.NeedleId(i)
		expectedOffset := types.Offset(i * 100)
		expectedSize := types.Size(i * 50)
		offset, size, err := db2.Get(id)
		if err != nil {
			t.Errorf("Get %d: %v", i, err)
			continue
		}
		if offset != expectedOffset {
			t.Errorf("Entry %d: offset got %d want %d", i, offset, expectedOffset)
		}
		if size != expectedSize {
			t.Errorf("Entry %d: size got %d want %d", i, size, expectedSize)
		}
	}

	// Check statistics
	if db2.FileCount() != 10 {
		t.Errorf("FileCount: got %d want 10", db2.FileCount())
	}
	if db2.ContentSize() == 0 {
		t.Error("ContentSize should be non-zero")
	}
}

func TestCompactIndex(t *testing.T) {
	tmpDir := t.TempDir()
	inputFile := filepath.Join(tmpDir, "input.idx")
	outputFile := filepath.Join(tmpDir, "output.idx")

	// Create index with some entries and deletions
	db, err := LoadNeedleMap(inputFile)
	if err != nil {
		t.Fatalf("LoadNeedleMap: %v", err)
	}

	// Add active entries
	for i := 1; i <= 10; i++ {
		if err := db.Put(types.NeedleId(i), types.Offset(i*100), types.Size(512)); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	// Delete entries 1-3
	for i := 1; i <= 3; i++ {
		if err := db.Delete(types.NeedleId(i)); err != nil {
			t.Fatalf("Delete %d: %v", i, err)
		}
	}
	db.Close()

	// Compact
	if err := CompactIndex(inputFile, outputFile); err != nil {
		t.Fatalf("CompactIndex: %v", err)
	}

	// Load compacted
	compact := NewMemDb()
	if err := compact.Load(outputFile); err != nil {
		t.Fatalf("Load compacted: %v", err)
	}
	defer compact.Close()

	// Deleted entries should be gone
	for i := 1; i <= 3; i++ {
		_, _, err := compact.Get(types.NeedleId(i))
		if err == nil {
			t.Errorf("Entry %d should be absent in compact index", i)
		}
	}

	// Active entries should be present
	for i := 4; i <= 10; i++ {
		_, _, err := compact.Get(types.NeedleId(i))
		if err != nil {
			t.Errorf("Entry %d should exist in compact index: %v", i, err)
		}
	}
}

func TestVolumeReadOnly(t *testing.T) {
	v, _ := setupTestVolume(t)
	defer v.Close()

	// Mark as read-only
	v.ReadOnly = true

	// Try to write needle (should fail)
	n := &needle.Needle{
		Cookie:   0x12345678,
		Id:       0x123456789ABCDEF0,
		DataSize: 4,
		Data:     []byte("test"),
	}

	_, _, err := v.WriteNeedle(n)
	if err == nil {
		t.Error("Expected error when writing to read-only volume")
	}

	// Try to delete needle (should fail)
	err = v.DeleteNeedle(n.Id)
	if err == nil {
		t.Error("Expected error when deleting from read-only volume")
	}

	// Reading should still work
	_, err = v.GetNeedle(n.Id)
	if err == nil {
		t.Error("Expected error when getting non-existent needle")
	}
}

func TestNeedCompact(t *testing.T) {
	v, _ := setupTestVolume(t)
	defer v.Close()

	// Initially should not need compact
	if v.NeedCompact(0.1) {
		t.Error("Expected volume to not need compact initially")
	}

	// Add and delete some needles
	for i := 0; i < 10; i++ {
		n := &needle.Needle{
			Cookie:   0x12345678,
			Id:       types.NeedleId(i + 1),
			DataSize: 100,
			Data:     make([]byte, 100),
		}
		n.SetAppendAtNs()

		if _, _, err := v.WriteNeedle(n); err != nil {
			t.Fatalf("Failed to write needle: %v", err)
		}
	}

	// Delete half
	for i := 0; i < 5; i++ {
		if err := v.DeleteNeedle(types.NeedleId(i + 1)); err != nil {
			t.Fatalf("Failed to delete needle: %v", err)
		}
	}

	// Should need compact with threshold 0.3 (30%)
	if !v.NeedCompact(0.3) {
		t.Error("Expected volume to need compact after deletions")
	}

	// Should not need compact with threshold 0.6 (60%)
	if v.NeedCompact(0.6) {
		t.Error("Expected volume to not need compact with high threshold")
	}
}

func TestCompactVolume(t *testing.T) {
	v, _ := setupTestVolume(t)
	defer v.Close()

	// Add and delete some needles
	for i := 0; i < 10; i++ {
		n := &needle.Needle{
			Cookie:   0x12345678,
			Id:       types.NeedleId(i + 1),
			DataSize: 100,
			Data:     make([]byte, 100),
		}
		n.SetAppendAtNs()

		if _, _, err := v.WriteNeedle(n); err != nil {
			t.Fatalf("Failed to write needle: %v", err)
		}
	}

	// Delete half
	for i := 0; i < 5; i++ {
		if err := v.DeleteNeedle(types.NeedleId(i + 1)); err != nil {
			t.Fatalf("Failed to delete needle: %v", err)
		}
	}

	// Compact and commit
	result, err := CompactVolume(v, 0.3)
	if err != nil {
		t.Fatalf("Failed to compact volume: %v", err)
	}

	if result.NeedlesWritten != 5 {
		t.Errorf("Expected 5 needles written, got %d", result.NeedlesWritten)
	}

	// Verify remaining needles are accessible (needles 6-10)
	for i := 6; i <= 10; i++ {
		_, err := v.GetNeedle(types.NeedleId(i))
		if err != nil {
			t.Errorf("Failed to get needle %d after compaction: %v", i, err)
		}
	}
}
