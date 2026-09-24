package ratelimit

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"

	"fluxgate/internal/apikey"
	"fluxgate/internal/config"
	"fluxgate/internal/metrics"
)

type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

type RateLimitMiddleware struct {
	engine  *Engine
	cfgMgr  *config.Manager
	metrics *metrics.Metrics
}

func NewMiddleware(engine *Engine, cfgMgr *config.Manager, m *metrics.Metrics) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		engine:  engine,
		cfgMgr:  cfgMgr,
		metrics: m,
	}
}

type contextKey string

const (
	ContextKeyAPIKey contextKey = "apiKey"
)

func (mw *RateLimitMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// 1. Identity extraction (D1, D14)
		key, err := apikey.Extract(r)
		if err != nil {
			mw.metrics.RequestsTotal.WithLabelValues("rejected_missing_key", "anonymous").Inc()
			mw.metrics.RequestDuration.WithLabelValues("rejected_missing_key").Observe(time.Since(start).Seconds())

			writeJSONError(w, http.StatusUnauthorized, "API_KEY_REQUIRED", "API key required", 0)
			return
		}

		ctx := r.Context()

		// 2. Resolve configured dynamic limit for this key (D7)
		rule, err := mw.cfgMgr.GetRule(ctx, key)
		if err != nil {
			// Failed to read Redis config -> fail-closed (D3, D12)
			mw.metrics.RequestsTotal.WithLabelValues("rejected_redis_error", key).Inc()
			mw.metrics.RequestDuration.WithLabelValues("rejected_redis_error").Observe(time.Since(start).Seconds())

			writeJSONError(w, http.StatusServiceUnavailable, "RATE_LIMIT_SERVICE_UNAVAILABLE", "Unable to verify rate limit at this time. Please retry shortly.", 0)
			return
		}

		// 3. Evaluate atomic sliding window in Redis
		redisStart := time.Now()
		result, err := mw.engine.Evaluate(ctx, key, rule.Limit, rule.WindowMs)
		redisDuration := time.Since(redisStart).Seconds()

		if err != nil {
			// Redis failure/timeout -> fail-closed with 503 (D3, D12)
			mw.metrics.RedisCallDuration.WithLabelValues("error").Observe(redisDuration)
			mw.metrics.RequestsTotal.WithLabelValues("rejected_redis_error", key).Inc()
			mw.metrics.RequestDuration.WithLabelValues("rejected_redis_error").Observe(time.Since(start).Seconds())

			writeJSONError(w, http.StatusServiceUnavailable, "RATE_LIMIT_SERVICE_UNAVAILABLE", "Unable to verify rate limit at this time. Please retry shortly.", 0)
			return
		}

		mw.metrics.RedisCallDuration.WithLabelValues("success").Observe(redisDuration)

		// 4. Calculate Rate Limit Headers (D18, D19)
		remaining := rule.Limit - result.CurrentCount
		if remaining < 0 {
			remaining = 0
		}

		now := time.Now()
		var resetEpochSeconds int64
		if result.OldestScoreMs <= 0 {
			// Empty set or limit=0 edge case (D19)
			resetEpochSeconds = now.Unix()
		} else {
			resetEpochSeconds = int64(math.Ceil(float64(result.OldestScoreMs+rule.WindowMs) / 1000.0))
		}

		retryAfterSeconds := int(resetEpochSeconds - now.Unix())
		if retryAfterSeconds < 1 {
			retryAfterSeconds = 1
		}

		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rule.Limit))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetEpochSeconds, 10))

		// 5. Decision Branch
		if result.Outcome == Rejected {
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))

			mw.metrics.RequestsTotal.WithLabelValues("rejected_limit", key).Inc()
			mw.metrics.RequestDuration.WithLabelValues("rejected_limit").Observe(time.Since(start).Seconds())

			writeJSONError(w, http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", "Rate limit exceeded. Please slow down.", retryAfterSeconds)
			return
		}

		// Allowed Path
		mw.metrics.RequestsTotal.WithLabelValues("allowed", key).Inc()
		ctxWithKey := context.WithValue(ctx, ContextKeyAPIKey, key)
		next.ServeHTTP(w, r.WithContext(ctxWithKey))
		mw.metrics.RequestDuration.WithLabelValues("allowed").Observe(time.Since(start).Seconds())
	})
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string, retryAfter int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorEnvelope{
		Error: ErrorBody{
			Code:              code,
			Message:           msg,
			RetryAfterSeconds: retryAfter,
		},
	})
}
