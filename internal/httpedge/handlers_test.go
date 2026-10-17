package httpedge_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"fluxgate/internal/httpedge"
	"fluxgate/internal/ratelimit"
	"fluxgate/internal/upstream"
	pb "fluxgate/proto/gen"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mockBackendServer struct {
	pb.UnimplementedDemoBackendServer
}

func (s *mockBackendServer) Echo(ctx context.Context, req *pb.EchoRequest) (*pb.EchoResponse, error) {
	if req.GetInjectLatencyMs() > 0 {
		time.Sleep(time.Duration(req.GetInjectLatencyMs()) * time.Millisecond)
	}
	if req.GetInjectError() {
		code := codes.Code(req.GetInjectErrorCode())
		if code == codes.OK {
			code = codes.Unavailable
		}
		return nil, status.Errorf(code, "synthetic injected error")
	}
	return &pb.EchoResponse{
		Message:          req.GetMessage(),
		ServerTimestampMs: time.Now().UnixMilli(),
		RequestId:        req.GetRequestId(),
	}, nil
}

func setupTestHandler(t *testing.T, timeout time.Duration) (*httpedge.Handler, func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on local tcp: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterDemoBackendServer(grpcServer, &mockBackendServer{})
	go grpcServer.Serve(lis)

	upClient, err := upstream.NewClient(lis.Addr().String())
	if err != nil {
		grpcServer.Stop()
		lis.Close()
		t.Fatalf("failed to create upstream client: %v", err)
	}

	cleanup := func() {
		upClient.Close()
		grpcServer.Stop()
		lis.Close()
	}

	handler := httpedge.NewHandler(upClient, nil, nil, timeout)
	return handler, cleanup
}

func TestHandler_HandleEcho_Timeout_Deterministic(t *testing.T) {
	timeout := 50 * time.Millisecond
	handler, cleanup := setupTestHandler(t, timeout)
	defer cleanup()

	body, _ := json.Marshal(httpedge.EchoRequestBody{
		Message:         "timeout-check",
		InjectLatencyMs: 150, // 150ms > 50ms timeout
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.HandleEcho(rec, req)

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
}

func TestHandler_HandleEcho_OrdinaryError_Deterministic(t *testing.T) {
	handler, cleanup := setupTestHandler(t, 2*time.Second)
	defer cleanup()

	body, _ := json.Marshal(httpedge.EchoRequestBody{
		Message:     "error-check",
		InjectError: true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.HandleEcho(rec, req)

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
}

func TestHandler_HandleEcho_ClientCancellation_Deterministic(t *testing.T) {
	handler, cleanup := setupTestHandler(t, 2*time.Second)
	defer cleanup()

	clientCtx, cancel := context.WithCancel(context.Background())
	cancel() // Simulate client closing connection

	body, _ := json.Marshal(httpedge.EchoRequestBody{
		Message: "cancel-check",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(body)).WithContext(clientCtx)
	rec := httptest.NewRecorder()

	handler.HandleEcho(rec, req)

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
}

func TestHandler_HandleEcho_Success_Deterministic(t *testing.T) {
	handler, cleanup := setupTestHandler(t, 2*time.Second)
	defer cleanup()

	body, _ := json.Marshal(httpedge.EchoRequestBody{
		Message: "success-check",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/echo", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.HandleEcho(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected HTTP 200 OK, got %d", rec.Code)
	}

	var resp httpedge.EchoResponseEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response JSON: %v", err)
	}
	if resp.Data.Message != "success-check" {
		t.Errorf("Expected message 'success-check', got %q", resp.Data.Message)
	}
}
