package ratelimit_test

import (
	"context"
	"os"
	"testing"
	"time"

	"fluxgate/internal/ratelimit"
	"fluxgate/internal/redisclient"
)

func TestEngine_SlidingWindowBoundary(t *testing.T) {
	cfg := redisclient.DefaultConfig()
	client, err := redisclient.NewClient(cfg)
	if err != nil {
		t.Skipf("Skipping integration test: Redis not reachable: %v", err)
	}
	defer client.Close()

	scriptBytes, err := os.ReadFile("../../scripts/lua/sliding_window.lua")
	if err != nil {
		t.Fatalf("Failed to read Lua script: %v", err)
	}

	engine, err := ratelimit.NewEngine(client, string(scriptBytes))
	if err != nil {
		t.Fatalf("Failed to instantiate engine: %v", err)
	}

	ctx := context.Background()
	apiKey := "test-boundary-key"
	limit := 3
	windowMs := int64(1000) // 1-second rolling window

	// Requests 1 to 3 should be Allowed (D2)
	for i := 1; i <= 3; i++ {
		res, err := engine.Evaluate(ctx, apiKey, limit, windowMs)
		if err != nil {
			t.Fatalf("Evaluate error on request %d: %v", i, err)
		}
		if res.Outcome != ratelimit.Allowed {
			t.Errorf("Request %d expected Allowed, got Rejected", i)
		}
		if res.CurrentCount != i {
			t.Errorf("Request %d expected count %d, got %d", i, i, res.CurrentCount)
		}
	}

	// Request 4 (Limit reached) should be Rejected (D2)
	res, err := engine.Evaluate(ctx, apiKey, limit, windowMs)
	if err != nil {
		t.Fatalf("Evaluate error on request 4: %v", err)
	}
	if res.Outcome != ratelimit.Rejected {
		t.Errorf("Request 4 expected Rejected, got Allowed")
	}
	if res.CurrentCount != 3 {
		t.Errorf("Request 4 rejected count should remain 3, got %d", res.CurrentCount)
	}

	// Wait for window to slide out (> 1000ms)
	time.Sleep(1100 * time.Millisecond)

	// Next request after window expiration should be Allowed
	res, err = engine.Evaluate(ctx, apiKey, limit, windowMs)
	if err != nil {
		t.Fatalf("Evaluate error after window slide: %v", err)
	}
	if res.Outcome != ratelimit.Allowed {
		t.Errorf("Expected request after window expiry to be Allowed, got Rejected")
	}
	if res.CurrentCount != 1 {
		t.Errorf("Expected reset count 1, got %d", res.CurrentCount)
	}
}
