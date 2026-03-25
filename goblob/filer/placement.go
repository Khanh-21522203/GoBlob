package filer

import (
	"context"
	"io"
)

// Uploader handles chunked file uploads to volume storage.
// It assigns file IDs from a master and writes data to volume servers.
// Implementations are injected into Filer so handlers can test without a live master.
type Uploader interface {
	ChunkUpload(ctx context.Context, reader io.Reader, totalSize, chunkSizeBytes int64) ([]*FileChunk, error)
}

// Resolver maps a volume ID to the HTTP URL of a volume server that holds its data.
// It abstracts the master lookup call so download handlers are testable.
type Resolver interface {
	ResolveVolume(ctx context.Context, volumeId uint32) (string, error)
}
