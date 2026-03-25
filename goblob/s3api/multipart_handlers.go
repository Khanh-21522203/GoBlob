package s3api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	s3multipart "GoBlob/goblob/s3api/multipart"
)

const minMultipartPartSize = 5 * 1024 * 1024

func (s *S3ApiServer) handleInitiateMultipart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if r.Method != http.MethodPost {
		writeS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	exists, err := s.store.BucketExists(r.Context(), bucket)
	if err != nil {
		writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if !exists {
		writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
		return
	}

	uploadID := newUploadID()
	if err := s.store.CreateMultipartUpload(r.Context(), bucket, key, uploadID, time.Now().UTC()); err != nil {
		if errors.Is(err, ErrBucketNotFound) {
			writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
			return
		}
		writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	writeXML(w, http.StatusOK, initiateMultipartUploadResult{
		Xmlns:    s3XMLNS,
		Bucket:   bucket,
		Key:      key,
		UploadID: uploadID,
	})
}

func (s *S3ApiServer) handleUploadPart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if r.Method != http.MethodPut {
		writeS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	uploadID := r.URL.Query().Get("uploadId")
	partNumStr := r.URL.Query().Get("partNumber")
	partNumber, err := strconv.Atoi(partNumStr)
	if err != nil || partNumber < 1 || partNumber > 10000 {
		writeS3Error(w, r, http.StatusBadRequest, "InvalidArgument", "invalid part number")
		return
	}
	if _, err := s.store.LoadMultipartUpload(r.Context(), bucket, key, uploadID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeS3Error(w, r, http.StatusNotFound, "NoSuchUpload", "multipart upload not found")
			return
		}
		writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, maxSinglePutObjectSize+1))
	if err != nil {
		writeS3Error(w, r, http.StatusBadRequest, "InvalidRequest", "failed to read part body")
		return
	}
	if len(data) > maxSinglePutObjectSize {
		writeS3Error(w, r, http.StatusRequestEntityTooLarge, "EntityTooLarge", "part is too large")
		return
	}

	eTag, err := s.store.PutMultipartPart(r.Context(), bucket, uploadID, partNumber, data)
	if err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
		default:
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}

	w.Header().Set("ETag", "\""+eTag+"\"")
	w.WriteHeader(http.StatusOK)
}

func (s *S3ApiServer) handleCompleteMultipart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if r.Method != http.MethodPost {
		writeS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	uploadID := r.URL.Query().Get("uploadId")
	if _, err := s.store.LoadMultipartUpload(r.Context(), bucket, key, uploadID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeS3Error(w, r, http.StatusNotFound, "NoSuchUpload", "multipart upload not found")
			return
		}
		writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024))
	if err != nil {
		writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "failed to read completion body")
		return
	}

	var req struct {
		XMLName xml.Name                   `xml:"CompleteMultipartUpload"`
		Parts   []s3multipart.CompletePart `xml:"Part"`
	}
	if err := xml.Unmarshal(body, &req); err != nil {
		writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "invalid completion xml")
		return
	}
	if len(req.Parts) == 0 {
		writeS3Error(w, r, http.StatusBadRequest, "InvalidPart", "no parts provided")
		return
	}

	sort.Slice(req.Parts, func(i, j int) bool { return req.Parts[i].PartNumber < req.Parts[j].PartNumber })
	for i := 1; i < len(req.Parts); i++ {
		if req.Parts[i].PartNumber == req.Parts[i-1].PartNumber {
			writeS3Error(w, r, http.StatusBadRequest, "InvalidPart", "duplicate part number")
			return
		}
	}

	combined := make([]byte, 0)
	for i, part := range req.Parts {
		obj, computedETag, err := s.store.GetMultipartPart(r.Context(), bucket, uploadID, part.PartNumber)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeS3Error(w, r, http.StatusBadRequest, "InvalidPart", "missing part")
				return
			}
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if strings.Trim(part.ETag, "\"") != computedETag {
			writeS3Error(w, r, http.StatusBadRequest, "InvalidPart", "etag mismatch")
			return
		}
		if i < len(req.Parts)-1 && int64(len(obj.Content)) < minMultipartPartSize {
			writeS3Error(w, r, http.StatusBadRequest, "EntityTooSmall", "part size below minimum")
			return
		}
		combined = append(combined, obj.Content...)
	}

	eTag, err := s.store.PutObject(r.Context(), bucket, key, combined, "application/octet-stream", nil)
	if err != nil {
		switch {
		case errors.Is(err, ErrBucketNotFound):
			writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
		default:
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		}
		return
	}

	_ = s.store.UpdateObjectExtended(r.Context(), bucket, key, func(ext map[string][]byte) (map[string][]byte, error) {
		if ext == nil {
			ext = make(map[string][]byte)
		}
		ext["s3:etag"] = []byte(eTag)
		return ext, nil
	})
	_ = s.store.DeleteMultipartUpload(r.Context(), bucket, uploadID)

	writeXML(w, http.StatusOK, completeMultipartUploadResult{
		Xmlns:    s3XMLNS,
		Location: "/" + bucket + "/" + key,
		Bucket:   bucket,
		Key:      key,
		ETag:     "\"" + eTag + "\"",
	})
}

func (s *S3ApiServer) handleAbortMultipart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if r.Method != http.MethodDelete {
		writeS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
		return
	}
	uploadID := r.URL.Query().Get("uploadId")
	if _, err := s.store.LoadMultipartUpload(r.Context(), bucket, key, uploadID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeS3Error(w, r, http.StatusNotFound, "NoSuchUpload", "multipart upload not found")
			return
		}
		writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if err := s.store.DeleteMultipartUpload(r.Context(), bucket, uploadID); err != nil {
		writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func newUploadID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
