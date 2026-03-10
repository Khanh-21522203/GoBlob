package s3api

import "errors"

var (
	ErrNoFilerConfigured = errors.New("no filer configured")
	ErrNotFound          = errors.New("not found")
	ErrBucketNotFound    = errors.New("bucket not found")
	ErrBucketExists      = errors.New("bucket already exists")
	ErrBucketNotEmpty    = errors.New("bucket not empty")
	ErrObjectNotFound    = errors.New("object not found")
)
