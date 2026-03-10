package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	s3iam "GoBlob/goblob/s3api/iam"
)

const (
	emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

type AuthType int

const (
	AuthTypeAnonymous AuthType = iota
	AuthTypeSigV4
	AuthTypePresigned
)

// VerifyResult is the result of SigV4 auth + IAM authorization.
type VerifyResult struct {
	Identity  *s3iam.Identity
	ErrorCode string
}

type parsedSignature struct {
	AccessKey     string
	Date          string
	Region        string
	Service       string
	SignedHeaders []string
	Signature     string
	AmzDate       string
	Scope         string
	Expires       int64
	Presigned     bool
}

// VerifyRequest verifies authentication and IAM authorization for a request.
func VerifyRequest(r *http.Request, iamMgr *s3iam.IdentityAccessManagement, action s3iam.S3Action, bucket, object string, now time.Time) VerifyResult {
	if iamMgr == nil {
		return VerifyResult{}
	}
	cfg := iamMgr.GetConfiguration()
	if len(cfg.GetIdentities()) == 0 {
		return VerifyResult{}
	}
	authType := detectAuthType(r)
	if authType == AuthTypeAnonymous {
		return VerifyResult{ErrorCode: "AccessDenied"}
	}

	parsed, err := parseSigV4(r, authType)
	if err != nil {
		return VerifyResult{ErrorCode: "AuthorizationHeaderMalformed"}
	}

	authRes := iamMgr.Authenticate(parsed.AccessKey)
	if authRes.ErrorCode != "" {
		return VerifyResult{ErrorCode: authRes.ErrorCode}
	}

	if parsed.Presigned {
		if parsed.Expires < 0 {
			return VerifyResult{ErrorCode: "AuthorizationQueryParametersError"}
		}
		requestTime, err := time.Parse("20060102T150405Z", parsed.AmzDate)
		if err != nil {
			return VerifyResult{ErrorCode: "AuthorizationQueryParametersError"}
		}
		if now.After(requestTime.Add(time.Duration(parsed.Expires) * time.Second)) {
			return VerifyResult{ErrorCode: "RequestExpired"}
		}
	}

	canonicalRequest := buildCanonicalRequest(r, parsed)
	stringToSign := buildStringToSign(parsed, canonicalRequest)
	signingKey := s3iam.DeriveSigningKey(authRes.SecretKey, parsed.Date, parsed.Region, parsed.Service)
	expectedSig := hmacHex(signingKey, stringToSign)
	if subtle.ConstantTimeCompare([]byte(expectedSig), []byte(parsed.Signature)) != 1 {
		return VerifyResult{ErrorCode: "SignatureDoesNotMatch"}
	}

	if !iamMgr.IsAuthorized(authRes.Identity, action, bucket, object) {
		return VerifyResult{ErrorCode: "AccessDenied"}
	}
	return VerifyResult{Identity: authRes.Identity}
}

func detectAuthType(r *http.Request) AuthType {
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(authz, "AWS4-HMAC-SHA256 ") {
		return AuthTypeSigV4
	}
	if strings.EqualFold(r.URL.Query().Get("X-Amz-Algorithm"), "AWS4-HMAC-SHA256") {
		return AuthTypePresigned
	}
	return AuthTypeAnonymous
}

func parseSigV4(r *http.Request, authType AuthType) (*parsedSignature, error) {
	if authType == AuthTypePresigned {
		return parsePresigned(r)
	}
	return parseAuthorizationHeader(r)
}

func parseAuthorizationHeader(r *http.Request) (*parsedSignature, error) {
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(authz, "AWS4-HMAC-SHA256 ") {
		return nil, fmt.Errorf("missing v4 authorization")
	}
	kvRaw := strings.TrimPrefix(authz, "AWS4-HMAC-SHA256 ")
	parts := strings.Split(kvRaw, ",")
	attrs := make(map[string]string, len(parts))
	for _, part := range parts {
		p := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(p) != 2 {
			continue
		}
		attrs[p[0]] = p[1]
	}
	credential := attrs["Credential"]
	signedHeaders := attrs["SignedHeaders"]
	signature := attrs["Signature"]
	if credential == "" || signedHeaders == "" || signature == "" {
		return nil, fmt.Errorf("missing authorization fields")
	}
	accessKey, date, region, service, scope, err := parseCredentialScope(credential)
	if err != nil {
		return nil, err
	}
	amzDate := r.Header.Get("X-Amz-Date")
	if amzDate == "" {
		amzDate = r.Header.Get("Date")
	}
	if amzDate == "" {
		return nil, fmt.Errorf("missing x-amz-date")
	}
	headers := splitAndNormalizeHeaderList(signedHeaders)
	return &parsedSignature{
		AccessKey:     accessKey,
		Date:          date,
		Region:        region,
		Service:       service,
		SignedHeaders: headers,
		Signature:     signature,
		AmzDate:       amzDate,
		Scope:         scope,
	}, nil
}

func parsePresigned(r *http.Request) (*parsedSignature, error) {
	q := r.URL.Query()
	credential, err := url.QueryUnescape(q.Get("X-Amz-Credential"))
	if err != nil {
		return nil, fmt.Errorf("decode credential: %w", err)
	}
	accessKey, date, region, service, scope, err := parseCredentialScope(credential)
	if err != nil {
		return nil, err
	}
	signedHeaders := splitAndNormalizeHeaderList(q.Get("X-Amz-SignedHeaders"))
	sig := q.Get("X-Amz-Signature")
	if len(signedHeaders) == 0 || sig == "" {
		return nil, fmt.Errorf("missing presigned fields")
	}
	expires, err := strconv.ParseInt(q.Get("X-Amz-Expires"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid x-amz-expires: %w", err)
	}
	return &parsedSignature{
		AccessKey:     accessKey,
		Date:          date,
		Region:        region,
		Service:       service,
		SignedHeaders: signedHeaders,
		Signature:     sig,
		AmzDate:       q.Get("X-Amz-Date"),
		Scope:         scope,
		Expires:       expires,
		Presigned:     true,
	}, nil
}

func parseCredentialScope(credential string) (accessKey, date, region, service, scope string, err error) {
	parts := strings.Split(credential, "/")
	if len(parts) != 5 {
		return "", "", "", "", "", fmt.Errorf("invalid credential scope")
	}
	if parts[4] != "aws4_request" {
		return "", "", "", "", "", fmt.Errorf("invalid credential terminator")
	}
	return parts[0], parts[1], parts[2], parts[3], strings.Join(parts[1:], "/"), nil
}

func splitAndNormalizeHeaderList(signedHeaders string) []string {
	items := strings.Split(signedHeaders, ";")
	out := make([]string, 0, len(items))
	for _, item := range items {
		i := strings.ToLower(strings.TrimSpace(item))
		if i == "" {
			continue
		}
		out = append(out, i)
	}
	sort.Strings(out)
	return out
}

func buildCanonicalRequest(r *http.Request, sig *parsedSignature) string {
	canonicalURI := canonicalURI(r.URL.Path)
	canonicalQuery := canonicalQueryString(r.URL.Query(), sig.Presigned)
	canonicalHeaders, signedHeaders := canonicalHeadersString(r, sig.SignedHeaders)
	payloadHash := requestPayloadHash(r, sig.Presigned)

	return strings.Join([]string{
		r.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
}

func buildStringToSign(sig *parsedSignature, canonicalRequest string) string {
	h := sha256.Sum256([]byte(canonicalRequest))
	return strings.Join([]string{
		"AWS4-HMAC-SHA256",
		sig.AmzDate,
		sig.Scope,
		hex.EncodeToString(h[:]),
	}, "\n")
}

func hmacHex(key []byte, payload string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func canonicalURI(rawPath string) string {
	p := rawPath
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	segments := strings.Split(p, "/")
	for i, seg := range segments {
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			decoded = seg
		}
		segments[i] = awsEncode(decoded, true)
	}
	result := strings.Join(segments, "/")
	if result == "" {
		return "/"
	}
	if !strings.HasPrefix(result, "/") {
		result = "/" + result
	}
	return result
}

func canonicalQueryString(q url.Values, presigned bool) string {
	type pair struct{ k, v string }
	pairs := make([]pair, 0)
	for k, values := range q {
		if presigned && strings.EqualFold(k, "X-Amz-Signature") {
			continue
		}
		sortedValues := append([]string(nil), values...)
		sort.Strings(sortedValues)
		if len(sortedValues) == 0 {
			pairs = append(pairs, pair{k: k, v: ""})
			continue
		}
		for _, v := range sortedValues {
			pairs = append(pairs, pair{k: k, v: v})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k == pairs[j].k {
			return pairs[i].v < pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	encoded := make([]string, 0, len(pairs))
	for _, p := range pairs {
		encoded = append(encoded, awsEncode(p.k, true)+"="+awsEncode(p.v, true))
	}
	return strings.Join(encoded, "&")
}

func canonicalHeadersString(r *http.Request, signedHeaders []string) (string, string) {
	headers := append([]string(nil), signedHeaders...)
	sort.Strings(headers)
	lines := make([]string, 0, len(headers))
	for _, h := range headers {
		value := ""
		if h == "host" {
			value = r.Host
		} else {
			values := r.Header.Values(h)
			value = strings.Join(values, ",")
		}
		value = compressSpaces(strings.TrimSpace(value))
		lines = append(lines, h+":"+value)
	}
	return strings.Join(lines, "\n") + "\n", strings.Join(headers, ";")
}

func requestPayloadHash(r *http.Request, presigned bool) string {
	if presigned {
		if v := r.URL.Query().Get("X-Amz-Content-Sha256"); v != "" {
			return v
		}
		return "UNSIGNED-PAYLOAD"
	}
	if v := r.Header.Get("X-Amz-Content-Sha256"); v != "" {
		return v
	}
	return emptyPayloadSHA256
}

func compressSpaces(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

func awsEncode(s string, encodeSlash bool) string {
	escaped := url.QueryEscape(s)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	escaped = strings.ReplaceAll(escaped, "*", "%2A")
	escaped = strings.ReplaceAll(escaped, "%7E", "~")
	if !encodeSlash {
		escaped = strings.ReplaceAll(escaped, "%2F", "/")
	}
	return escaped
}
