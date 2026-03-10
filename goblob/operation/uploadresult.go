package operation

// UploadResult is returned by volume upload endpoints.
type UploadResult struct {
	Name  string `json:"name"`
	Size  uint32 `json:"size"`
	ETag  string `json:"eTag"`
	Mime  string `json:"mime"`
	Fid   string `json:"fid"`
	Error string `json:"error,omitempty"`
}

// ChunkUploadResult describes one uploaded chunk.
type ChunkUploadResult struct {
	FileId       string
	Offset       int64
	Size         int64
	ModifiedTsNs int64
	ETag         string
}
