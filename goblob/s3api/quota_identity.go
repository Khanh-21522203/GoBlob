package s3api

import (
	"net/http"
	"net/url"
	"strings"
)

func requestQuotaUserID(r *http.Request) string {
	if r == nil {
		return ""
	}
	if key := strings.TrimSpace(r.Header.Get("X-Access-Key")); key != "" {
		return key
	}
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "" {
		if idx := strings.Index(authz, "Credential="); idx >= 0 {
			rest := authz[idx+len("Credential="):]
			if cut := strings.Index(rest, ","); cut >= 0 {
				rest = rest[:cut]
			}
			if cut := strings.Index(rest, "/"); cut >= 0 {
				rest = rest[:cut]
			}
			if rest != "" {
				return strings.TrimSpace(rest)
			}
		}
	}
	if credential := r.URL.Query().Get("X-Amz-Credential"); credential != "" {
		if decoded, err := url.QueryUnescape(credential); err == nil {
			credential = decoded
		}
		if cut := strings.Index(credential, "/"); cut >= 0 {
			credential = credential[:cut]
		}
		return strings.TrimSpace(credential)
	}
	return ""
}
