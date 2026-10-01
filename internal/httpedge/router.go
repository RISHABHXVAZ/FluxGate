package httpedge

import (
	"net/http"

	"fluxgate/internal/ratelimit"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewRouter(h *Handler, rlMiddleware *ratelimit.RateLimitMiddleware) http.Handler {
	mux := http.NewServeMux()

	// Observability & Probe endpoints (unprotected)
	mux.HandleFunc("/health", h.HealthHandler)
	mux.HandleFunc("/ready", h.ReadyHandler)
	mux.Handle("/metrics", promhttp.Handler())

	// Dynamic config management endpoints
	mux.HandleFunc("/api/v1/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			h.HandleGetConfig(w, r)
		case http.MethodPost:
			h.HandleSetConfig(w, r)
		default:
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
	})

	// Business API routes (protected by rate limiter)
	echoHandler := http.HandlerFunc(h.HandleEcho)
	mux.Handle("/api/v1/echo", rlMiddleware.Wrap(echoHandler))

	return corsMiddleware(mux)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, X-Request-ID")
		w.Header().Set("Access-Control-Expose-Headers", "X-RateLimit-Limit, X-RateLimit-Remaining, X-RateLimit-Reset, Retry-After")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

