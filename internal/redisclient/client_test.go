package redisclient_test

import (
	"context"
	"os"
	"testing"
	"time"

	"fluxgate/internal/redisclient"
)

func TestRedisClient_ScriptLoadAndEvalSha(t *testing.T) {
	cfg := redisclient.DefaultConfig()
	client, err := redisclient.NewClient(cfg)
	if err != nil {
		t.Skipf("Skipping integration test: Redis not reachable at %s: %v", cfg.Addr, err)
	}
	defer client.Close()

	ctx := context.Background()

	// 1. Read Lua script from filesystem
	scriptBytes, err := os.ReadFile("../../scripts/lua/sliding_window.lua")
	if err != nil {
		t.Fatalf("Failed to read Lua script: %v", err)
	}
	script := string(scriptBytes)

	// 2. Load script into Redis
	sha, err := client.ScriptLoad(ctx, script)
	if err != nil {
		t.Fatalf("ScriptLoad failed: %v", err)
	}
	if len(sha) != 40 {
		t.Fatalf("Expected 40-char SHA1 hash, got: %s", sha)
	}

	// 3. Execute EvalSha
	key := "ratelimit:test-key"
	nowMs := time.Now().UnixMilli()
	windowMs := int64(60000)
	limit := 5
	entropy := "test-suffix"
	ttlMs := windowMs * 2

	res, err := client.EvalSha(ctx, sha, []string{key}, nowMs, windowMs, limit, entropy, ttlMs)
	if err != nil {
		t.Fatalf("EvalSha failed: %v", err)
	}

	resSlice, ok := res.([]interface{})
	if !ok || len(resSlice) != 3 {
		t.Fatalf("Expected 3-element array return, got: %#v", res)
	}

	allowed, ok1 := resSlice[0].(int64)
	count, ok2 := resSlice[1].(int64)
	oldestScore, ok3 := resSlice[2].(int64)

	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("Failed to parse return tuple elements: %#v", resSlice)
	}

	if allowed != 1 {
		t.Errorf("Expected allowed=1, got %d", allowed)
	}
	if count != 1 {
		t.Errorf("Expected count=1, got %d", count)
	}
	if oldestScore != nowMs {
		t.Errorf("Expected oldestScore=%d, got %d", nowMs, oldestScore)
	}
}
