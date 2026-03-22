package s3api

import (
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/obs"
	"GoBlob/goblob/quota"
	s3auth "GoBlob/goblob/s3api/auth"
	s3iam "GoBlob/goblob/s3api/iam"
)

// S3ApiServerOption configures S3 gateway behavior.
type S3ApiServerOption struct {
	Filers      []types.ServerAddress
	Port        int
	DomainName  string
	BucketsPath string
}

// DefaultS3ApiServerOption returns default S3 API server options.
func DefaultS3ApiServerOption() *S3ApiServerOption {
	return &S3ApiServerOption{
		Port:        types.DefaultS3HTTPPort,
		BucketsPath: "/buckets",
	}
}

// S3ApiServer is an S3-compatible gateway backed by filer metadata.
type S3ApiServer struct {
	option      *S3ApiServerOption
	filerClient *FilerClient
	iam         *s3iam.IdentityAccessManagement
	quota       *quota.Manager

	logger *slog.Logger
}

// NewS3ApiServer registers S3 handlers on mux and returns the server.
func NewS3ApiServer(mux *http.ServeMux, opt *S3ApiServerOption) (*S3ApiServer, error) {
	if opt == nil {
		opt = DefaultS3ApiServerOption()
	}
	if opt.BucketsPath == "" {
		opt.BucketsPath = "/buckets"
	}
	logger := obs.New("s3").With("server", "s3api")

	filerClient := NewFilerClient(opt.Filers, opt.BucketsPath)
	var iamClient s3iam.FilerIAMClient
	if len(opt.Filers) > 0 {
		iamClient = filerClient
	}
	iamMgr, err := s3iam.NewIdentityAccessManagement(iamClient, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize iam manager: %w", err)
	}

	s3 := &S3ApiServer{
		option:      opt,
		filerClient: filerClient,
		iam:         iamMgr,
		quota:       quota.NewManager(filerClient),
		logger:      logger,
	}

	if err := s3.filerClient.EnsureBucketsRoot(context.Background()); err != nil && err != ErrNoFilerConfigured {
		return nil, fmt.Errorf("ensure buckets root: %w", err)
	}

	if mux != nil {
		mux.HandleFunc("/health", s3.handleHealth)
		mux.HandleFunc("/ready", s3.handleReady)
		mux.HandleFunc("/", s3.handle)
	}
	return s3, nil
}

// Shutdown gracefully stops S3 API server resources.
func (s *S3ApiServer) Shutdown() {
	if s == nil {
		return
	}
	// IAM and quota managers are stateless (no background goroutines); drop references
	// so in-memory identity data is GC'd.
	s.iam = nil
	s.quota = nil
	s.filerClient = nil
}

// ReloadIAM allows tests or integrations to refresh IAM config from filer.
func (s *S3ApiServer) ReloadIAM(ctx context.Context) error {
	return s.iam.LoadFromFiler(ctx)
}

