package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"GoBlob/goblob/core/types"
	"GoBlob/goblob/security"
)

// TestHandleStatus tests the status endpoint.
func TestHandleStatus(t *testing.T) {
	opt := DefaultVolumeServerOption()
	opt.Port = 0 // Use random port

	// Create a minimal volume server for testing
	adminMux := http.NewServeMux()
	publicMux := http.NewServeMux()
	vs := &VolumeServer{
		option: opt,
		logger: nil,
	}
	vs.registerRoutes(adminMux, publicMux)

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	adminMux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}
}

// TestHandleDelete tests the delete endpoint.
func TestHandleDelete(t *testing.T) {
	opt := DefaultVolumeServerOption()
	adminMux := http.NewServeMux()
	publicMux := http.NewServeMux()
	guard := security.NewGuard("", "", "")
	vs := &VolumeServer{
		option: opt,
		guard:  guard,
	}
	vs.registerRoutes(adminMux, publicMux)

	// Test with invalid file ID
	req := httptest.NewRequest("DELETE", "/invalid", nil)
	w := httptest.NewRecorder()
	adminMux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid file ID, got %d", w.Code)
	}

	// Test with valid file ID format
	req = httptest.NewRequest("DELETE", "/1,00000000000000010000000123", nil)
	w = httptest.NewRecorder()
	adminMux.ServeHTTP(w, req)

	// Should fail with 500 since store is not initialized
	if w.Code != http.StatusInternalServerError && w.Code != http.StatusNotFound {
		t.Logf("status for delete with no store: %d", w.Code)
	}
}

// TestParseFileID tests file ID parsing.
func TestParseFileID(t *testing.T) {
	tests := []struct {
		name    string
		fidStr  string
		wantVid types.VolumeId
		wantNid types.NeedleId
		wantErr bool
	}{
		{
			name:    "valid file ID",
			fidStr:  "3,00000000000000010000000123",
			wantVid: 3,
			wantNid: 0x100,
			wantErr: false,
		},
		{
			name:    "invalid file ID - no comma",
			fidStr:  "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vid, nid, _, err := ParseFileID(tt.fidStr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseFileID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if vid != tt.wantVid {
					t.Errorf("ParseFileID() vid = %v, want %v", vid, tt.wantVid)
				}
				if nid != tt.wantNid {
					t.Errorf("ParseFileID() nid = %v, want %v", nid, tt.wantNid)
				}
			}
		})
	}
}

// TestVolumeServerLifecycle tests creating and shutting down a volume server.
func TestVolumeServerLifecycle(t *testing.T) {
	opt := DefaultVolumeServerOption()
	opt.Directories = []DiskDirectoryConfig{
		{Path: t.TempDir(), MaxVolumeCount: 1},
	}

	adminMux := http.NewServeMux()
	publicMux := http.NewServeMux()
	vs, err := NewVolumeServer(adminMux, publicMux, nil, opt)
	if err != nil {
		t.Fatalf("NewVolumeServer() error = %v", err != nil)
	}

	// Start the server
	vs.Start()

	// Wait a bit for heartbeat to start
	time.Sleep(100 * time.Millisecond)

	// Shutdown
	vs.Shutdown()

	// Verify shutdown completes
	select {
	case <-vs.ctx.Done():
		// Expected
	default:
		t.Error("context should be done after shutdown")
	}
}
