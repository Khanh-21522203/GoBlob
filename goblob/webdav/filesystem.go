package webdav

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/filer_pb"

	xwebdav "golang.org/x/net/webdav"
)

type filerAPI interface {
	LookupDirectoryEntry(ctx context.Context, in *filer_pb.LookupDirectoryEntryRequest, opts ...grpc.CallOption) (*filer_pb.LookupDirectoryEntryResponse, error)
	ListEntries(ctx context.Context, in *filer_pb.ListEntriesRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[filer_pb.ListEntriesResponse], error)
	CreateEntry(ctx context.Context, in *filer_pb.CreateEntryRequest, opts ...grpc.CallOption) (*filer_pb.CreateEntryResponse, error)
	UpdateEntry(ctx context.Context, in *filer_pb.UpdateEntryRequest, opts ...grpc.CallOption) (*filer_pb.UpdateEntryResponse, error)
	DeleteEntry(ctx context.Context, in *filer_pb.DeleteEntryRequest, opts ...grpc.CallOption) (*filer_pb.DeleteEntryResponse, error)
	AtomicRenameEntry(ctx context.Context, in *filer_pb.AtomicRenameEntryRequest, opts ...grpc.CallOption) (*filer_pb.AtomicRenameEntryResponse, error)
}

// FilerFileSystem implements x/net/webdav FileSystem.
type FilerFileSystem struct {
	delegate xwebdav.FileSystem

	conn   *grpc.ClientConn
	client filerAPI
	root   string
}

// NewFilerFileSystem creates a local-directory-backed WebDAV filesystem.
func NewFilerFileSystem(rootDir string) *FilerFileSystem {
	if rootDir == "" {
		rootDir = "."
	}
	return &FilerFileSystem{delegate: xwebdav.Dir(rootDir)}
}

// NewFilerFileSystemFromAddress creates a filer-gRPC-backed WebDAV filesystem.
func NewFilerFileSystemFromAddress(filerAddr, filerRoot string) (*FilerFileSystem, error) {
	addr := string(types.ServerAddress(strings.TrimSpace(filerAddr)).ToGrpcAddress())
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	fs := NewFilerFileSystemWithClient(filerRoot, filer_pb.NewFilerServiceClient(conn))
	fs.conn = conn
	if err := fs.ensureRoot(context.Background()); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return fs, nil
}

// NewFilerFileSystemWithClient is primarily intended for tests.
func NewFilerFileSystemWithClient(filerRoot string, client filerAPI) *FilerFileSystem {
	root := normalizeRoot(filerRoot)
	return &FilerFileSystem{client: client, root: root}
}

func (fs *FilerFileSystem) Close() error {
	if fs != nil && fs.conn != nil {
		return fs.conn.Close()
	}
	return nil
}

func normalizeRoot(root string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(root))
	if cleaned == "/" {
		return "/webdav"
	}
	return cleaned
}

func (fs *FilerFileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	if fs.delegate != nil {
		return fs.delegate.Mkdir(ctx, name, perm)
	}
	full := fs.toFilerPath(name)
	if full == fs.root {
		return nil
	}
	if err := fs.ensureParents(ctx, full); err != nil {
		return err
	}
	return fs.createDir(ctx, full, perm)
}

