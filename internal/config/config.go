package config

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"fluxgate/internal/redisclient"
)

var ErrDefaultConfigMissing = errors.New("default rate limit config missing in Redis (ratelimit:config:default)")

type RateLimitRule struct {
	Limit    int
	WindowMs int64
}

type Manager struct {
	client   *redisclient.Client
	cacheTTL time.Duration
	mu       sync.RWMutex
	cache    map[string]cachedRule
}

type cachedRule struct {
	rule      RateLimitRule
	expiresAt time.Time
}

func NewManager(client *redisclient.Client, cacheTTL time.Duration) *Manager {
	return &Manager{
		client:   client,
		cacheTTL: cacheTTL,
		cache:    make(map[string]cachedRule),
	}
}

// EnsureDefaultConfig validates that ratelimit:config:default exists at boot (D17)
func (m *Manager) EnsureDefaultConfig(ctx context.Context) error {
	defaultKey := "ratelimit:config:default"
	data, err := m.client.HGetAll(ctx, defaultKey)
	if err != nil {
		return fmt.Errorf("failed to read default config: %w", err)
	}
	if len(data) == 0 {
		return ErrDefaultConfigMissing
	}
	if _, ok := data["limit"]; !ok {
		return fmt.Errorf("%w: 'limit' field missing", ErrDefaultConfigMissing)
	}
	if _, ok := data["window_ms"]; !ok {
		return fmt.Errorf("%w: 'window_ms' field missing", ErrDefaultConfigMissing)
	}
	return nil
}

// GetRule resolves the rate limit for a key, checking local cache -> Redis override -> Redis default (D7)
func (m *Manager) GetRule(ctx context.Context, apiKey string) (RateLimitRule, error) {
	now := time.Now()

	m.mu.RLock()
	cached, found := m.cache[apiKey]
	m.mu.RUnlock()

	if found && now.Before(cached.expiresAt) {
		return cached.rule, nil
	}

	rule, err := m.fetchFromRedis(ctx, apiKey)
	if err != nil {
		return RateLimitRule{}, err
	}

	m.mu.Lock()
	m.cache[apiKey] = cachedRule{
		rule:      rule,
		expiresAt: now.Add(m.cacheTTL),
	}
	m.mu.Unlock()

	return rule, nil
}

func (m *Manager) fetchFromRedis(ctx context.Context, apiKey string) (RateLimitRule, error) {
	// 1. Try per-key override: ratelimit:config:{apiKey}
	key := fmt.Sprintf("ratelimit:config:%s", apiKey)
	rule, err := m.readRuleHash(ctx, key)
	if err == nil {
		return rule, nil
	}

	// 2. Fall back to global default: ratelimit:config:default
	defaultKey := "ratelimit:config:default"
	rule, err = m.readRuleHash(ctx, defaultKey)
	if err != nil {
		return RateLimitRule{}, fmt.Errorf("failed to fetch default config: %w", err)
	}

	return rule, nil
}

func (m *Manager) readRuleHash(ctx context.Context, key string) (RateLimitRule, error) {
	data, err := m.client.HGetAll(ctx, key)
	if err != nil {
		return RateLimitRule{}, err
	}
	if len(data) == 0 {
		return RateLimitRule{}, errors.New("config not found")
	}

	limitStr, hasLimit := data["limit"]
	windowStr, hasWindow := data["window_ms"]
	if !hasLimit || !hasWindow {
		return RateLimitRule{}, errors.New("incomplete config fields")
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		return RateLimitRule{}, fmt.Errorf("invalid limit value: %w", err)
	}

	windowMs, err := strconv.ParseInt(windowStr, 10, 64)
	if err != nil {
		return RateLimitRule{}, fmt.Errorf("invalid window_ms value: %w", err)
	}

	return RateLimitRule{Limit: limit, WindowMs: windowMs}, nil
}
