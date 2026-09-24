package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "fluxgate/proto/gen"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type server struct {
	pb.UnimplementedDemoBackendServer
}

func (s *server) Echo(ctx context.Context, req *pb.EchoRequest) (*pb.EchoResponse, error) {
	// 1. Synthetic latency injection (D15)
	if req.GetInjectLatencyMs() > 0 {
		time.Sleep(time.Duration(req.GetInjectLatencyMs()) * time.Millisecond)
	}

	// 2. Synthetic error injection (D15)
	if req.GetInjectError() {
		code := codes.Code(req.GetInjectErrorCode())
		if code == codes.OK {
			code = codes.Unavailable // Default to UNAVAILABLE (14) if unset
		}
		return nil, status.Errorf(code, "synthetic injected error from backend for request_id: %s", req.GetRequestId())
	}

	return &pb.EchoResponse{
		Message:          req.GetMessage(),
		ServerTimestampMs: time.Now().UnixMilli(),
		RequestId:        req.GetRequestId(),
	}, nil
}

func main() {
	port := os.Getenv("BACKEND_PORT")
	if port == "" {
		port = "50051"
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen on port %s: %v", port, err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterDemoBackendServer(grpcServer, &server{})

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("DemoBackend gRPC server listening on :%s", port)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down backend gRPC server...")
	grpcServer.GracefulStop()
	log.Println("Backend server stopped.")
}
