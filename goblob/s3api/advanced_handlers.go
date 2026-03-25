package s3api

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
)

func (s *S3ApiServer) getBucketVersioningState(ctx context.Context, bucket string) (string, error) {
	data, err := s.store.GetBucketMeta(ctx, bucket, "versioning")
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		if errors.Is(err, ErrBucketNotFound) {
			return "", ErrBucketNotFound
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s *S3ApiServer) handleBucketVersioning(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodGet:
		state, err := s.getBucketVersioningState(r.Context(), bucket)
		if err != nil {
			if errors.Is(err, ErrBucketNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
				return
			}
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		writeXML(w, http.StatusOK, versioningConfiguration{Xmlns: s3XMLNS, Status: state})
	case http.MethodPut:
		defer r.Body.Close()
		body, err := io.ReadAll(io.LimitReader(r.Body, 256*1024))
		if err != nil {
			writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "failed to read versioning body")
			return
		}
		var cfg versioningConfiguration
		if err := xml.Unmarshal(body, &cfg); err != nil {
			writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "invalid versioning xml")
			return
		}
		cfg.Status = strings.TrimSpace(cfg.Status)
		if cfg.Status != "" && cfg.Status != "Enabled" && cfg.Status != "Suspended" {
			writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "invalid versioning status")
			return
		}
		if cfg.Status == "" {
			_ = s.store.DeleteBucketMeta(r.Context(), bucket, "versioning")
		} else if err := s.store.SetBucketMeta(r.Context(), bucket, "versioning", []byte(cfg.Status)); err != nil {
			if errors.Is(err, ErrBucketNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
				return
			}
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		writeS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *S3ApiServer) handleBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.GetBucketMeta(r.Context(), bucket, "policy")
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchBucketPolicy", "bucket policy not found")
				return
			}
			if errors.Is(err, ErrBucketNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
				return
			}
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodPut:
		defer r.Body.Close()
		body, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024))
		if err != nil {
			writeS3Error(w, r, http.StatusBadRequest, "MalformedPolicy", "failed to read policy")
			return
		}
		if err := s.store.SetBucketMeta(r.Context(), bucket, "policy", body); err != nil {
			if errors.Is(err, ErrBucketNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
				return
			}
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := s.store.DeleteBucketMeta(r.Context(), bucket, "policy"); err != nil {
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *S3ApiServer) handleBucketCORS(w http.ResponseWriter, r *http.Request, bucket string) {
	switch r.Method {
	case http.MethodGet:
		data, err := s.store.GetBucketMeta(r.Context(), bucket, "cors")
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchCORSConfiguration", "cors config not found")
				return
			}
			if errors.Is(err, ErrBucketNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
				return
			}
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	case http.MethodPut:
		defer r.Body.Close()
		body, err := io.ReadAll(io.LimitReader(r.Body, 512*1024))
		if err != nil {
			writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "failed to read cors body")
			return
		}
		var cfg corsConfiguration
		if err := xml.Unmarshal(body, &cfg); err != nil {
			writeS3Error(w, r, http.StatusBadRequest, "MalformedXML", "invalid cors xml")
			return
		}
		cfg.Xmlns = s3XMLNS
		encoded, _ := xml.Marshal(cfg)
		if err := s.store.SetBucketMeta(r.Context(), bucket, "cors", encoded); err != nil {
			if errors.Is(err, ErrBucketNotFound) {
				writeS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "bucket not found")
				return
			}
			writeS3Error(w, r, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		writeS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (s *S3ApiServer) handleCORSPreflight(w http.ResponseWriter, r *http.Request, bucket string) {
	origin := r.Header.Get("Origin")
	method := r.Header.Get("Access-Control-Request-Method")
	if origin == "" || method == "" {
		writeS3Error(w, r, http.StatusForbidden, "AccessDenied", "cors preflight denied")
		return
	}
	data, err := s.store.GetBucketMeta(r.Context(), bucket, "cors")
	if err != nil {
		writeS3Error(w, r, http.StatusForbidden, "AccessDenied", "cors preflight denied")
		return
	}
	var cfg corsConfiguration
	if err := xml.Unmarshal(data, &cfg); err != nil {
		writeS3Error(w, r, http.StatusForbidden, "AccessDenied", "cors preflight denied")
		return
	}
	for _, rule := range cfg.Rules {
		if !corsOriginAllowed(rule.AllowedOrigins, origin) || !corsMethodAllowed(rule.AllowedMethods, method) {
			continue
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", strings.Join(rule.AllowedMethods, ", "))
		if len(rule.AllowedHeaders) > 0 {
			w.Header().Set("Access-Control-Allow-Headers", strings.Join(rule.AllowedHeaders, ", "))
		}
		if len(rule.ExposeHeaders) > 0 {
			w.Header().Set("Access-Control-Expose-Headers", strings.Join(rule.ExposeHeaders, ", "))
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	writeS3Error(w, r, http.StatusForbidden, "AccessDenied", "cors preflight denied")
}

func corsOriginAllowed(allowed []string, origin string) bool {
	for _, candidate := range allowed {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.EqualFold(candidate, origin) {
			return true
		}
	}
	return false
}

func corsMethodAllowed(allowed []string, method string) bool {
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), method) {
			return true
		}
	}
	return false
}
