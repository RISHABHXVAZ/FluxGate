package httpedge_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fluxgate/internal/httpedge"
	"fluxgate/internal/ratelimit"
)

// Unit tests for HTTP edge security protections.
// Authored by QA / Tester: Rishabh Singh (Reg: 20243235)

func TestHandler_AdminAuth_Enforcement(t *testing.T) {
	h := httpedge.NewHandler(nil, nil, nil)
	h.SetAdminKey("super-secret-key-qa")

	// 1. Unauthenticated GET /api/v1/config -> 401
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/config?key=test", nil)
	recGet := httptest.NewRecorder()
	h.HandleGetConfig(recGet, reqGet)

	if recGet.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on unauthenticated GET, got %d", recGet.Code)
	}

	var errResp ratelimit.ErrorEnvelope
	if err := json.NewDecoder(recGet.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error json: %v", err)
	}
	if errResp.Error.Code != "ADMIN_AUTH_REQUIRED" {
		t.Errorf("expected ADMIN_AUTH_REQUIRED code, got %s", errResp.Error.Code)
	}

	// 2. Unauthenticated POST /api/v1/config -> 401
	reqPost := httptest.NewRequest(http.MethodPost, "/api/v1/config", bytes.NewBufferString(`{"key":"test","limit":10,"window_ms":1000}`))
	recPost := httptest.NewRecorder()
	h.HandleSetConfig(recPost, reqPost)

	if recPost.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on unauthenticated POST, got %d", recPost.Code)
	}

	// 3. Incorrect Auth Header -> 401
	reqBadAuth := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	reqBadAuth.Header.Set("X-Admin-Key", "wrong-key")
	recBadAuth := httptest.NewRecorder()
	h.HandleGetConfig(recBadAuth, reqBadAuth)

	if recBadAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong X-Admin-Key, got %d", recBadAuth.Code)
	}
}

func TestHandler_PayloadSizeLimit_Enforcement(t *testing.T) {
	h := httpedge.NewHandler(nil, nil, nil)

	// Send 2MB payload to /api/v1/echo (limit is 1MB)
	largeMessage := strings.Repeat("A", 2*1024*1024)
	payload, _ := json.Marshal(map[string]string{"message": largeMessage})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	h.HandleEcho(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 StatusRequestEntityTooLarge on 2MB payload, got %d", rec.Code)
	}

	var errResp ratelimit.ErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Error.Code != "PAYLOAD_TOO_LARGE" {
		t.Errorf("expected PAYLOAD_TOO_LARGE, got %s", errResp.Error.Code)
	}
}