func (fs *FilerFileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (xwebdav.File, error) {
	if fs.delegate != nil {
		return fs.delegate.OpenFile(ctx, name, flag, perm)
	}
	full := fs.toFilerPath(name)
	entry, err := fs.lookup(ctx, full)
	created := false
	if err != nil {
		if !isNotFound(err) {
			return nil, err
		}
		if flag&os.O_CREATE == 0 {
			return nil, os.ErrNotExist
		}
		if err := fs.ensureParents(ctx, full); err != nil {
			return nil, err
		}
		now := time.Now().Unix()
		dir, base := splitDirName(full)
		entry = &filer_pb.Entry{
			Name:    base,
			Content: []byte{},
			Attributes: &filer_pb.FuseAttributes{
				FileMode: uint32(perm),
				Mtime:    now,
				Crtime:   now,
				FileSize: 0,
			},
		}
		if _, err := fs.client.CreateEntry(ctx, &filer_pb.CreateEntryRequest{Directory: dir, Entry: entry}); err != nil {
			return nil, err
		}
		created = true
	}
	if entry.GetIsDirectory() {
		return &filerFile{fs: fs, fullPath: full, isDir: true}, nil
	}
	data := append([]byte(nil), entry.GetContent()...)
	dirty := false
	if flag&os.O_TRUNC != 0 {
		data = []byte{}
		dirty = true
	}
	offset := int64(0)
	if flag&os.O_APPEND != 0 {
		offset = int64(len(data))
	}
	return &filerFile{
		fs:       fs,
		fullPath: full,
		mode:     os.FileMode(entry.GetAttributes().GetFileMode()),
		data:     data,
		offset:   offset,
		flags:    flag,
		dirty:    dirty,
		created:  created,
	}, nil
}

func (fs *FilerFileSystem) RemoveAll(ctx context.Context, name string) error {
	if fs.delegate != nil {
		return fs.delegate.RemoveAll(ctx, name)
	}
	full := fs.toFilerPath(name)
	if full == fs.root {
		children, err := fs.listDirectory(ctx, full)
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			return err
		}
		for _, child := range children {
			if err := fs.removeAllPath(ctx, path.Join(full, child.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	return fs.removeAllPath(ctx, full)
}

func (fs *FilerFileSystem) Rename(ctx context.Context, oldName, newName string) error {
	if fs.delegate != nil {
		return fs.delegate.Rename(ctx, oldName, newName)
	}
	oldPath := fs.toFilerPath(oldName)
	newPath := fs.toFilerPath(newName)
	if oldPath == fs.root || newPath == fs.root {
		return errors.New("renaming root is not supported")
	}
	if err := fs.ensureParents(ctx, newPath); err != nil {
		return err
	}
	oldDir, oldBase := splitDirName(oldPath)
	newDir, newBase := splitDirName(newPath)
	_, err := fs.client.AtomicRenameEntry(ctx, &filer_pb.AtomicRenameEntryRequest{
		OldDirectory: oldDir,
		OldName:      oldBase,
		NewDirectory: newDir,
		NewName:      newBase,
	})
	return err
}

func (fs *FilerFileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	if fs.delegate != nil {
		return fs.delegate.Stat(ctx, name)
	}
	full := fs.toFilerPath(name)
	entry, err := fs.lookup(ctx, full)
	if err != nil {
		if isNotFound(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	return entryInfoFrom(full, entry), nil
}

func (fs *FilerFileSystem) toFilerPath(name string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(name))
	if cleaned == "/" {
		return fs.root
	}
	return path.Join(fs.root, strings.TrimPrefix(cleaned, "/"))
}

func (fs *FilerFileSystem) lookup(ctx context.Context, full string) (*filer_pb.Entry, error) {
	dir, base := splitDirName(full)
	resp, err := fs.client.LookupDirectoryEntry(ctx, &filer_pb.LookupDirectoryEntryRequest{Directory: dir, Name: base})
	if err != nil {
		return nil, err
	}
	if resp.GetEntry() == nil {
		return nil, status.Error(codes.NotFound, "entry not found")
	}
	return resp.GetEntry(), nil
}

func (fs *FilerFileSystem) ensureRoot(ctx context.Context) error {
	if fs == nil || fs.client == nil {
		return nil
	}
	if err := fs.ensureParents(ctx, fs.root); err != nil {
		return err
	}
	return fs.createDir(ctx, fs.root, 0o755)
}

func (fs *FilerFileSystem) ensureParents(ctx context.Context, full string) error {
	dir, _ := splitDirName(full)
	if dir == "/" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(dir, "/"), "/")
	current := "/"
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = path.Join(current, part)
		if current == "/" {
			continue
		}
		if _, err := fs.lookup(ctx, current); err == nil {
			continue
		} else if !isNotFound(err) {
			return err
		}
		if err := fs.createDir(ctx, current, 0o755); err != nil {
			if isAlreadyExists(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func (fs *FilerFileSystem) createDir(ctx context.Context, full string, perm os.FileMode) error {
	dir, base := splitDirName(full)
	now := time.Now().Unix()
	_, err := fs.client.CreateEntry(ctx, &filer_pb.CreateEntryRequest{
		Directory: dir,
		Entry: &filer_pb.Entry{
			Name:        base,
			IsDirectory: true,
			Attributes: &filer_pb.FuseAttributes{
				FileMode: uint32(perm | os.ModeDir),
				Mtime:    now,
				Crtime:   now,
			},
		},
	})
	if isAlreadyExists(err) {
		return nil
	}
	return err
}

func (fs *FilerFileSystem) listDirectory(ctx context.Context, full string) ([]os.FileInfo, error) {
	stream, err := fs.client.ListEntries(ctx, &filer_pb.ListEntriesRequest{Directory: full, Limit: 10000})
	if err != nil {
		return nil, err
	}
	out := make([]os.FileInfo, 0)
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		entry := resp.GetEntry()
		if entry == nil {
			continue
		}
		out = append(out, entryInfoFrom(path.Join(full, entry.GetName()), entry))
	}
	return out, nil
}

func (fs *FilerFileSystem) removeAllPath(ctx context.Context, full string) error {
	entry, err := fs.lookup(ctx, full)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	if entry.GetIsDirectory() {
		children, err := fs.listDirectory(ctx, full)
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := fs.removeAllPath(ctx, path.Join(full, child.Name())); err != nil {
				return err
			}
		}
	}
	dir, base := splitDirName(full)
	_, err = fs.client.DeleteEntry(ctx, &filer_pb.DeleteEntryRequest{Directory: dir, Name: base, IsDeleteData: true})
	if isNotFound(err) {
		return nil
	}
	return err
}

func splitDirName(full string) (string, string) {
	cleaned := path.Clean("/" + full)
	if cleaned == "/" {
		return "/", ""
	}
	dir, base := path.Split(cleaned)
	dir = path.Clean(dir)
	if dir == "." {
		dir = "/"
	}
	return dir, base
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.NotFound
}

func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.AlreadyExists
}

type entryInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func entryInfoFrom(full string, entry *filer_pb.Entry) os.FileInfo {
	attr := entry.GetAttributes()
	mode := os.FileMode(attr.GetFileMode())
	if entry.GetIsDirectory() {
		mode |= os.ModeDir
	}
	size := int64(attr.GetFileSize())
	if size == 0 && len(entry.GetContent()) > 0 {
		size = int64(len(entry.GetContent()))
	}
	return entryInfo{
		name:    path.Base(full),
		size:    size,
		mode:    mode,
		modTime: time.Unix(attr.GetMtime(), 0),
		isDir:   entry.GetIsDirectory(),
	}
}

func (i entryInfo) Name() string       { return i.name }
func (i entryInfo) Size() int64        { return i.size }
func (i entryInfo) Mode() os.FileMode  { return i.mode }
func (i entryInfo) ModTime() time.Time { return i.modTime }
func (i entryInfo) IsDir() bool        { return i.isDir }
func (i entryInfo) Sys() any           { return nil }

type filerFile struct {
	fs       *FilerFileSystem
	fullPath string
	isDir    bool

	mode    os.FileMode
	data    []byte
	offset  int64
	flags   int
	dirty   bool
	closed  bool
	created bool

	dirEntries []os.FileInfo
	dirPos     int
}

func (f *filerFile) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	if f.isDir || f.fs == nil || f.fs.delegate != nil || !f.dirty {
		return nil
	}
	dir, base := splitDirName(f.fullPath)
	now := time.Now().Unix()
	entry := &filer_pb.Entry{
		Name:    base,
		Content: append([]byte(nil), f.data...),
		Attributes: &filer_pb.FuseAttributes{
			FileMode: uint32(f.mode),
			Mtime:    now,
			Crtime:   now,
			FileSize: uint64(len(f.data)),
		},
	}
	var err error
	if f.created {
		_, err = f.fs.client.CreateEntry(context.Background(), &filer_pb.CreateEntryRequest{Directory: dir, Entry: entry})
		if isAlreadyExists(err) {
			_, err = f.fs.client.UpdateEntry(context.Background(), &filer_pb.UpdateEntryRequest{Directory: dir, Entry: entry})
		}
	} else {
		_, err = f.fs.client.UpdateEntry(context.Background(), &filer_pb.UpdateEntryRequest{Directory: dir, Entry: entry})
	}
	return err
}

func (f *filerFile) Read(p []byte) (int, error) {
	if f.isDir {
		return 0, io.EOF
	}
	if f.offset >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *filerFile) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = f.offset + offset
	case io.SeekEnd:
		next = int64(len(f.data)) + offset
	default:
		return 0, os.ErrInvalid
	}
	if next < 0 {
		return 0, os.ErrInvalid
	}
	f.offset = next
	return f.offset, nil
}

func (f *filerFile) Write(p []byte) (int, error) {
	if f.isDir {
		return 0, os.ErrPermission
	}
	if f.flags&os.O_WRONLY == 0 && f.flags&os.O_RDWR == 0 && f.flags&os.O_APPEND == 0 {
		return 0, os.ErrPermission
	}
	end := int(f.offset) + len(p)
	if end > len(f.data) {
		next := make([]byte, end)
		copy(next, f.data)
		f.data = next
	}
	copy(f.data[f.offset:], p)
	f.offset += int64(len(p))
	f.dirty = true
	return len(p), nil
}

func (f *filerFile) Readdir(count int) ([]os.FileInfo, error) {
	if !f.isDir {
		return nil, os.ErrInvalid
	}
	if f.dirEntries == nil {
		entries, err := f.fs.listDirectory(context.Background(), f.fullPath)
		if err != nil {
			return nil, err
		}
		f.dirEntries = entries
	}
	if f.dirPos >= len(f.dirEntries) {
		return nil, io.EOF
	}
	if count <= 0 {
		count = len(f.dirEntries) - f.dirPos
	}
	end := f.dirPos + count
	if end > len(f.dirEntries) {
		end = len(f.dirEntries)
	}
	out := f.dirEntries[f.dirPos:end]
	f.dirPos = end
	return out, nil
}

func (f *filerFile) Stat() (os.FileInfo, error) {
	if f.fs == nil {
		return nil, os.ErrInvalid
	}
	if f.isDir {
		return entryInfo{name: path.Base(f.fullPath), mode: os.ModeDir | 0o755, isDir: true, modTime: time.Now()}, nil
	}
	return entryInfo{name: path.Base(f.fullPath), size: int64(len(f.data)), mode: f.mode, modTime: time.Now()}, nil
}
