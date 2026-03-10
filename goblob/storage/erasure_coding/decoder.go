package erasure_coding

import "fmt"

// Decode reconstructs missing shards and returns concatenated data shards.
// Since EC introduces shard padding, callers should use DecodeWithLength when
// original object size is known.
func (e *Encoder) Decode(shards [][]byte) ([]byte, error) {
	data, err := e.DecodeWithLength(shards, -1)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// DecodeWithLength decodes shards and optionally trims output to originalSize.
// If originalSize < 0, no trimming is applied.
func (e *Encoder) DecodeWithLength(shards [][]byte, originalSize int) ([]byte, error) {
	if e == nil || e.enc == nil {
		return nil, fmt.Errorf("encoder not initialized")
	}
	if len(shards) != e.TotalShards() {
		return nil, fmt.Errorf("invalid shard count: got %d, want %d", len(shards), e.TotalShards())
	}

	// Ensure all present shards are same size.
	shardSize := 0
	for i := 0; i < len(shards); i++ {
		if shards[i] == nil {
			continue
		}
		if shardSize == 0 {
			shardSize = len(shards[i])
			continue
		}
		if len(shards[i]) != shardSize {
			return nil, fmt.Errorf("shard size mismatch: got %d, want %d", len(shards[i]), shardSize)
		}
	}
	if shardSize == 0 {
		return nil, fmt.Errorf("no available shards")
	}

	if err := e.enc.Reconstruct(shards); err != nil {
		return nil, err
	}
	ok, err := e.enc.Verify(shards)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("shard verification failed")
	}

	data := make([]byte, 0, e.dataShards*shardSize)
	for i := 0; i < e.dataShards; i++ {
		data = append(data, shards[i]...)
	}
	if originalSize >= 0 {
		if originalSize > len(data) {
			return nil, fmt.Errorf("originalSize %d exceeds decoded size %d", originalSize, len(data))
		}
		data = data[:originalSize]
	}
	return data, nil
}
