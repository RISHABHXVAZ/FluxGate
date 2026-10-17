package integration_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	pb "fluxgate/proto/gen"
	"fluxgate/internal/upstream"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mockBackend struct {
	pb.UnimplementedDemoBackendServer
}

func (m *mockBackend) Echo(ctx context.Context, req *pb.EchoRequest) (*pb.EchoResponse, error) {
	if req.GetInjectLatencyMs() > 0 {
		time.Sleep(time.Duration(req.GetInjectLatencyMs()) * time.Millisecond)
	}
	if req.GetInjectError() {
		code := codes.Code(req.GetInjectErrorCode())
		if code == codes.OK {
			code = codes.Unavailable
		}
		return nil, status.Errorf(code, "mock injected error")
	}
	return &pb.EchoResponse{
		Message:          req.GetMessage(),
		ServerTimestampMs: time.Now().UnixMilli(),
		RequestId:        req.GetRequestId(),
	}, nil
}

func TestUpstreamClient_EchoAndChaos(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	srv := grpc.NewServer()
	pb.RegisterDemoBackendServer(srv, &mockBackend{})
	go srv.Serve(lis)
	defer srv.Stop()

	client, err := upstream.NewClient(lis.Addr().String())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// 1. Normal Echo
	res, err := client.Echo(ctx, upstream.EchoParams{
		Message:   "hello fluxgate",
		RequestID: "req-1",
	})
	if err != nil {
		t.Fatalf("Echo failed: %v", err)
	}
	if res.Message != "hello fluxgate" || res.RequestID != "req-1" {
		t.Errorf("Unexpected echo response: %#v", res)
	}

	// 2. Injected Error Test
	_, err = client.Echo(ctx, upstream.EchoParams{
		Message:         "fail me",
		RequestID:       "req-2",
		InjectError:     true,
		InjectErrorCode: int32(codes.Unavailable),
	})
	if err == nil {
		t.Errorf("Expected upstream error, got nil")
	}
}

func TestUpstreamClient_TimeoutAndCancellation(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	srv := grpc.NewServer()
	pb.RegisterDemoBackendServer(srv, &mockBackend{})
	go srv.Serve(lis)
	defer srv.Stop()

	client, err := upstream.NewClient(lis.Addr().String())
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	// 1. Timeout Test
	t.Run("Context Timeout", func(t *testing.T) {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err := client.Echo(timeoutCtx, upstream.EchoParams{
			Message:         "slow",
			RequestID:       "req-timeout",
			InjectLatencyMs: 150, // 150ms > 50ms timeout
		})

		if err == nil {
			t.Fatalf("Expected timeout error, got nil")
		}
		if !errors.Is(err, upstream.ErrUpstreamTimeout) {
			t.Errorf("Expected ErrUpstreamTimeout, got: %v", err)
		}
		if errors.Is(err, upstream.ErrClientCanceled) {
			t.Errorf("Did not expect ErrClientCanceled on deadline exceeded")
		}
		if errors.Is(err, upstream.ErrUpstreamUnavailable) {
			t.Errorf("Did not expect ErrUpstreamUnavailable on deadline exceeded")
		}
	})

	// 2. Client Cancellation Test
	t.Run("Context Cancellation", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel context immediately

		_, err := client.Echo(cancelCtx, upstream.EchoParams{
			Message:   "cancel",
			RequestID: "req-cancel",
		})

		if err == nil {
			t.Fatalf("Expected cancellation error, got nil")
		}
		if !errors.Is(err, upstream.ErrClientCanceled) {
			t.Errorf("Expected ErrClientCanceled, got: %v", err)
		}
		if errors.Is(err, upstream.ErrUpstreamTimeout) {
			t.Errorf("Did not expect ErrUpstreamTimeout on explicit cancellation")
		}
	})
}
