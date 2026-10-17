package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testBackend struct {
	pb.UnimplementedDemoBackendServer
}

func (b *testBackend) Echo(ctx context.Context, req *pb.EchoRequest) (*pb.EchoResponse, error) {
	if req.GetInjectLatencyMs() > 0 {
		time.Sleep(time.Duration(req.GetInjectLatencyMs()) * time.Millisecond)
	}
	if req.GetInjectError() {
		code := codes.Code(req.GetInjectErrorCode())
		if code == codes.OK {
			code = codes.Unavailable
		}
		return nil, status.Errorf(code, "test backend injected error")
	}
	return &pb.EchoResponse{
		Message:          req.GetMessage(),
		ServerTimestampMs: time.Now().UnixMilli(),
		RequestId:        req.GetRequestId(),
	}, nil
}

func TestGateway_CompleteHTTPFlow(t *testing.T) {
	// 1. Setup Redis
	rCfg := redisclient.DefaultConfig()
	rClient, err := redisclient.NewClient(rCfg)
	if err != nil {
		t.Skipf("Redis unreachable: %v", err)
	}
	defer rClient.Close()

	ctx := context.Background()
	_ = rClient.HSet(ctx, "ratelimit:config:default", "limit", "2", "window_ms", "60000")

	// 2. Setup mock upstream backend
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	srv := grpc.NewServer()
	pb.RegisterDemoBackendServer(srv, &testBackend{})
	go srv.Serve(lis)
	defer srv.Stop()

	upClient, err := upstream.NewClient(lis.Addr().String())
	if err != nil {
		t.Fatalf("upstream client failed: %v", err)
	}
	defer upClient.Close()

	// 3. Setup Gateway Pipeline
	scriptBytes, err := os.ReadFile("../../scripts/lua/sliding_window.lua")
	if err != nil {
		t.Fatalf("read script failed: %v", err)
	}

	engine, err := ratelimit.NewEngine(rClient, string(scriptBytes))
	if err != nil {
		t.Fatalf("engine failed: %v", err)
	}

	cfgMgr := config.NewManager(rClient, 5*time.Second)
	m := metrics.NewMetrics(prometheus.NewRegistry())
	rlMw := ratelimit.NewMiddleware(engine, cfgMgr, m)
	h := httpedge.NewHandler(upClient, rClient.Ping, cfgMgr, 2*time.Second)
	router := httpedge.NewRouter(h, rlMw)

	// Test 1: Missing API key -> 401 Unauthorized (D14)
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewBufferString(`{"message":"hi"}`))
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 on missing API key, got %d", rec1.Code)
	}

	// Test 2: Valid Request 1 -> 200 OK
	apiKey := "test-client-http"
	bodyBytes, _ := json.Marshal(httpedge.EchoRequestBody{Message: "hello"})
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(bodyBytes))
	req2.Header.Set("X-API-Key", apiKey)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("Expected 200 on request 1, got %d", rec2.Code)
	}
	if rec2.Header().Get("X-RateLimit-Remaining") != "1" {
		t.Errorf("Expected remaining 1, got %s", rec2.Header().Get("X-RateLimit-Remaining"))
	}

	// Test 3: Valid Request 2 -> 200 OK
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(bodyBytes))
	req3.Header.Set("X-API-Key", apiKey)
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Errorf("Expected 200 on request 2, got %d", rec3.Code)
	}

	// Test 4: Request 3 (Exceeds limit of 2) -> 429 Too Many Requests (D2)
	req4 := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(bodyBytes))
	req4.Header.Set("X-API-Key", apiKey)
	rec4 := httptest.NewRecorder()
	router.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusTooManyRequests {
		t.Errorf("Expected 429 on request 3, got %d", rec4.Code)
	}
	if rec4.Header().Get("Retry-After") == "" {
		t.Errorf("Expected Retry-After header on 429 response")
	}

	// Test 5: Health & Ready Probes
	hReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	hRec := httptest.NewRecorder()
	router.ServeHTTP(hRec, hReq)
	if hRec.Code != http.StatusOK {
		t.Errorf("Expected 200 for /health, got %d", hRec.Code)
	}
}

