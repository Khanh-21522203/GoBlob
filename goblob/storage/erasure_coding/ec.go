package erasure_coding

const (
	DefaultDataShards   = 10
	DefaultParityShards = 3
	DiskTypeEC          = "ec"
)

// ECVolume describes erasure-coded volume placement metadata.
type ECVolume struct {
	VolumeId     uint32
	DataShards   int
	ParityShards int
	ShardSize    int64
	Shards       []ECShard
}

// ECShard describes where one shard is stored.
type ECShard struct {
	VolumeId   uint32
	ShardIndex int
	DataNode   string
}

func (v *ECVolume) TotalShards() int {
	if v == nil {
		return 0
	}
	return v.DataShards + v.ParityShards
}
