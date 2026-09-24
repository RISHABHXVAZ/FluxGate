package upstream

import (
	"context"
	"errors"
	"fmt"
	"time"

	pb "fluxgate/proto/gen"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var ErrUpstreamUnavailable = errors.New("upstream service failed or unavailable")

type Client struct {
	conn   *grpc.ClientConn
	client pb.DemoBackendClient
}

func NewClient(targetAddr string) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		targetAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to upstream gRPC backend at %s: %w", targetAddr, err)
	}

	return &Client{
		conn:   conn,
		client: pb.NewDemoBackendClient(conn),
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

type EchoParams struct {
	Message         string
	RequestID       string
	InjectLatencyMs int32
	InjectError     bool
	InjectErrorCode int32
}

type EchoResult struct {
	Message          string
	ServerTimestampMs int64
	RequestID        string
}

func (c *Client) Echo(ctx context.Context, params EchoParams) (EchoResult, error) {
	req := &pb.EchoRequest{
		Message:         params.Message,
		RequestId:       params.RequestID,
		InjectLatencyMs: params.InjectLatencyMs,
		InjectError:     params.InjectError,
		InjectErrorCode: params.InjectErrorCode,
	}

	resp, err := c.client.Echo(ctx, req)
	if err != nil {
		return EchoResult{}, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}

	return EchoResult{
		Message:          resp.GetMessage(),
		ServerTimestampMs: resp.GetServerTimestampMs(),
		RequestID:        resp.GetRequestId(),
	}, nil
}
