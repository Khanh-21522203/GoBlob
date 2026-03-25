package s3api

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/pb/filer_pb"
)

// ObjectData is filer object payload + metadata.
type ObjectData struct {
	Content     []byte
	ContentType string
	Extended    map[string][]byte
	Mtime       time.Time
	Size        int64
}

// ObjectInfo is listable object metadata.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
}

// FilerClient wraps basic filer gRPC operations used by S3 gateway.
type FilerClient struct {
	addresses   []string
	bucketsPath string
}

func NewFilerClient(filers []types.ServerAddress, bucketsPath string) *FilerClient {
	addrs := make([]string, 0, len(filers))
	seen := make(map[string]struct{})
	for _, addr := range filers {
		grpcAddr := normalizeFilerGRPCAddress(string(addr))
		if grpcAddr == "" {
			continue
		}
		if _, ok := seen[grpcAddr]; ok {
			continue
		}
		seen[grpcAddr] = struct{}{}
		addrs = append(addrs, grpcAddr)
	}
	if bucketsPath == "" {
		bucketsPath = "/buckets"
	}
	return &FilerClient{addresses: addrs, bucketsPath: path.Clean("/" + bucketsPath)}
}

func normalizeFilerGRPCAddress(addr string) string {
	if addr == "" {
		return ""
	}
	lastDot := strings.LastIndex(addr, ".")
	if lastDot > 0 && lastDot < len(addr)-1 {
		suffix := addr[lastDot+1:]
		prefix := addr[:lastDot]
		if _, err := strconv.Atoi(suffix); err == nil {
			host, _, splitErr := net.SplitHostPort(prefix)
			if splitErr == nil {
				return net.JoinHostPort(host, suffix)
			}
		}
	}
	return string(types.ServerAddress(addr).ToGrpcAddress())
}

func (fc *FilerClient) withClient(ctx context.Context, fn func(filer_pb.FilerServiceClient) error) error {
	if len(fc.addresses) == 0 {
		return ErrNoFilerConfigured
	}
	var lastErr error
	for _, addr := range fc.addresses {
		conn, err := grpc.NewClient(
			addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(64<<20),
				grpc.MaxCallSendMsgSize(64<<20),
			),
		)
		if err != nil {
			lastErr = err
			continue
		}
		err = fn(filer_pb.NewFilerServiceClient(conn))
		_ = conn.Close()
		if err == nil {
			return nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return ErrNoFilerConfigured
	}
	return lastErr
}

func splitPath(fullPath string) (directory, name string) {
	cleaned := path.Clean("/" + fullPath)
	if cleaned == "/" {
		return "/", ""
	}
	dir, name := path.Split(cleaned)
	dir = path.Clean(dir)
	if dir == "." {
		dir = "/"
	}
	return dir, name
}

func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotFound) {
		return true
	}
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.NotFound
}

