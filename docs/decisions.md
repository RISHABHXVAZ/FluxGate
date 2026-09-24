# Architecture & Design Decisions Log

This document tracks foundational architecture and design decisions (D1 through D19) established for **FluxGate**.

---

### D1: Rate-Limit Identity
* **Decision:** Rate-limit identity is determined by the API key provided in the `X-API-Key` request header.
* **Details:** Every client must identify itself via `X-API-Key`.

### D2: Boundary Semantics
* **Decision:** Requests $1$ through $N$ are allowed within the window; request $N+1$ is rejected.
* **Details:** Strict, deterministic threshold semantics without off-by-one errors.

### D3: Redis Failure Semantics
* **Decision:** Fail-closed.
* **Details:** If Redis is unreachable or returns an error during rate-limit evaluation, requests are blocked rather than allowed through, protecting downstream services from unbounded traffic spikes.

### D4: Process & Deployment Architecture
* **Decision:** Single process combining the Gateway and Rate Limiter with internal Go package separation.
* **Details:** Eliminates inter-process latency between gateway edge routing and rate-limiting logic while preserving modularity.

### D5: Upstream Backend
* **Decision:** Simple built-in demo backend service.
* **Details:** A purpose-built upstream service (`cmd/backend`) implementing the `DemoBackend` gRPC service for end-to-end routing, validation, and load testing.

### D6: Communication Protocols
* **Decision:** Client $\rightarrow$ Gateway via HTTP/1.1 (JSON/REST); Gateway $\rightarrow$ Backend via gRPC (Protobuf).
* **Details:** Acts as an edge protocol gateway translating client HTTP requests into strongly-typed gRPC RPC calls.

### D7: Dynamic Limits Configuration
* **Decision:** Dynamic configuration via Redis with in-memory caching (Option C).
* **Details:** Rate-limit policies and tier limits are stored in Redis (e.g., `ratelimit:config:<tier>`) and cached in-memory with a short TTL (5–10s) to balance low latency with dynamic updates.

### D8: Kubernetes Target
* **Decision:** Local Kubernetes environment targeting Kind or Minikube.
* **Details:** Ensures deployment manifests and orchestration can be validated locally with reproducible infrastructure.

### D9: Project Priorities
* **Decision:** Prioritize correctness, deep engineering understanding, and interview-explainability over premature complexity.
* **Details:** Focus on rock-solid concurrency, atomic Redis operations, clean contracts, and transparent failure modes.

### D10: Component Boundaries
* **Decision:** Distinct system components:
  1. **Gateway + Rate Limiter** (`cmd/gateway`)
  2. **Demo Backend** (`cmd/backend`)
  3. **Redis** (Sliding-window state store & dynamic configs)
  4. **Prometheus** (Metrics collection)
  5. **Grafana** (Visualization & dashboards)

### D11: Implementation Language
* **Decision:** Go (Golang).
* **Details:** Go provides high-performance concurrent networking, low memory footprint, and first-class gRPC and Prometheus ecosystems.

### D12: Redis Failure HTTP Status
* **Decision:** HTTP `503 Service Unavailable`.
* **Details:** When rate limiting fails due to Redis unavailability (under fail-closed policy D3), the gateway responds with HTTP 503 indicating temporary infrastructure degradation.

### D13: Accounting for Rejected Requests
* **Decision:** Rejected requests are excluded from the ZSET.
* **Details:** Requests that exceed the rate limit do not add entries to the sliding-window ZSET. Rejections do not consume quota or prolong the rate-limited window.

### D14: Missing API Key Handling
* **Decision:** HTTP `401 Unauthorized`.
* **Details:** Requests missing the `X-API-Key` header are immediately rejected at the edge with HTTP 401 before hitting Redis or upstream services.

### D15: Backend Chaos Simulation
* **Decision:** Synthetic chaos injection flags in request payloads.
* **Details:** Upstream `EchoRequest` contract supports `inject_latency_ms`, `inject_error`, and `inject_error_code` to test gateway timeouts, retries, and error propagation under controlled failure scenarios.

### D16: Upstream Backend / gRPC Failure Status
* **Decision:** HTTP `502 Bad Gateway`.
* **Details:** When the upstream gRPC service is unavailable, errors, or fails to respond, the gateway responds to the HTTP client with HTTP 502.

### D17: Default Configuration Health Gate
* **Decision:** Missing `ratelimit:config:default` fails startup or marks the gateway unhealthy on `/ready`.
* **Details:** The gateway requires a valid default rate-limit policy in Redis to accept traffic safely. Readiness probe checks ensure traffic is only routed when configuration is loaded.

### D18: Lua Script Contract
* **Decision:** Redis sliding-window Lua script returns a 3-element array:
  `{ allowed (1 or 0), current_count, oldest_score_ms }`.
* **Details:** Enables atomic decision-making and provides accurate metadata for `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `X-RateLimit-Reset` response headers.

### D19: Reset Header Fallback
* **Decision:** If `oldest_score_ms == -1` (e.g. empty window or edge state), `X-RateLimit-Reset` defaults to current epoch seconds.
* **Details:** Ensures deterministic, well-formed rate-limit headers are always returned to clients even when no previous requests remain in the window.
