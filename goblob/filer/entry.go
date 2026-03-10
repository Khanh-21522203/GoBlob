package filer

import (
	"os"
	"time"

	"GoBlob/goblob/core/types"
)

// FullPath is a typed string for file/directory paths.
type FullPath string

// DirAndName splits the full path into directory and base name.
// "/photos/vacation.jpg" → ("/photos", "vacation.jpg")
// "/" → ("", "/")  (root dir)
func (fp FullPath) DirAndName() (dir FullPath, name string) {
	// find last slash
	s := string(fp)
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			if i == 0 {
				return FullPath("/"), s[1:]
			}
			return FullPath(s[:i]), s[i+1:]
		}
	}
	return FullPath("/"), s
}

// Attr holds POSIX-like file/directory attributes.
type Attr struct {
	Mode          os.FileMode
	Uid           uint32
	Gid           uint32
	Mtime         time.Time
	Crtime        time.Time
	Mime          string
	Replication   string
	Collection    string
	TtlSec        int32
	DiskType      types.DiskType
	FileSize      uint64
	INode         uint64
	SymlinkTarget string
}

// FileChunk represents one chunk of a file stored on a volume server.
type FileChunk struct {
	FileId       string `json:"fileId"`
	Offset       int64  `json:"offset"`
	Size         int64  `json:"size"`
	ModifiedTsNs int64  `json:"modifiedTsNs"`
	ETag         string `json:"eTag"`
	IsCompressed bool   `json:"isCompressed"`
}

// RemoteEntry references an object in a remote storage system.
type RemoteEntry struct {
	StorageName string
	Key         string
	ETag        string
	StoredName  string
}

// Entry represents a file or directory in the filer namespace.
type Entry struct {
	FullPath        FullPath
	Attr            Attr
	Chunks          []*FileChunk
	Content         []byte            // inline data for small files
	Extended        map[string][]byte // extended attributes
	HardLinkId      []byte
	HardLinkCounter int32
	Remote          *RemoteEntry
}

// IsDirectory returns true if this entry represents a directory.
func (e *Entry) IsDirectory() bool {
	return e.Attr.Mode&os.ModeDir != 0
}
