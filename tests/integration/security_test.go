package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"fluxgate/internal/config"
	"fluxgate/internal/httpedge"
	"fluxgate/internal/metrics"
	"fluxgate/internal/ratelimit"
	"fluxgate/internal/redisclient"
	"fluxgate/internal/upstream"
	pb "fluxgate/proto/gen"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
)

// setupSecurityTestPipeline initializes a test harness for security audit tests.
// Authored by QA / Tester: Rishabh Singh (Reg: 20243235)
func setupSecurityTestPipeline(t *testing.T) (http.Handler, *httpedge.Handler, func()) {
	rCfg := redisclient.DefaultConfig()
	rClient, err := redisclient.NewClient(rCfg)
	if err != nil {
		t.Skipf("Redis unreachable for security test: %v", err)
	}

	ctx := context.Background()
	_ = rClient.HSet(ctx, "ratelimit:config:default", "limit", "10", "window_ms", "60000")

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on test port: %v", err)
	}

	srv := grpc.NewServer()
	pb.RegisterDemoBackendServer(srv, &testBackend{})
	go srv.Serve(lis)

	upClient, err := upstream.NewClient(lis.Addr().String())
	if err != nil {
		t.Fatalf("failed to create upstream client: %v", err)
	}

	scriptBytes, err := os.ReadFile("../../scripts/lua/sliding_window.lua")
	if err != nil {
		t.Fatalf("read sliding_window.lua failed: %v", err)
	}

	engine, err := ratelimit.NewEngine(rClient, string(scriptBytes))
	if err != nil {
		t.Fatalf("failed to initialize rate limit engine: %v", err)
	}

	cfgMgr := config.NewManager(rClient, 5*time.Second)
	m := metrics.NewMetrics(prometheus.NewRegistry())
	rlMw := ratelimit.NewMiddleware(engine, cfgMgr, m)
	h := httpedge.NewHandler(upClient, rClient.Ping, cfgMgr)
	h.SetAdminKey("test-admin-secret-qa")
	router := httpedge.NewRouter(h, rlMw)

	cleanup := func() {
		_ = upClient.Close()
		srv.Stop()
		_ = lis.Close()
		_ = rClient.Close()
	}

	return router, h, cleanup
}

// SEC-01: Admin Endpoints Must Require Authentication
func TestSecurity_AdminEndpointRequiresAuth(t *testing.T) {
	router, _, cleanup := setupSecurityTestPipeline(t)
	defer cleanup()

	// 1. Unauthenticated GET /api/v1/config -> 401 Unauthorized
	reqGetNoAuth := httptest.NewRequest(http.MethodGet, "/api/v1/config?key=default", nil)
	recGetNoAuth := httptest.NewRecorder()
	router.ServeHTTP(recGetNoAuth, reqGetNoAuth)

	if recGetNoAuth.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 on unauthenticated GET config, got %d", recGetNoAuth.Code)
	}

	// 2. Unauthenticated POST /api/v1/config -> 401 Unauthorized
	body := bytes.NewBufferString(`{"key":"client-hack","limit":99999,"window_ms":1000}`)
	reqPostNoAuth := httptest.NewRequest(http.MethodPost, "/api/v1/config", body)
	recPostNoAuth := httptest.NewRecorder()
	router.ServeHTTP(recPostNoAuth, reqPostNoAuth)

	if recPostNoAuth.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 on unauthenticated POST config, got %d", recPostNoAuth.Code)
	}

	// 3. Invalid Token -> 401 Unauthorized
	reqInvalidToken := httptest.NewRequest(http.MethodGet, "/api/v1/config?key=default", nil)
	reqInvalidToken.Header.Set("X-Admin-Key", "wrong-secret-token")
	recInvalidToken := httptest.NewRecorder()
	router.ServeHTTP(recInvalidToken, reqInvalidToken)

	if recInvalidToken.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 with wrong X-Admin-Key, got %d", recInvalidToken.Code)
	}

	// 4. Valid X-Admin-Key -> 200 OK
	reqValidHeader := httptest.NewRequest(http.MethodPost, "/api/v1/config", bytes.NewBufferString(`{"key":"client-qa","limit":50,"window_ms":60000}`))
	reqValidHeader.Header.Set("X-Admin-Key", "test-admin-secret-qa")
	recValidHeader := httptest.NewRecorder()
	router.ServeHTTP(recValidHeader, reqValidHeader)

	if recValidHeader.Code != http.StatusOK {
		t.Errorf("Expected 200 with valid X-Admin-Key, got %d (%s)", recValidHeader.Code, recValidHeader.Body.String())
	}

	// 5. Valid Authorization: Bearer <token> -> 200 OK
	reqValidBearer := httptest.NewRequest(http.MethodGet, "/api/v1/config?key=client-qa", nil)
	reqValidBearer.Header.Set("Authorization", "Bearer test-admin-secret-qa")
	recValidBearer := httptest.NewRecorder()
	router.ServeHTTP(recValidBearer, reqValidBearer)

	if recValidBearer.Code != http.StatusOK {
		t.Errorf("Expected 200 with valid Bearer token, got %d", recValidBearer.Code)
	}
}

// SEC-02: Request Payload Must Be Bounded To Prevent Memory Exhaustion DoS
func TestSecurity_PayloadSizeLimit(t *testing.T) {
	router, _, cleanup := setupSecurityTestPipeline(t)
	defer cleanup()

	// 1. Construct 2MB oversized payload (Limit is 1MB)
	largeMessage := strings.Repeat("A", 2*1024*1024)
	payload, _ := json.Marshal(map[string]string{"message": largeMessage})

	reqOversize := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(payload))
	reqOversize.Header.Set("X-API-Key", "test-client-qa")
	recOversize := httptest.NewRecorder()
	router.ServeHTTP(recOversize, reqOversize)

	if recOversize.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Expected 413 Payload Too Large on 2MB request, got %d", recOversize.Code)
	}

	// 2. Normal payload should succeed
	normalPayload, _ := json.Marshal(map[string]string{"message": "Safe small payload"})
	reqNormal := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(normalPayload))
	reqNormal.Header.Set("X-API-Key", "test-client-qa")
	recNormal := httptest.NewRecorder()
	router.ServeHTTP(recNormal, reqNormal)

	if recNormal.Code != http.StatusOK {
		t.Errorf("Expected 200 for normal payload, got %d", recNormal.Code)
	}
}

// SEC-03: API Key Input Validation & Fuzzing Protection
func TestSecurity_APIKeyValidation(t *testing.T) {
	router, _, cleanup := setupSecurityTestPipeline(t)
	defer cleanup()

	fuzzTests := []struct {
		name       string
		apiKey     string
		expectCode int
	}{
		{"excessive_length_key", strings.Repeat("x", 200), http.StatusBadRequest},
		{"sql_injection_attempt", "client'; DROP TABLE users;--", http.StatusBadRequest},
		{"null_byte_injection", "client\x00malicious", http.StatusBadRequest},
		{"shell_metacharacters", "client`whoami`", http.StatusBadRequest},
		{"valid_standard_key", "client-alpha-qa.1", http.StatusOK},
	}

	for _, tt := range fuzzTests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewBufferString(`{"message":"test"}`))
			req.Header.Set("X-API-Key", tt.apiKey)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tt.expectCode {
				t.Errorf("Case %s: expected status %d, got %d (%s)", tt.name, tt.expectCode, rec.Code, rec.Body.String())
			}
		})
	}
}
