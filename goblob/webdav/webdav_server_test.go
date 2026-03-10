package webdav

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerBasicAuthWrapper(t *testing.T) {
	s := NewServer(":0", NewFilerFileSystem(t.TempDir()), "user", "pass")

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	resp := httptest.NewRecorder()
	s.handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want=%d", resp.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodOptions, "/", nil)
	req.SetBasicAuth("user", "pass")
	resp = httptest.NewRecorder()
	s.handler.ServeHTTP(resp, req)
	if resp.Code == http.StatusUnauthorized {
		t.Fatalf("unexpected unauthorized for valid credentials")
	}
}
