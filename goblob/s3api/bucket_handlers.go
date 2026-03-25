package s3api

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *S3ApiServer) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	bucketNames, err := s.store.ListBuckets(r.Context())
	if err != nil {
		writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	items := make([]bucketInfo, 0, len(bucketNames))
	now := time.Now().UTC().Format(time.RFC3339)
	for _, bucket := range bucketNames {
		items = append(items, bucketInfo{Name: bucket, CreationDate: now})
	}
	writeXML(w, http.StatusOK, listAllMyBucketsResult{
		Xmlns: s3XMLNS,
		Owner: owner{ID: "goblob", DisplayName: "goblob"},
		Buckets: buckets{
			Bucket: items,
		},
	})
}

func (s *S3ApiServer) handleCreateBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := s.store.CreateBucket(r.Context(), bucket); err != nil {
		switch {
		case errors.Is(err, ErrBucketExists):
			writeS3Error(w, r, http.StatusConflict, "BucketAlreadyOwnedByYou", err.Error())
		default:
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	writeXML(w, http.StatusOK, createBucketResult{Xmlns: s3XMLNS, Location: "/" + bucket})
}

func (s *S3ApiServer) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := s.store.DeleteBucket(r.Context(), bucket); err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", err.Error())
		case errors.Is(err, ErrBucketNotEmpty):
			writeS3Error(w, r, http.StatusConflict, "BucketNotEmpty", err.Error())
		default:
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *S3ApiServer) handleHeadBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	exists, err := s.store.BucketExists(r.Context(), bucket)
	if err != nil {
		writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if !exists {
		writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *S3ApiServer) handleListObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	prefix := r.URL.Query().Get("prefix")
	objects, err := s.store.ListObjects(r.Context(), bucket, prefix)
	if err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
		default:
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}
	contents := make([]objectItem, 0, len(objects))
	for _, obj := range objects {
		eTag := strings.TrimSpace(obj.ETag)
		if eTag != "" && !strings.HasPrefix(eTag, "\"") {
			eTag = "\"" + eTag + "\""
		}
		contents = append(contents, objectItem{
			Key:          obj.Key,
			LastModified: obj.LastModified.UTC().Format(time.RFC3339),
			ETag:         eTag,
			Size:         obj.Size,
			StorageClass: "STANDARD",
		})
	}
	writeXML(w, http.StatusOK, listBucketResult{
		Xmlns:       s3XMLNS,
		Name:        bucket,
		Prefix:      prefix,
		KeyCount:    len(contents),
		MaxKeys:     len(contents),
		IsTruncated: false,
		Contents:    contents,
	})
}

func (s *S3ApiServer) handleDeleteObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024))
	if err != nil {
		writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "failed to read request body")
		return
	}
	var req deleteObjectsRequest
	if err := xml.Unmarshal(body, &req); err != nil {
		writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "invalid delete request xml")
		return
	}
	if len(req.Objects) > 1000 {
		writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "too many objects in delete request")
		return
	}

	deleted := make([]deletedEntry, 0, len(req.Objects))
	for _, obj := range req.Objects {
		if strings.TrimSpace(obj.Key) == "" {
			continue
		}
		_ = s.store.DeleteObject(r.Context(), bucket, obj.Key)
		deleted = append(deleted, deletedEntry(obj))
	}
	writeXML(w, http.StatusOK, deleteObjectsResult{Xmlns: s3XMLNS, Deleted: deleted})
}
