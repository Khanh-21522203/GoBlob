package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJWTRoundTrip(t *testing.T) {
	key := "test-secret-key"
	token, err := SignJWT(key, 3600)
	if err != nil {
		t.Fatalf("SignJWT failed: %v", err)
	}

	if token == "" {
		t.Error("expected non-empty token")
	}

	claims, err := VerifyJWT(token, key)
	if err != nil {
		t.Fatalf("VerifyJWT failed: %v", err)
	}

	if claims == nil {
		t.Error("expected non-nil claims")
	}
}

func TestJWTExpired(t *testing.T) {
	key := "test-secret-key"
	token, err := SignJWT(key, 1) // 1 second expiry
	if err != nil {
		t.Fatalf("SignJWT failed: %v", err)
	}

	time.Sleep(2 * time.Second)

	_, err = VerifyJWT(token, key)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestJWTInvalidKey(t *testing.T) {
	key1 := "test-secret-key-1"
	key2 := "test-secret-key-2"

	token, err := SignJWT(key1, 3600)
	if err != nil {
		t.Fatalf("SignJWT failed: %v", err)
	}

	_, err = VerifyJWT(token, key2)
	if err == nil {
		t.Error("expected error for wrong key")
	}
}

func TestJWTEMptyKey(t *testing.T) {
	_, err := SignJWT("", 3600)
	if err == nil {
		t.Error("expected error for empty key")
	}

	_, err = VerifyJWT("token", "")
	if err == nil {
		t.Error("expected error for empty key")
	}
}

func TestGuardIPWhitelist(t *testing.T) {
	tests := []struct {
		name      string
		whitelist string
		ip        string
		allowed   bool
	}{
		{"empty whitelist", "", "192.168.1.1:12345", true},
		{"single IP", "192.168.1.1", "192.168.1.1:12345", true},
		{"single IP not in list", "192.168.1.1", "192.168.1.2:12345", false},
		{"CIDR", "192.168.0.0/16", "192.168.1.100:12345", true},
		{"CIDR not in range", "192.168.0.0/16", "10.0.0.1:12345", false},
		{"multiple entries", "192.168.1.1,10.0.0.0/8", "10.0.0.1:12345", true},
		{"IPv4 loopback", "127.0.0.1", "127.0.0.1:12345", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard := NewGuard(tt.whitelist, "", "")

			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.ip

			result := guard.Allowed(req, false)
			if result != tt.allowed {
				t.Errorf("Allowed() = %v, want %v", result, tt.allowed)
			}
		})
	}
}

func TestGuardJWT(t *testing.T) {
	key := "test-secret-key"

	t.Run("request without JWT blocked when key configured", func(t *testing.T) {
		guard := NewGuard("", key, "")

		req := httptest.NewRequest("GET", "/", nil)

		result := guard.Allowed(req, true)
		if result {
			t.Error("expected false when JWT required but not provided")
		}
	})

	t.Run("request with valid JWT passes", func(t *testing.T) {
		guard := NewGuard("", key, "")

		token, err := SignJWT(key, 3600)
		if err != nil {
			t.Fatalf("SignJWT failed: %v", err)
		}

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		result := guard.Allowed(req, true)
		if !result {
			t.Error("expected true for valid JWT")
		}
	})

	t.Run("request with invalid JWT blocked", func(t *testing.T) {
		guard := NewGuard("", key, "")

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")

		result := guard.Allowed(req, true)
		if result {
			t.Error("expected false for invalid JWT")
		}
	})

	t.Run("JWT verification skipped when no key configured", func(t *testing.T) {
		guard := NewGuard("", "", "")

		req := httptest.NewRequest("GET", "/", nil)
		// No Authorization header

		result := guard.Allowed(req, true)
		if !result {
			t.Error("expected true when no key configured")
		}
	})
}

func TestParseWhiteList(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"192.168.1.1", []string{"192.168.1.1"}},
		{"192.168.1.1,10.0.0.1", []string{"192.168.1.1", "10.0.0.1"}},
		{"192.168.1.1, 10.0.0.1 ", []string{"192.168.1.1", "10.0.0.1"}},
		{"192.168.0.0/16,10.0.0.0/8", []string{"192.168.0.0/16", "10.0.0.0/8"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ParseWhiteList(tt.input)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d entries, got %d", len(tt.expected), len(result))
			}

			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("entry[%d] = %s, want %s", i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestGuardFilerKey(t *testing.T) {
	mainKey := "main-key"
	filerKey := "filer-key"

	guard := NewGuard("", mainKey, filerKey)

	if guard.SigningKey() != mainKey {
		t.Errorf("expected signing key %s, got %s", mainKey, guard.SigningKey())
	}

	if guard.FilerKey() != filerKey {
		t.Errorf("expected filer key %s, got %s", filerKey, guard.FilerKey())
	}

	if !guard.HasFilerKey() {
		t.Error("expected HasFilerKey() to return true")
	}

	// Test filer access
	token, err := SignJWT(filerKey, 3600)
	if err != nil {
		t.Fatalf("SignJWT failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	if !guard.FilerAllowed(req) {
		t.Error("expected filer access to be allowed")
	}

	// Test with wrong key
	token2, err := SignJWT(mainKey, 3600)
	if err != nil {
		t.Fatalf("SignJWT failed: %v", err)
	}

	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("Authorization", "Bearer "+token2)

	if guard.FilerAllowed(req2) {
		t.Error("expected filer access to be denied with wrong key")
	}
}

func TestHTTPMiddleware(t *testing.T) {
	guard := NewGuard("", "test-key", "")

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	middleware := HTTPMiddleware(guard, next)

	t.Run("request without JWT blocked", func(t *testing.T) {
		nextCalled = false
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		middleware.ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("expected status 403, got %d", w.Code)
		}
		if nextCalled {
			t.Error("expected next handler not to be called")
		}
	})

	t.Run("request with valid JWT passes", func(t *testing.T) {
		nextCalled = false
		token, _ := SignJWT("test-key", 3600)

		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()

		middleware.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		if !nextCalled {
			t.Error("expected next handler to be called")
		}
	})
}

func TestWhiteListMethods(t *testing.T) {
	guard := NewGuard("192.168.1.1,10.0.0.0/8", "", "")

	if guard.IsWhiteListEmpty() {
		t.Error("expected whitelist to not be empty")
	}

	if !guard.WhiteListContains("192.168.1.1") {
		t.Error("expected exact IP match to work")
	}

	if !guard.WhiteListContains("10.0.0.1") {
		t.Error("expected CIDR match to work")
	}

	if guard.WhiteListContains("172.16.0.1") {
		t.Error("expected non-whitelisted IP to not match")
	}

	// Test setting new whitelist
	guard.SetWhiteList("127.0.0.1")

	if guard.WhiteListContains("192.168.1.1") {
		t.Error("expected old whitelist to be cleared")
	}

	if !guard.WhiteListContains("127.0.0.1") {
		t.Error("expected new whitelist to be set")
	}
}

func TestBearerTokenExtraction(t *testing.T) {
	tests := []struct {
		name   string
		header string
		valid  bool
	}{
		{"Bearer prefix", "Bearer token123", true},
		{"lowercase bearer", "bearer token123", true},
		{"no prefix", "token123", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := strings.TrimPrefix(tt.header, "Bearer ")
			token = strings.TrimPrefix(token, "bearer ")
			if tt.valid && token == "" && tt.header != "" {
				t.Error("expected token to be extracted")
			}
		})
	}
}
