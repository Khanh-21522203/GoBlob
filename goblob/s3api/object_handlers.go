package s3api

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"GoBlob/goblob/quota"
)

const (
	maxSinglePutObjectSize = 256 << 20 // 256 MB
)

func (s *S3ApiServer) handlePutObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, maxSinglePutObjectSize+1))
	if err != nil {
		writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "failed to read object body")
		return
	}
	if len(data) > maxSinglePutObjectSize {
		writeS3Error(w, r, http.StatusRequestEntityTooLarge, "EntityTooLarge", "object is too large")
		return
	}

	userID := requestQuotaUserID(r)
	if s.quota != nil {
		if err := s.quota.CheckUserQuota(r.Context(), userID, int64(len(data))); err != nil {
			if _, ok := err.(*quota.QuotaExceededError); ok {
				writeS3Error(w, r, http.StatusInsufficientStorage, "InsufficientStorage", err.Error())
				return
			}
			s.logger.Warn("quota user check failed", "user", userID, "err", err)
		}
		if err := s.quota.CheckBucketQuota(r.Context(), bucket, int64(len(data))); err != nil {
			if _, ok := err.(*quota.QuotaExceededError); ok {
				writeS3Error(w, r, http.StatusInsufficientStorage, "InsufficientStorage", err.Error())
				return
			}
			s.logger.Warn("quota bucket check failed", "bucket", bucket, "err", err)
		}
	}

	extended := map[string][]byte{}
	if userID != "" {
		extended["s3:owner"] = []byte(userID)
	}
	versioning, _ := s.getBucketVersioningState(r.Context(), bucket)
	if strings.EqualFold(versioning, "Enabled") {
		versionID := generateVersionID()
		extended["s3:version_id"] = []byte(versionID)
		w.Header().Set("x-amz-version-id", versionID)
	}

	sse := strings.TrimSpace(r.Header.Get("x-amz-server-side-encryption"))
	if strings.EqualFold(sse, "AES256") {
		extended["s3:sse"] = []byte("AES256")
		w.Header().Set("x-amz-server-side-encryption", "AES256")
	}

	mimeType := r.Header.Get("Content-Type")
	eTag, err := s.filerClient.PutObject(r.Context(), bucket, key, data, mimeType, extended)
	if err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
		default:
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}

	if err := s.filerClient.UpdateObjectExtended(r.Context(), bucket, key, func(ext map[string][]byte) (map[string][]byte, error) {
		if ext == nil {
			ext = make(map[string][]byte)
		}
		ext["s3:etag"] = []byte(eTag)
		return ext, nil
	}); err != nil {
		s.logger.Warn("failed to persist object etag metadata", "bucket", bucket, "key", key, "err", err)
	}
	if s.quota != nil {
		if err := s.quota.AddBucketUsage(r.Context(), bucket, int64(len(data))); err != nil {
			s.logger.Warn("failed to add bucket quota usage", "bucket", bucket, "err", err)
		}
		if userID != "" {
			if err := s.quota.AddUserUsage(r.Context(), userID, int64(len(data))); err != nil {
				s.logger.Warn("failed to add user quota usage", "user", userID, "err", err)
			}
		}
	}

	w.Header().Set("ETag", "\""+eTag+"\"")
	w.WriteHeader(http.StatusOK)
}

func (s *S3ApiServer) handleGetObject(w http.ResponseWriter, r *http.Request, bucket, key string, withBody bool) {
	obj, err := s.filerClient.GetObject(r.Context(), bucket, key)
	if err != nil {
		switch {
		case errors.Is(err, ErrObjectNotFound):
			writeS3Error(w, r, http.StatusNotFound, "NoSuchKey", "object not found")
		default:
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}

	if obj.ContentType == "" {
		obj.ContentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(obj.Content)), 10))
	w.Header().Set("Last-Modified", obj.Mtime.UTC().Format(http.TimeFormat))
	if eTag := string(obj.Extended["s3:etag"]); eTag != "" {
		w.Header().Set("ETag", "\""+eTag+"\"")
	}
	if sse := string(obj.Extended["s3:sse"]); sse != "" {
		w.Header().Set("x-amz-server-side-encryption", sse)
	}
	if versionID := string(obj.Extended["s3:version_id"]); versionID != "" {
		w.Header().Set("x-amz-version-id", versionID)
	}
	if !withBody {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.Content)
}

func (s *S3ApiServer) handleDeleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	exists, err := s.filerClient.BucketExists(r.Context(), bucket)
	if err != nil {
		writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if !exists {
		writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
		return
	}
	obj, _ := s.filerClient.GetObject(r.Context(), bucket, key)
	if err := s.filerClient.DeleteObject(r.Context(), bucket, key); err != nil {
		writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if s.quota != nil && obj != nil {
		if err := s.quota.SubtractBucketUsage(r.Context(), bucket, obj.Size); err != nil {
			s.logger.Warn("failed to subtract bucket quota usage", "bucket", bucket, "err", err)
		}
		owner := string(obj.Extended["s3:owner"])
		if owner == "" {
			owner = requestQuotaUserID(r)
		}
		if owner != "" {
			if err := s.quota.SubtractUserUsage(r.Context(), owner, obj.Size); err != nil {
				s.logger.Warn("failed to subtract user quota usage", "user", owner, "err", err)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *S3ApiServer) handleObjectTagging(w http.ResponseWriter, r *http.Request, bucket, key string) {
	switch r.Method {
	case http.MethodPut:
		defer r.Body.Close()
		body, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
		if err != nil {
			writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "failed to read tagging body")
			return
		}
		var t tagging
		if err := xml.Unmarshal(body, &t); err != nil {
			writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "invalid tagging xml")
			return
		}
		if len(t.TagSet) > 10 {
			writeS3Error(w, r, http.StatusBadRequest, "InvalidTag", "too many tags")
			return
		}
		raw, err := json.Marshal(t.TagSet)
		if err != nil {
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		err = s.filerClient.UpdateObjectExtended(r.Context(), bucket, key, func(ext map[string][]byte) (map[string][]byte, error) {
			if ext == nil {
				ext = make(map[string][]byte)
			}
			ext["s3:tags"] = raw
			return ext, nil
		})
		if err != nil {
			if errors.Is(err, ErrObjectNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchKey", "object not found")
				return
			}
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		obj, err := s.filerClient.GetObject(r.Context(), bucket, key)
		if err != nil {
			if errors.Is(err, ErrObjectNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchKey", "object not found")
				return
			}
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		result := tagging{Xmlns: s3XMLNS, TagSet: []tagKV{}}
		if raw := obj.Extended["s3:tags"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &result.TagSet)
		}
		writeXML(w, http.StatusOK, result)
	case http.MethodDelete:
		err := s.filerClient.UpdateObjectExtended(r.Context(), bucket, key, func(ext map[string][]byte) (map[string][]byte, error) {
			delete(ext, "s3:tags")
			return ext, nil
		})
		if err != nil {
			if errors.Is(err, ErrObjectNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchKey", "object not found")
				return
			}
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func generateVersionID() string {
	return strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
}
