package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Metrics struct {
	RequestsTotal       *prometheus.CounterVec
	RequestDuration     *prometheus.HistogramVec
	RedisCallDuration   *prometheus.HistogramVec
	BackendCallDuration *prometheus.HistogramVec
	BackendErrorsTotal  *prometheus.CounterVec
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)

	return &Metrics{
		RequestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gateway_requests_total",
				Help: "Total number of HTTP requests handled by final outcome.",
			},
			[]string{"outcome", "api_key"},
		),
		RequestDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gateway_request_duration_seconds",
				Help:    "Total end-to-end request duration through the gateway in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"outcome"},
		),
		RedisCallDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gateway_redis_call_duration_seconds",
				Help:    "Duration of Redis rate-limiting calls in seconds.",
				Buckets: []float64{0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1},
			},
			[]string{"result"},
		),
		BackendCallDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gateway_backend_call_duration_seconds",
				Help:    "Duration of upstream gRPC backend calls in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"status"},
		),
		BackendErrorsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gateway_backend_errors_total",
				Help: "Total number of gRPC backend invocation errors.",
			},
			[]string{"status"},
		),
	}
}
