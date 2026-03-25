package s3api

import (
	"context"
	"time"
)

// FilerGateway is the interface S3ApiServer uses for all filer operations.
// *FilerClient satisfies this interface. Tests can substitute a fake.
type FilerGateway interface {
	// Bucket operations
	ListBuckets(ctx context.Context) ([]string, error)
	BucketExists(ctx context.Context, bucket string) (bool, error)
	CreateBucket(ctx context.Context, bucket string) error
	DeleteBucket(ctx context.Context, bucket string) error

	// Object operations
	PutObject(ctx context.Context, bucket, key string, data []byte, mimeType string, extended map[string][]byte) (string, error)
	GetObject(ctx context.Context, bucket, key string) (*ObjectData, error)
	DeleteObject(ctx context.Context, bucket, key string) error
	ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error)
	UpdateObjectExtended(ctx context.Context, bucket, key string, fn func(map[string][]byte) (map[string][]byte, error)) error

	// Bucket metadata (versioning, policy, CORS, lifecycle, etc.)
	GetBucketMeta(ctx context.Context, bucket, metaKey string) ([]byte, error)
	SetBucketMeta(ctx context.Context, bucket, metaKey string, value []byte) error
	DeleteBucketMeta(ctx context.Context, bucket, metaKey string) error

	// Multipart upload lifecycle
	CreateMultipartUpload(ctx context.Context, bucket, key, uploadID string, createdAt time.Time) error
	LoadMultipartUpload(ctx context.Context, bucket, key, uploadID string) (*MultipartMeta, error)
	PutMultipartPart(ctx context.Context, bucket, uploadID string, partNumber int, data []byte) (string, error)
	GetMultipartPart(ctx context.Context, bucket, uploadID string, partNumber int) (*ObjectData, string, error)
	DeleteMultipartUpload(ctx context.Context, bucket, uploadID string) error

	// KV store — satisfies quota.KVStore and iam.FilerIAMClient
	KvGet(ctx context.Context, key []byte) ([]byte, error)
	KvPut(ctx context.Context, key []byte, value []byte) error
	KvDelete(ctx context.Context, key []byte) error
}

// BucketRootInitializer is an optional interface checked at S3ApiServer startup.
// Implementations that need to pre-create the buckets root directory implement it.
type BucketRootInitializer interface {
	EnsureBucketsRoot(ctx context.Context) error
}

// MultipartMeta is the exported view of an in-progress multipart upload.
type MultipartMeta struct {
	UploadID      string `json:"upload_id"`
	Bucket        string `json:"bucket"`
	Key           string `json:"key"`
	CreatedAtUnix int64  `json:"created_at_unix"`
}