func (s *S3ApiServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (s *S3ApiServer) handleReady(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.filerClient == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("NOT READY"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("READY"))
}

func (s *S3ApiServer) handle(w http.ResponseWriter, r *http.Request) {
	bucket, key := splitBucketAndKey(r.URL.Path)

	if bucket == "" {
		if r.Method != http.MethodGet {
			writeS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed")
			return
		}
		if !s.authorize(w, r, s3iam.S3ActionListAllMyBuckets, "", "") {
			return
		}
		s.handleListBuckets(w, r)
		return
	}

	if r.Method == http.MethodOptions {
		s.handleCORSPreflight(w, r, bucket)
		return
	}

	q := r.URL.Query()
	if key == "" {
		s.handleBucketLevel(w, r, bucket, q)
		return
	}
	s.handleObjectLevel(w, r, bucket, key, q)
}

func (s *S3ApiServer) handleBucketLevel(w http.ResponseWriter, r *http.Request, bucket string, q map[string][]string) {
	// Query operations have priority.
	switch {
	case hasQueryKey(q, "versioning"):
		action := s3iam.S3ActionGetBucketVersioning
		if r.Method == http.MethodPut {
			action = s3iam.S3ActionPutBucketVersioning
		}
		if !s.authorize(w, r, action, bucket, "") {
			return
		}
		s.handleBucketVersioning(w, r, bucket)
		return
	case hasQueryKey(q, "policy"):
		action := s3iam.S3ActionGetBucketPolicy
		if r.Method == http.MethodPut {
			action = s3iam.S3ActionPutBucketPolicy
		}
		if r.Method == http.MethodDelete {
			action = s3iam.S3ActionDeleteBucketPolicy
		}
		if !s.authorize(w, r, action, bucket, "") {
			return
		}
		s.handleBucketPolicy(w, r, bucket)
		return
	case hasQueryKey(q, "cors"):
		action := s3iam.S3ActionGetBucketCors
		if r.Method == http.MethodPut {
			action = s3iam.S3ActionPutBucketCors
		}
		if !s.authorize(w, r, action, bucket, "") {
			return
		}
		s.handleBucketCORS(w, r, bucket)
		return
	case hasQueryKey(q, "lifecycle"):
		action := s3iam.S3ActionGetBucketLifecycle
		if r.Method == http.MethodPut {
			action = s3iam.S3ActionPutBucketLifecycle
		}
		if r.Method == http.MethodDelete {
			action = s3iam.S3ActionDeleteBucketLifecycle
		}
		if !s.authorize(w, r, action, bucket, "") {
			return
		}
		s.handleBucketLifecycle(w, r, bucket)
		return
	case hasQueryKey(q, "delete"):
		if !s.authorize(w, r, s3iam.S3ActionDeleteObject, bucket, "") {
			return
		}
		s.handleDeleteObjects(w, r, bucket)
		return
	}

	switch r.Method {
	case http.MethodPut:
		if !s.authorize(w, r, s3iam.S3ActionCreateBucket, bucket, "") {
			return
		}
		s.handleCreateBucket(w, r, bucket)
	case http.MethodDelete:
		if !s.authorize(w, r, s3iam.S3ActionDeleteBucket, bucket, "") {
			return
		}
		s.handleDeleteBucket(w, r, bucket)
	case http.MethodGet:
		if !s.authorize(w, r, s3iam.S3ActionListBucket, bucket, "") {
			return
		}
		s.handleListObjects(w, r, bucket)
	case http.MethodHead:
		if !s.authorize(w, r, s3iam.S3ActionListBucket, bucket, "") {
			return
		}
		s.handleHeadBucket(w, r, bucket)
	default:
		writeS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed")
	}
}

func (s *S3ApiServer) handleObjectLevel(w http.ResponseWriter, r *http.Request, bucket, key string, q map[string][]string) {
	switch {
	case hasQueryKey(q, "uploads"):
		if !s.authorize(w, r, s3iam.S3ActionCreateMultipart, bucket, key) {
			return
		}
		s.handleInitiateMultipart(w, r, bucket, key)
		return
	case hasQueryKey(q, "uploadId") && hasQueryKey(q, "partNumber"):
		if !s.authorize(w, r, s3iam.S3ActionUploadPart, bucket, key) {
			return
		}
		s.handleUploadPart(w, r, bucket, key)
		return
	case hasQueryKey(q, "uploadId") && r.Method == http.MethodPost:
		if !s.authorize(w, r, s3iam.S3ActionCompleteMultipart, bucket, key) {
			return
		}
		s.handleCompleteMultipart(w, r, bucket, key)
		return
	case hasQueryKey(q, "uploadId") && r.Method == http.MethodDelete:
		if !s.authorize(w, r, s3iam.S3ActionAbortMultipart, bucket, key) {
			return
		}
		s.handleAbortMultipart(w, r, bucket, key)
		return
	case hasQueryKey(q, "tagging"):
		action := s3iam.S3ActionGetObjectTagging
		if r.Method == http.MethodPut {
			action = s3iam.S3ActionPutObjectTagging
		}
		if r.Method == http.MethodDelete {
			action = s3iam.S3ActionDeleteObjectTagging
		}
		if !s.authorize(w, r, action, bucket, key) {
			return
		}
		s.handleObjectTagging(w, r, bucket, key)
		return
	}

	switch r.Method {
	case http.MethodPut:
		if !s.authorize(w, r, s3iam.S3ActionPutObject, bucket, key) {
			return
		}
		s.handlePutObject(w, r, bucket, key)
	case http.MethodGet:
		if !s.authorize(w, r, s3iam.S3ActionGetObject, bucket, key) {
			return
		}
		s.handleGetObject(w, r, bucket, key, true)
	case http.MethodHead:
		if !s.authorize(w, r, s3iam.S3ActionHeadObject, bucket, key) {
			return
		}
		s.handleGetObject(w, r, bucket, key, false)
	case http.MethodDelete:
		if !s.authorize(w, r, s3iam.S3ActionDeleteObject, bucket, key) {
			return
		}
		s.handleDeleteObject(w, r, bucket, key)
	default:
		writeS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed")
	}
}

func splitBucketAndKey(rawPath string) (bucket, key string) {
	cleaned := path.Clean("/" + rawPath)
	if cleaned == "/" {
		return "", ""
	}
	parts := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	if len(parts) == 0 {
		return "", ""
	}
	bucket = parts[0]
	if len(parts) > 1 {
		key = strings.Join(parts[1:], "/")
	}
	return bucket, key
}

func hasQueryKey(q map[string][]string, key string) bool {
	_, ok := q[key]
	return ok
}

func (s *S3ApiServer) authorize(w http.ResponseWriter, r *http.Request, action s3iam.S3Action, bucket, key string) bool {
	res := s3auth.VerifyRequest(r, s.iam, action, bucket, key, time.Now().UTC())
	if res.ErrorCode == "" {
		return true
	}
	statusCode := http.StatusForbidden
	switch res.ErrorCode {
	case "SignatureDoesNotMatch":
		statusCode = http.StatusForbidden
	case "InvalidAccessKeyId":
		statusCode = http.StatusForbidden
	case "AuthorizationHeaderMalformed", "AuthorizationQueryParametersError":
		statusCode = http.StatusBadRequest
	case "RequestExpired":
		statusCode = http.StatusForbidden
	case "AccessDenied":
		statusCode = http.StatusForbidden
	}
	writeS3Error(w, r, statusCode, res.ErrorCode, "request authentication failed")
	return false
}

func writeXML(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(v)
}
