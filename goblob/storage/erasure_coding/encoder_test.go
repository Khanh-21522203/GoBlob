package erasure_coding

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeRoundTripWithLength(t *testing.T) {
	enc, err := NewEncoder(10, 3)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	input := []byte("hello erasure coding world")
	shards, err := enc.Encode(input)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	output, err := enc.DecodeWithLength(shards, len(input))
	if err != nil {
		t.Fatalf("DecodeWithLength: %v", err)
	}
	if !bytes.Equal(output, input) {
		t.Fatalf("decoded data mismatch: got %q want %q", output, input)
	}
}

func TestDecodeReconstructMissingShard(t *testing.T) {
	enc, err := NewEncoder(6, 3)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	input := []byte("this payload should survive a missing data shard")
	shards, err := enc.Encode(input)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	shards[2] = nil
	shards[7] = nil

	output, err := enc.DecodeWithLength(shards, len(input))
	if err != nil {
		t.Fatalf("DecodeWithLength: %v", err)
	}
	if !bytes.Equal(output, input) {
		t.Fatalf("decoded data mismatch: got %q want %q", output, input)
	}
}

func TestNewEncoderValidateConfig(t *testing.T) {
	if _, err := NewEncoder(0, 1); err == nil {
		t.Fatal("expected error for dataShards=0")
	}
	if _, err := NewEncoder(1, -1); err == nil {
		t.Fatal("expected error for parityShards<0")
	}
}

func TestDecodeRejectsShardSizeMismatch(t *testing.T) {
	enc, err := NewEncoder(2, 1)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	shards := [][]byte{
		[]byte{1, 2, 3},
		[]byte{4, 5},
		nil,
	}
	if _, err := enc.Decode(shards); err == nil {
		t.Fatal("expected shard size mismatch error")
	}
}
