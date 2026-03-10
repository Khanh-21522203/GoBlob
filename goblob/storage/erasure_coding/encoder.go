package erasure_coding

import (
	"fmt"

	"github.com/klauspost/reedsolomon"
)

// Encoder wraps Reed-Solomon encode/decode behavior for a fixed shard layout.
type Encoder struct {
	dataShards   int
	parityShards int
	enc          reedsolomon.Encoder
}

func NewEncoder(dataShards, parityShards int) (*Encoder, error) {
	if dataShards <= 0 {
		return nil, fmt.Errorf("dataShards must be > 0")
	}
	if parityShards < 0 {
		return nil, fmt.Errorf("parityShards must be >= 0")
	}
	enc, err := reedsolomon.New(dataShards, parityShards)
	if err != nil {
		return nil, err
	}
	return &Encoder{
		dataShards:   dataShards,
		parityShards: parityShards,
		enc:          enc,
	}, nil
}

func (e *Encoder) DataShards() int {
	if e == nil {
		return 0
	}
	return e.dataShards
}

func (e *Encoder) ParityShards() int {
	if e == nil {
		return 0
	}
	return e.parityShards
}

func (e *Encoder) TotalShards() int {
	if e == nil {
		return 0
	}
	return e.dataShards + e.parityShards
}

// Encode splits data into data shards and computes parity shards.
// Returned shard slices are equal length.
func (e *Encoder) Encode(data []byte) ([][]byte, error) {
	if e == nil || e.enc == nil {
		return nil, fmt.Errorf("encoder not initialized")
	}

	shardSize := 0
	if len(data) > 0 {
		shardSize = (len(data) + e.dataShards - 1) / e.dataShards
	}
	if shardSize == 0 {
		shardSize = 1
	}

	shards := make([][]byte, e.TotalShards())
	for i := 0; i < e.dataShards; i++ {
		shards[i] = make([]byte, shardSize)
		start := i * shardSize
		if start >= len(data) {
			continue
		}
		end := start + shardSize
		if end > len(data) {
			end = len(data)
		}
		copy(shards[i], data[start:end])
	}
	for i := e.dataShards; i < e.TotalShards(); i++ {
		shards[i] = make([]byte, shardSize)
	}

	if err := e.enc.Encode(shards); err != nil {
		return nil, err
	}
	return shards, nil
}
