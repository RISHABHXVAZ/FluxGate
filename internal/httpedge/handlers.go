package httpedge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"fluxgate/internal/config"
	"fluxgate/internal/ratelimit"
	"fluxgate/internal/upstream"
)

type Handler struct {
	upstreamClient *upstream.Client
	pingRedis      func(ctx context.Context) error
	cfgMgr         *config.Manager
}

func NewHandler(upstreamClient *upstream.Client, pingRedis func(ctx context.Context) error, cfgMgr *config.Manager) *Handler {
	return &Handler{
		upstreamClient: upstreamClient,
		pingRedis:      pingRedis,
		cfgMgr:         cfgMgr,
	}
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

	var reqBody EchoRequestBody
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		w.Header().Set("Content-Type", "application/json")
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

