package s3api

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"

	"GoBlob/goblob/s3api/lifecycle"
)

func (s *S3ApiServer) handleBucketLifecycle(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodPut:
		defer r.Body.Close()
		body, err := io.ReadAll(io.LimitReader(r.Body, 512*1024))
		if err != nil {
			writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "failed to read lifecycle body")
			return
		}
		var cfg lifecycle.LifecycleConfiguration
		if err := xml.Unmarshal(body, &cfg); err != nil {
			writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "invalid lifecycle xml")
			return
		}
		if err := lifecycle.ValidateConfiguration(&cfg); err != nil {
			writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", err.Error())
			return
		}
		encoded, err := json.Marshal(cfg)
		if err != nil {
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if err := s.store.SetBucketMeta(r.Context(), bucket, "lifecycle.json", encoded); err != nil {
			if errors.Is(err, ErrBucketNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
				return
			}
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		raw, err := s.store.GetBucketMeta(r.Context(), bucket, "lifecycle.json")
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchLifecycleConfiguration", "lifecycle config not found")
				return
			}
			if errors.Is(err, ErrBucketNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
				return
			}
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		var cfg lifecycle.LifecycleConfiguration
		if err := json.Unmarshal(raw, &cfg); err != nil {
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", "invalid stored lifecycle configuration")
			return
		}
		cfg.XMLName = xml.Name{Local: "LifecycleConfiguration"}
		writeXML(w, http.StatusOK, cfg)
	case http.MethodDelete:
		if err := s.store.DeleteBucketMeta(r.Context(), bucket, "lifecycle.json"); err != nil {
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}
