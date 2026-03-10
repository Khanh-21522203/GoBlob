package auth

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	iampb "GoBlob/goblob/pb/iam_pb"
	s3iam "GoBlob/goblob/s3api/iam"
)

func TestVerifyRequestSigV4(t *testing.T) {
	iamMgr := mustIAM(t, &iampb.S3ApiConfiguration{Identities: []*iampb.Identity{{
		Name:        "dev",
		Credentials: []*iampb.Credential{{AccessKey: "AKID", SecretKey: "SECRET"}},
		Actions:     []string{"Read"},
	}}})

	req := httptest.NewRequest(http.MethodGet, "http://s3.local/photos/cat.jpg", nil)
	req.Host = "s3.local"
	signHeaderRequest(req, "AKID", "SECRET", "us-east-1", "s3", "20260310T030405Z")

	res := VerifyRequest(req, iamMgr, s3iam.S3ActionGetObject, "photos", "cat.jpg", time.Date(2026, 3, 10, 3, 10, 0, 0, time.UTC))
	if res.ErrorCode != "" {
		t.Fatalf("expected auth success, got %s", res.ErrorCode)
	}
	if res.Identity == nil || res.Identity.Name != "dev" {
		t.Fatalf("unexpected identity: %#v", res.Identity)
	}
}

func TestVerifyRequestSignatureMismatch(t *testing.T) {
	iamMgr := mustIAM(t, &iampb.S3ApiConfiguration{Identities: []*iampb.Identity{{
		Name:        "dev",
		Credentials: []*iampb.Credential{{AccessKey: "AKID", SecretKey: "SECRET"}},
		Actions:     []string{"Read"},
	}}})

	req := httptest.NewRequest(http.MethodGet, "http://s3.local/photos/cat.jpg", nil)
	req.Host = "s3.local"
	signHeaderRequest(req, "AKID", "SECRET", "us-east-1", "s3", "20260310T030405Z")
	req.URL.Path = "/photos/cat2.jpg" // tamper after signing

	res := VerifyRequest(req, iamMgr, s3iam.S3ActionGetObject, "photos", "cat2.jpg", time.Date(2026, 3, 10, 3, 10, 0, 0, time.UTC))
	if res.ErrorCode != "SignatureDoesNotMatch" {
		t.Fatalf("expected SignatureDoesNotMatch, got %q", res.ErrorCode)
	}
}

func TestVerifyRequestPresignedExpired(t *testing.T) {
	iamMgr := mustIAM(t, &iampb.S3ApiConfiguration{Identities: []*iampb.Identity{{
		Name:        "dev",
		Credentials: []*iampb.Credential{{AccessKey: "AKID", SecretKey: "SECRET"}},
		Actions:     []string{"Read"},
	}}})

	req := httptest.NewRequest(http.MethodGet, "http://s3.local/photos/cat.jpg", nil)
	req.Host = "s3.local"
	signPresignedRequest(req, "AKID", "SECRET", "us-east-1", "s3", "20260310T030405Z", 30)

	now := time.Date(2026, 3, 10, 3, 6, 0, 0, time.UTC) // 115s later
	res := VerifyRequest(req, iamMgr, s3iam.S3ActionGetObject, "photos", "cat.jpg", now)
	if res.ErrorCode != "RequestExpired" {
		t.Fatalf("expected RequestExpired, got %q", res.ErrorCode)
	}
}

func signHeaderRequest(req *http.Request, accessKey, secret, region, service, amzDate string) {
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", emptyPayloadSHA256)
	signedHeaders := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	date := amzDate[:8]
	scope := date + "/" + region + "/" + service + "/aws4_request"

	sig := &parsedSignature{
		AccessKey:     accessKey,
		Date:          date,
		Region:        region,
		Service:       service,
		SignedHeaders: signedHeaders,
		AmzDate:       amzDate,
		Scope:         scope,
	}
	canonical := buildCanonicalRequest(req, sig)
	stringToSign := buildStringToSign(sig, canonical)
	signingKey := s3iam.DeriveSigningKey(secret, date, region, service)
	signature := hmacHex(signingKey, stringToSign)
	credential := accessKey + "/" + scope

	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+credential+", SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature="+signature)
}

func signPresignedRequest(req *http.Request, accessKey, secret, region, service, amzDate string, expires int64) {
	q := req.URL.Query()
	date := amzDate[:8]
	scope := date + "/" + region + "/" + service + "/aws4_request"
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", accessKey+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.FormatInt(expires, 10))
	q.Set("X-Amz-SignedHeaders", "host")
	req.URL.RawQuery = q.Encode()

	sig := &parsedSignature{
		AccessKey:     accessKey,
		Date:          date,
		Region:        region,
		Service:       service,
		SignedHeaders: []string{"host"},
		AmzDate:       amzDate,
		Scope:         scope,
		Expires:       expires,
		Presigned:     true,
	}
	canonical := buildCanonicalRequest(req, sig)
	stringToSign := buildStringToSign(sig, canonical)
	signingKey := s3iam.DeriveSigningKey(secret, date, region, service)
	signature := hmacHex(signingKey, stringToSign)

	q.Set("X-Amz-Signature", signature)
	req.URL.RawQuery = q.Encode()
}

func mustIAM(t *testing.T, cfg *iampb.S3ApiConfiguration) *s3iam.IdentityAccessManagement {
	t.Helper()
	iamMgr, err := s3iam.NewIdentityAccessManagement(nil, nil)
	if err != nil {
		t.Fatalf("NewIdentityAccessManagement: %v", err)
	}
	if err := iamMgr.Reload(cfg); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	return iamMgr
}