func TestGateway_UpstreamTimeoutAndErrors(t *testing.T) {
	// 1. Setup Redis
	rCfg := redisclient.DefaultConfig()
	rClient, err := redisclient.NewClient(rCfg)
	if err != nil {
		t.Skipf("Redis unreachable: %v", err)
	}
	defer rClient.Close()

	ctx := context.Background()
	_ = rClient.HSet(ctx, "ratelimit:config:default", "limit", "100", "window_ms", "60000")

	// 2. Setup mock upstream backend
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	srv := grpc.NewServer()
	pb.RegisterDemoBackendServer(srv, &testBackend{})
	go srv.Serve(lis)
	defer srv.Stop()

	upClient, err := upstream.NewClient(lis.Addr().String())
	if err != nil {
		t.Fatalf("upstream client failed: %v", err)
	}
	defer upClient.Close()

	// 3. Setup Gateway Pipeline with a tight 50ms upstream timeout
	scriptBytes, err := os.ReadFile("../../scripts/lua/sliding_window.lua")
	if err != nil {
		t.Fatalf("read script failed: %v", err)
	}

	engine, err := ratelimit.NewEngine(rClient, string(scriptBytes))
	if err != nil {
		t.Fatalf("engine failed: %v", err)
	}

	cfgMgr := config.NewManager(rClient, 5*time.Second)
	m := metrics.NewMetrics(prometheus.NewRegistry())
	rlMw := ratelimit.NewMiddleware(engine, cfgMgr, m)

	shortTimeout := 50 * time.Millisecond
	h := httpedge.NewHandler(upClient, rClient.Ping, cfgMgr, shortTimeout)
	router := httpedge.NewRouter(h, rlMw)

	apiKey := "test-timeout-key"

	// Subtest A: Upstream Deadline Exceeded -> HTTP 504 & UPSTREAM_TIMEOUT
	t.Run("Upstream Deadline Exceeded (504)", func(t *testing.T) {
		body, _ := json.Marshal(httpedge.EchoRequestBody{
			Message:         "timeout-test",
			InjectLatencyMs: 150, // 150ms > 50ms timeout
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(body))
		req.Header.Set("X-API-Key", apiKey)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusGatewayTimeout {
			t.Fatalf("Expected HTTP 504 Gateway Timeout, got %d. Body: %s", rec.Code, rec.Body.String())
		}

		var env ratelimit.ErrorEnvelope
		if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
			t.Fatalf("Failed to decode response JSON: %v", err)
		}
		if env.Error.Code != "UPSTREAM_TIMEOUT" {
			t.Errorf("Expected error code UPSTREAM_TIMEOUT, got %q", env.Error.Code)
		}
		if env.Error.Message != "The upstream service timed out." {
			t.Errorf("Unexpected error message: %q", env.Error.Message)
		}
	})

	// Subtest B: Ordinary Upstream Failure -> HTTP 502 & UPSTREAM_UNAVAILABLE
	t.Run("Ordinary Upstream Failure (502)", func(t *testing.T) {
		body, _ := json.Marshal(httpedge.EchoRequestBody{
			Message:     "failure-test",
			InjectError: true,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(body))
		req.Header.Set("X-API-Key", apiKey)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadGateway {
			t.Fatalf("Expected HTTP 502 Bad Gateway, got %d. Body: %s", rec.Code, rec.Body.String())
		}

		var env ratelimit.ErrorEnvelope
		if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
			t.Fatalf("Failed to decode response JSON: %v", err)
		}
		if env.Error.Code != "UPSTREAM_UNAVAILABLE" {
			t.Errorf("Expected error code UPSTREAM_UNAVAILABLE, got %q", env.Error.Code)
		}
		if env.Error.Message != "The upstream service failed to respond." {
			t.Errorf("Unexpected error message: %q", env.Error.Message)
		}
	})

	// Subtest C: Client-Request Cancellation -> HTTP 499 & Not Misreported as 504
	t.Run("Client Request Cancellation (499)", func(t *testing.T) {
		clientCtx, cancel := context.WithCancel(context.Background())
		cancel() // Client canceled request immediately

		body, _ := json.Marshal(httpedge.EchoRequestBody{
			Message: "cancel-test",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(body)).WithContext(clientCtx)
		req.Header.Set("X-API-Key", apiKey)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code == http.StatusGatewayTimeout {
			t.Errorf("Client cancellation must NOT be misreported as HTTP 504 Gateway Timeout")
		}
		if rec.Code != 499 {
			t.Errorf("Expected HTTP 499 Client Closed Request, got %d", rec.Code)
		}

		var env ratelimit.ErrorEnvelope
		if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
			t.Fatalf("Failed to decode response JSON: %v", err)
		}
		if env.Error.Code != "CLIENT_CLOSED_REQUEST" {
			t.Errorf("Expected error code CLIENT_CLOSED_REQUEST, got %q", env.Error.Code)
		}
	})
}