func (fc *FilerClient) lookupEntry(ctx context.Context, fullPath string) (*filer_pb.Entry, error) {
	directory, name := splitPath(fullPath)
	var out *filer_pb.Entry
	err := fc.withClient(ctx, func(client filer_pb.FilerServiceClient) error {
		resp, err := client.LookupDirectoryEntry(ctx, &filer_pb.LookupDirectoryEntryRequest{Directory: directory, Name: name})
		if err != nil {
			return err
		}
		out = resp.GetEntry()
		if out == nil {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		if isNotFoundErr(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return out, nil
}

func (fc *FilerClient) EnsureDirectory(ctx context.Context, fullPath string) error {
	cleaned := path.Clean("/" + fullPath)
	if cleaned == "/" {
		return nil
	}
	segments := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	current := ""
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		current = path.Join(current, "/", segment)
		entry, err := fc.lookupEntry(ctx, current)
		if err == nil {
			if !entry.GetIsDirectory() {
				return fmt.Errorf("path %s exists and is not a directory", current)
			}
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}

		dir, name := splitPath(current)
		attr := &filer_pb.FuseAttributes{
			FileMode: uint32(os.ModeDir | 0755),
			Mtime:    time.Now().Unix(),
			Crtime:   time.Now().Unix(),
		}
		createReq := &filer_pb.CreateEntryRequest{
			Directory: dir,
			Entry: &filer_pb.Entry{
				Name:        name,
				IsDirectory: true,
				Attributes:  attr,
			},
		}
		if err := fc.withClient(ctx, func(client filer_pb.FilerServiceClient) error {
			_, err := client.CreateEntry(ctx, createReq)
			return err
		}); err != nil && !isAlreadyExists(err) {
			return err
		}
	}
	return nil
}

func isAlreadyExists(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.AlreadyExists
}

func (fc *FilerClient) EnsureBucketsRoot(ctx context.Context) error {
	return fc.EnsureDirectory(ctx, fc.bucketsPath)
}

func (fc *FilerClient) ListBuckets(ctx context.Context) ([]string, error) {
	if err := fc.EnsureBucketsRoot(ctx); err != nil {
		return nil, err
	}
	entries, err := fc.listEntries(ctx, fc.bucketsPath, "")
	if err != nil {
		return nil, err
	}
	buckets := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || !entry.GetIsDirectory() {
			continue
		}
		name := entry.GetName()
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		buckets = append(buckets, name)
	}
	sort.Strings(buckets)
	return buckets, nil
}

func (fc *FilerClient) bucketPath(bucket string) string {
	return path.Join(fc.bucketsPath, bucket)
}

func (fc *FilerClient) BucketExists(ctx context.Context, bucket string) (bool, error) {
	if bucket == "" {
		return false, ErrBucketNotFound
	}
	entry, err := fc.lookupEntry(ctx, fc.bucketPath(bucket))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return entry.GetIsDirectory(), nil
}

func (fc *FilerClient) CreateBucket(ctx context.Context, bucket string) error {
	if err := fc.EnsureBucketsRoot(ctx); err != nil {
		return err
	}
	exists, err := fc.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if exists {
		return ErrBucketExists
	}
	if err := fc.EnsureDirectory(ctx, fc.bucketPath(bucket)); err != nil {
		return err
	}
	return nil
}

func (fc *FilerClient) DeleteBucket(ctx context.Context, bucket string) error {
	exists, err := fc.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}
	objects, err := fc.ListObjects(ctx, bucket, "")
	if err != nil {
		return err
	}
	if len(objects) > 0 {
		return ErrBucketNotEmpty
	}

	for _, hidden := range []string{".sys", ".uploads", ".tags"} {
		_ = fc.deleteTree(ctx, path.Join(fc.bucketPath(bucket), hidden))
	}
	return fc.deleteEntry(ctx, fc.bucketPath(bucket))
}

func (fc *FilerClient) deleteTree(ctx context.Context, root string) error {
	entry, err := fc.lookupEntry(ctx, root)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if entry.GetIsDirectory() {
		children, err := fc.listEntries(ctx, root, "")
		if err != nil {
			return err
		}
		for _, child := range children {
			if child == nil {
				continue
			}
			if err := fc.deleteTree(ctx, path.Join(root, child.GetName())); err != nil {
				return err
			}
		}
	}
	return fc.deleteEntry(ctx, root)
}

func (fc *FilerClient) PutObject(ctx context.Context, bucket, key string, data []byte, mimeType string, extended map[string][]byte) (string, error) {
	exists, err := fc.BucketExists(ctx, bucket)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", ErrBucketNotFound
	}
	fullPath := path.Join(fc.bucketPath(bucket), key)
	directory, name := splitPath(fullPath)
	if err := fc.EnsureDirectory(ctx, directory); err != nil {
		return "", err
	}

	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	now := time.Now().Unix()
	entry := &filer_pb.Entry{
		Name:        name,
		IsDirectory: false,
		Content:     append([]byte(nil), data...),
		Extended:    cloneExtended(extended),
		Attributes: &filer_pb.FuseAttributes{
			FileMode: uint32(0644),
			FileSize: uint64(len(data)),
			Mtime:    now,
			Crtime:   now,
			Mime:     mimeType,
		},
	}
	if err := fc.upsertEntry(ctx, directory, entry); err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", ErrBucketNotFound
		}
		return "", err
	}

	sum := md5.Sum(data)
	eTag := hex.EncodeToString(sum[:])
	return eTag, nil
}

