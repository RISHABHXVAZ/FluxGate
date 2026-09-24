package httpedge

import (
	"net/http"

	"fluxgate/internal/ratelimit"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewRouter(h *Handler, rlMiddleware *ratelimit.RateLimitMiddleware) *http.ServeMux {
	mux := http.NewServeMux()

	// Observability & Probe endpoints (unprotected)
	mux.HandleFunc("/health", h.HealthHandler)
	mux.HandleFunc("/ready", h.ReadyHandler)
	mux.Handle("/metrics", promhttp.Handler())

	// Business API routes (protected by rate limiter)
	echoHandler := http.HandlerFunc(h.HandleEcho)
	mux.Handle("/api/v1/echo", rlMiddleware.Wrap(echoHandler))

	return mux
}
