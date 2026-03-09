package storage

import (
	"testing"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/storage/needle"
)

func TestNewStore(t *testing.T) {
	tmpDir := t.TempDir()

	s, err := NewStore("127.0.0.1", 8080, []DiskDirectoryConfig{
		{Directory: tmpDir, MaxVolumeCount: 10},
	}, NeedleMapInMemory)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	if s.Ip != "127.0.0.1" {
		t.Errorf("Expected IP 127.0.0.1, got %s", s.Ip)
	}
	if s.Port != 8080 {
		t.Errorf("Expected port 8080, got %d", s.Port)
	}
	if len(s.Locations) != 1 {
		t.Errorf("Expected 1 location, got %d", len(s.Locations))
	}
}

func TestStoreAllocateAndWriteVolume(t *testing.T) {
	tmpDir := t.TempDir()

	s, err := NewStore("127.0.0.1", 8080, []DiskDirectoryConfig{
		{Directory: tmpDir, MaxVolumeCount: 10},
	}, NeedleMapInMemory)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Allocate a volume
	vid := types.VolumeId(1)
	if err := s.AllocateVolume(vid, "test", types.NeedleVersionV3); err != nil {
		t.Fatalf("Failed to allocate volume: %v", err)
	}

	// Get the volume
	v, ok := s.GetVolume(vid)
	if !ok {
		t.Fatal("Volume not found after allocation")
	}
	if v.ID() != vid {
		t.Errorf("Expected volume ID %d, got %d", vid, v.ID())
	}

	// Write a needle
	n := &needle.Needle{
		Cookie:   0x12345678,
		Id:       types.NeedleId(1),
		DataSize: 4,
		Data:     []byte("test"),
	}
	n.SetAppendAtNs()

	_, _, err = s.WriteVolumeNeedle(vid, n)
	if err != nil {
		t.Fatalf("Failed to write needle: %v", err)
	}

	// Read the needle back using FileId (with Cookie)
	fid := types.FileId{
		VolumeId: vid,
		NeedleId: n.Id,
		Cookie:   n.Cookie,
	}
	retrieved, err := s.ReadVolumeNeedle(vid, fid)
	if err != nil {
		t.Fatalf("Failed to read needle: %v", err)
	}
	if string(retrieved.Data) != "test" {
		t.Errorf("Expected data 'test', got '%s'", retrieved.Data)
	}

	// Delete the needle
	if err := s.DeleteVolumeNeedle(vid, n.Id); err != nil {
		t.Fatalf("Failed to delete needle: %v", err)
	}

	// Should not be readable after deletion
	_, err = s.ReadVolumeNeedle(vid, fid)
	if err == nil {
		t.Error("Expected error reading deleted needle")
	}
}

func TestStoreVolumeNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	s, err := NewStore("127.0.0.1", 8080, []DiskDirectoryConfig{
		{Directory: tmpDir, MaxVolumeCount: 10},
	}, NeedleMapInMemory)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer s.Close()

	// Try to write to non-existent volume
	n := &needle.Needle{Id: 1, DataSize: 4, Data: []byte("test")}
	_, _, err = s.WriteVolumeNeedle(types.VolumeId(99), n)
	if err == nil {
		t.Error("Expected error for non-existent volume")
	}
}