func (fc *FilerClient) upsertEntry(ctx context.Context, directory string, entry *filer_pb.Entry) error {
	full := path.Join(directory, entry.GetName())
	_, err := fc.lookupEntry(ctx, full)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if errors.Is(err, ErrNotFound) {
		return fc.withClient(ctx, func(client filer_pb.FilerServiceClient) error {
			_, err := client.CreateEntry(ctx, &filer_pb.CreateEntryRequest{Directory: directory, Entry: entry})
			return err
		})
	}
	return fc.withClient(ctx, func(client filer_pb.FilerServiceClient) error {
		_, err := client.UpdateEntry(ctx, &filer_pb.UpdateEntryRequest{Directory: directory, Entry: entry})
		return err
	})
}

func cloneExtended(ext map[string][]byte) map[string][]byte {
	if len(ext) == 0 {
		return nil
	}
	out := make(map[string][]byte, len(ext))
	for k, v := range ext {
		copied := make([]byte, len(v))
		copy(copied, v)
		out[k] = copied
	}
	return out
}

func (fc *FilerClient) GetObject(ctx context.Context, bucket, key string) (*ObjectData, error) {
	fullPath := path.Join(fc.bucketPath(bucket), key)
	entry, err := fc.lookupEntry(ctx, fullPath)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	if entry.GetIsDirectory() {
		return nil, ErrObjectNotFound
	}
	data := append([]byte(nil), entry.GetContent()...)
	mtime := time.Unix(entry.GetAttributes().GetMtime(), 0).UTC()
	return &ObjectData{
		Content:     data,
		ContentType: entry.GetAttributes().GetMime(),
		Extended:    cloneExtended(entry.GetExtended()),
		Mtime:       mtime,
		Size:        int64(entry.GetAttributes().GetFileSize()),
	}, nil
}

func (fc *FilerClient) UpdateObjectExtended(ctx context.Context, bucket, key string, updateFn func(map[string][]byte) (map[string][]byte, error)) error {
	fullPath := path.Join(fc.bucketPath(bucket), key)
	entry, err := fc.lookupEntry(ctx, fullPath)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrObjectNotFound
		}
		return err
	}
	dir, _ := splitPath(fullPath)
	updated, err := updateFn(cloneExtended(entry.GetExtended()))
	if err != nil {
		return err
	}
	entry.Extended = cloneExtended(updated)
	return fc.withClient(ctx, func(client filer_pb.FilerServiceClient) error {
		_, err := client.UpdateEntry(ctx, &filer_pb.UpdateEntryRequest{Directory: dir, Entry: entry})
		return err
	})
}

