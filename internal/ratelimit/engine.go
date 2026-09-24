package ratelimit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"fluxgate/internal/redisclient"
)

type Outcome int

const (
	Allowed Outcome = iota
	Rejected
)

type Result struct {
	Outcome       Outcome
	CurrentCount  int
	OldestScoreMs int64
}

var ErrIndeterminate = errors.New("rate limiter: indeterminate (redis unavailable or failed)")

type Engine struct {
	client     *redisclient.Client
	scriptText string
	scriptSHA  string
	mu         sync.RWMutex
}

func NewEngine(client *redisclient.Client, scriptText string) (*Engine, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sha, err := client.ScriptLoad(ctx, scriptText)
	if err != nil {
		return nil, fmt.Errorf("failed to load sliding window script: %w", err)
	}

	return &Engine{
		client:     client,
		scriptText: scriptText,
		scriptSHA:  sha,
	}, nil
}

func (e *Engine) Evaluate(ctx context.Context, apiKey string, limit int, windowMs int64) (Result, error) {
	nowMs := time.Now().UnixMilli()
	ttlMs := windowMs * 2
	memberSuffix := generateEntropy()
	key := fmt.Sprintf("ratelimit:%s", apiKey)

	e.mu.RLock()
	sha := e.scriptSHA
	e.mu.RUnlock()

	res, err := e.client.EvalSha(ctx, sha, []string{key}, nowMs, windowMs, limit, memberSuffix, ttlMs)
	if err != nil {
		if redisclient.IsNoScript(err) {
			// Script cache evicted on Redis; reload script and retry once
			reloadErr := e.reloadScript(ctx)
			if reloadErr != nil {
				return Result{}, ErrIndeterminate
			}

			e.mu.RLock()
			sha = e.scriptSHA
			e.mu.RUnlock()

			res, err = e.client.EvalSha(ctx, sha, []string{key}, nowMs, windowMs, limit, memberSuffix, ttlMs)
			if err != nil {
				return Result{}, ErrIndeterminate
			}
		} else {
			return Result{}, ErrIndeterminate
		}
	}

	return parseScriptResult(res)
}

func (e *Engine) reloadScript(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	sha, err := e.client.ScriptLoad(ctx, e.scriptText)
	if err != nil {
		return err
	}
	e.scriptSHA = sha
	return nil
}

func parseScriptResult(res interface{}) (Result, error) {
	arr, ok := res.([]interface{})
	if !ok || len(arr) != 3 {
		return Result{}, fmt.Errorf("malformed script result: %#v", res)
	}

	allowedInt, ok1 := arr[0].(int64)
	countInt, ok2 := arr[1].(int64)
	oldestScoreInt, ok3 := arr[2].(int64)

	if !ok1 || !ok2 || !ok3 {
		return Result{}, fmt.Errorf("type assertion failed on script result: %#v", arr)
	}

	outcome := Rejected
	if allowedInt == 1 {
		outcome = Allowed
	}

	return Result{
		Outcome:       outcome,
		CurrentCount:  int(countInt),
		OldestScoreMs: oldestScoreInt,
	}, nil
}

func generateEntropy() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
