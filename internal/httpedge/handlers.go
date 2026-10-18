package httpedge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"fluxgate/internal/config"
	"fluxgate/internal/ratelimit"
	"fluxgate/internal/upstream"
)

const (
	MaxEchoBodyBytes   = 1 << 20  // 1MB request payload limit to prevent DoS
	MaxConfigBodyBytes = 64 << 10 // 64KB config payload limit
)

type Handler struct {
	upstreamClient *upstream.Client
	pingRedis      func(ctx context.Context) error
	cfgMgr         *config.Manager
	adminKey       string
}

func NewHandler(upstreamClient *upstream.Client, pingRedis func(ctx context.Context) error, cfgMgr *config.Manager) *Handler {
	adminKey := os.Getenv("ADMIN_API_KEY")
	if adminKey == "" {
		adminKey = "fluxgate-admin-secret"
	}

	return &Handler{
		upstreamClient: upstreamClient,
		pingRedis:      pingRedis,
		cfgMgr:         cfgMgr,
		adminKey:       adminKey,
	}
}

func (h *Handler) SetAdminKey(key string) {
	h.adminKey = key
}

func (h *Handler) verifyAdminAuth(r *http.Request) bool {
	if h.adminKey == "" {
		return true // Auth check bypassed if admin key is explicitly disabled
	}

	// 1. Check X-Admin-Key header
	if r.Header.Get("X-Admin-Key") == h.adminKey {
		return true
	}

	// 2. Check Authorization: Bearer <token>
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		if token == h.adminKey {
			return true
		}
	}

	return false
}

type EchoRequestBody struct {
	Message         string `json:"message"`
	InjectLatencyMs int32  `json:"inject_latency_ms,omitempty"`
	InjectError     bool   `json:"inject_error,omitempty"`
	InjectErrorCode int32  `json:"inject_error_code,omitempty"`
}

type EchoResponseEnvelope struct {
	Data EchoData `json:"data"`
}

type EchoData struct {
	Message           string `json:"message"`
	ServerTimestampMs int64  `json:"server_timestamp_ms"`
}

func (h *Handler) HandleEcho(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body size to 1MB to prevent memory exhaustion DoS
	r.Body = http.MaxBytesReader(w, r.Body, MaxEchoBodyBytes)

	var reqBody EchoRequestBody
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		w.Header().Set("Content-Type", "application/json")

		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_ = json.NewEncoder(w).Encode(ratelimit.ErrorEnvelope{
				Error: ratelimit.ErrorBody{
					Code:    "PAYLOAD_TOO_LARGE",
					Message: "Request payload exceeds maximum allowed size of 1MB",
				},
			})
			return
		}

		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ratelimit.ErrorEnvelope{
			Error: ratelimit.ErrorBody{
				Code:    "INVALID_REQUEST_PAYLOAD",
				Message: "Request body must be valid JSON",
			},
		})
		return
	}

	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = fmt.Sprintf("req-%d", time.Now().UnixNano())
	}

	// Dispatch to upstream backend via gRPC (D6)
	res, err := h.upstreamClient.Echo(r.Context(), upstream.EchoParams{
		Message:         reqBody.Message,
		RequestID:       requestID,
		InjectLatencyMs: reqBody.InjectLatencyMs,
		InjectError:     reqBody.InjectError,
		InjectErrorCode: reqBody.InjectErrorCode,
	})
	if err != nil {
		// Backend unavailable or error returned -> 502 Bad Gateway (D16)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(ratelimit.ErrorEnvelope{
			Error: ratelimit.ErrorBody{
				Code:    "UPSTREAM_UNAVAILABLE",
				Message: "The upstream service failed to respond.",
			},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(EchoResponseEnvelope{
		Data: EchoData{
			Message:           res.Message,
			ServerTimestampMs: res.ServerTimestampMs,
		},
	})
}

// HealthHandler: Gateway process liveness check (does NOT touch Redis or Backend)
func (h *Handler) HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"UP"}`))
}

// ReadyHandler: Gateway readiness check (confirms Redis connectivity) (D17)
func (h *Handler) ReadyHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
	defer cancel()

	if err := h.pingRedis(ctx); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"DOWN","reason":"redis unreachable"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"READY"}`))
}

type SetConfigRequest struct {
	Key      string `json:"key"`
	Limit    int    `json:"limit"`
	WindowMs int64  `json:"window_ms"`
}

// HandleGetConfig handles GET /api/v1/config?key=...
func (h *Handler) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.verifyAdminAuth(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(ratelimit.ErrorEnvelope{
			Error: ratelimit.ErrorBody{
				Code:    "ADMIN_AUTH_REQUIRED",
				Message: "Admin authentication required. Provide valid X-Admin-Key header or Bearer token.",
			},
		})
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		key = "default"
	}

	rule, err := h.cfgMgr.GetRule(r.Context(), key)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"key":       key,
		"limit":     rule.Limit,
		"window_ms": rule.WindowMs,
	})
}

// HandleSetConfig handles POST /api/v1/config
func (h *Handler) HandleSetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.verifyAdminAuth(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(ratelimit.ErrorEnvelope{
			Error: ratelimit.ErrorBody{
				Code:    "ADMIN_AUTH_REQUIRED",
				Message: "Admin authentication required. Provide valid X-Admin-Key header or Bearer token.",
			},
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxConfigBodyBytes)

	var req SetConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON payload"})
		return
	}

	if req.Limit <= 0 || req.WindowMs <= 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "limit and window_ms must be positive"})
		return
	}

	if req.Key == "" {
		req.Key = "default"
	}

	if err := h.cfgMgr.SetRule(r.Context(), req.Key, req.Limit, req.WindowMs); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "updated",
		"key":       req.Key,
		"limit":     req.Limit,
		"window_ms": req.WindowMs,
	})
}