func (fc *FilerClient) DeleteObject(ctx context.Context, bucket, key string) error {
	fullPath := path.Join(fc.bucketPath(bucket), key)
	if err := fc.deleteEntry(ctx, fullPath); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

func (fc *FilerClient) deleteEntry(ctx context.Context, fullPath string) error {
	dir, name := splitPath(fullPath)
	err := fc.withClient(ctx, func(client filer_pb.FilerServiceClient) error {
		_, err := client.DeleteEntry(ctx, &filer_pb.DeleteEntryRequest{Directory: dir, Name: name, IsDeleteData: true})
		return err
	})
	if err != nil {
		if isNotFoundErr(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (fc *FilerClient) listEntries(ctx context.Context, directory, prefix string) ([]*filer_pb.Entry, error) {
	entries := make([]*filer_pb.Entry, 0)
	err := fc.withClient(ctx, func(client filer_pb.FilerServiceClient) error {
		stream, err := client.ListEntries(ctx, &filer_pb.ListEntriesRequest{Directory: directory, Prefix: prefix, Limit: 10000})
		if err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
				return ErrNotFound
			}
			return err
		}
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if entry := resp.GetEntry(); entry != nil {
				entries = append(entries, entry)
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return entries, nil
}

func (fc *FilerClient) ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	exists, err := fc.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBucketNotFound
	}
	objects := make([]ObjectInfo, 0)
	bucketRoot := fc.bucketPath(bucket)
	if err := fc.walkObjects(ctx, bucketRoot, "", prefix, &objects); err != nil {
		return nil, err
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	return objects, nil
}

func (fc *FilerClient) walkObjects(ctx context.Context, dirPath, keyPrefix, filterPrefix string, objects *[]ObjectInfo) error {
	entries, err := fc.listEntries(ctx, dirPath, "")
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		name := entry.GetName()
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, ".") {
			// internal metadata directories/files are hidden from object listing.
			continue
		}
		if entry.GetIsDirectory() {
			nextDir := path.Join(dirPath, name)
			nextPrefix := keyPrefix + name + "/"
			if err := fc.walkObjects(ctx, nextDir, nextPrefix, filterPrefix, objects); err != nil {
				return err
			}
			continue
		}
		key := keyPrefix + name
		if filterPrefix != "" && !strings.HasPrefix(key, filterPrefix) {
			continue
		}
		mtime := time.Unix(entry.GetAttributes().GetMtime(), 0).UTC()
		*objects = append(*objects, ObjectInfo{
			Key:          key,
			Size:         int64(entry.GetAttributes().GetFileSize()),
			LastModified: mtime,
			ETag:         string(entry.GetExtended()["s3:etag"]),
		})
	}
	return nil
}

func (fc *FilerClient) bucketMetaPath(bucket, key string) string {
	return path.Join(fc.bucketPath(bucket), ".sys", key)
}

func (fc *FilerClient) multipartRootPath(bucket string) string {
	return path.Join(fc.bucketPath(bucket), ".uploads")
}

func (fc *FilerClient) multipartUploadPath(bucket, uploadID string) string {
	return path.Join(fc.multipartRootPath(bucket), uploadID)
}

func (fc *FilerClient) multipartMetaPath(bucket, uploadID string) string {
	return path.Join(fc.multipartUploadPath(bucket, uploadID), ".meta")
}

func (fc *FilerClient) multipartPartKey(uploadID string, partNumber int) string {
	return path.Join(".uploads", uploadID, strconv.Itoa(partNumber))
}

func (fc *FilerClient) SetBucketMeta(ctx context.Context, bucket, key string, value []byte) error {
	exists, err := fc.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}
	metaPath := fc.bucketMetaPath(bucket, key)
	dir, name := splitPath(metaPath)
	if err := fc.EnsureDirectory(ctx, dir); err != nil {
		return err
	}
	entry := &filer_pb.Entry{
		Name:        name,
		IsDirectory: false,
		Content:     append([]byte(nil), value...),
		Attributes: &filer_pb.FuseAttributes{
			FileMode: uint32(0644),
			FileSize: uint64(len(value)),
			Mtime:    time.Now().Unix(),
			Crtime:   time.Now().Unix(),
			Mime:     "application/octet-stream",
		},
	}
	return fc.upsertEntry(ctx, dir, entry)
}

func (fc *FilerClient) GetBucketMeta(ctx context.Context, bucket, key string) ([]byte, error) {
	metaPath := fc.bucketMetaPath(bucket, key)
	entry, err := fc.lookupEntry(ctx, metaPath)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return append([]byte(nil), entry.GetContent()...), nil
}

func (fc *FilerClient) DeleteBucketMeta(ctx context.Context, bucket, key string) error {
	metaPath := fc.bucketMetaPath(bucket, key)
	err := fc.deleteEntry(ctx, metaPath)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

func (fc *FilerClient) CreateMultipartUpload(ctx context.Context, bucket, key, uploadID string, createdAt time.Time) error {
	exists, err := fc.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrBucketNotFound
	}

	rootDir := fc.multipartUploadPath(bucket, uploadID)
	if err := fc.EnsureDirectory(ctx, rootDir); err != nil {
		return err
	}
	meta := MultipartMeta{
		UploadID:      uploadID,
		Bucket:        bucket,
		Key:           key,
		CreatedAtUnix: createdAt.UTC().Unix(),
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	dir, name := splitPath(fc.multipartMetaPath(bucket, uploadID))
	entry := &filer_pb.Entry{
		Name:        name,
		IsDirectory: false,
		Content:     metaBytes,
		Attributes: &filer_pb.FuseAttributes{
			FileMode: uint32(0644),
			FileSize: uint64(len(metaBytes)),
			Mtime:    time.Now().Unix(),
			Crtime:   time.Now().Unix(),
			Mime:     "application/json",
		},
	}
	return fc.upsertEntry(ctx, dir, entry)
}

func (fc *FilerClient) LoadMultipartUpload(ctx context.Context, bucket, key, uploadID string) (*MultipartMeta, error) {
	entry, err := fc.lookupEntry(ctx, fc.multipartMetaPath(bucket, uploadID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	meta := &MultipartMeta{}
	if err := json.Unmarshal(entry.GetContent(), meta); err != nil {
		return nil, err
	}
	if meta.UploadID == "" {
		meta.UploadID = uploadID
	}
	if meta.Bucket == "" {
		meta.Bucket = bucket
	}
	if meta.Bucket != bucket {
		return nil, ErrNotFound
	}
	if key != "" && meta.Key != key {
		return nil, ErrNotFound
	}
	return meta, nil
}

func (fc *FilerClient) PutMultipartPart(ctx context.Context, bucket, uploadID string, partNumber int, data []byte) (string, error) {
	return fc.PutObject(ctx, bucket, fc.multipartPartKey(uploadID, partNumber), data, "application/octet-stream", nil)
}

func (fc *FilerClient) GetMultipartPart(ctx context.Context, bucket, uploadID string, partNumber int) (*ObjectData, string, error) {
	obj, err := fc.GetObject(ctx, bucket, fc.multipartPartKey(uploadID, partNumber))
	if err != nil {
		if errors.Is(err, ErrObjectNotFound) {
			return nil, "", ErrNotFound
		}
		return nil, "", err
	}
	sum := md5.Sum(obj.Content)
	return obj, hex.EncodeToString(sum[:]), nil
}

func (fc *FilerClient) DeleteMultipartUpload(ctx context.Context, bucket, uploadID string) error {
	if err := fc.deleteTree(ctx, fc.multipartUploadPath(bucket, uploadID)); err != nil {
		return err
	}
	return nil
}

// KvGet implements IAM filer KV access via filer gRPC.
func (fc *FilerClient) KvGet(ctx context.Context, key []byte) ([]byte, error) {
	var out []byte
	err := fc.withClient(ctx, func(client filer_pb.FilerServiceClient) error {
		resp, err := client.KvGet(ctx, &filer_pb.KvGetRequest{Key: key})
		if err != nil {
			return err
		}
		if resp.GetError() != "" {
			if strings.Contains(strings.ToLower(resp.GetError()), "not found") {
				return ErrNotFound
			}
			return errors.New(resp.GetError())
		}
		out = append([]byte(nil), resp.GetValue()...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// KvPut implements IAM filer KV access via filer gRPC.
func (fc *FilerClient) KvPut(ctx context.Context, key []byte, value []byte) error {
	return fc.withClient(ctx, func(client filer_pb.FilerServiceClient) error {
		resp, err := client.KvPut(ctx, &filer_pb.KvPutRequest{Key: key, Value: value})
		if err != nil {
			return err
		}
		if resp.GetError() != "" {
			return errors.New(resp.GetError())
		}
		return nil
	})
}

// KvDelete deletes a key from filer KV.
func (fc *FilerClient) KvDelete(ctx context.Context, key []byte) error {
	return fc.withClient(ctx, func(client filer_pb.FilerServiceClient) error {
		resp, err := client.KvDelete(ctx, &filer_pb.KvDeleteRequest{Key: key})
		if err != nil {
			return err
		}
		if resp.GetError() != "" {
			return errors.New(resp.GetError())
		}
		return nil
	})
}
