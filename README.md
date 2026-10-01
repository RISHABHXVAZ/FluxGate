# FluxGate ⚡

> High-Performance API Gateway & Distributed Sliding-Window Rate Limiter in Go.

FluxGate is an edge reverse-proxy and rate-limiting gateway designed for high-concurrency microservice architectures. It accepts client HTTP/REST requests, performs identity verification, enforces strict **atomic sliding-window rate limits** backed by Redis, translates payloads into strongly-typed **gRPC** calls, and forwards them to upstream services with full observability and fault isolation.

---

## Key Features

* **Atomic Sliding-Window Rate Limiter**: Implemented using Redis Sorted Sets (ZSET) evaluated via a Lua script for strict concurrency control without race conditions.
* **Edge Protocol Translation**: Bridges HTTP/1.1 REST JSON clients to internal gRPC (Protobuf over HTTP/2) services.
* **Fail-Closed Resiliency ([D3](docs/decisions.md#L15), [D12](docs/decisions.md#L55))**: Protects downstream services by rejecting traffic with `503 Service Unavailable` if Redis becomes unreachable or times out.
* **Zero-Quota Waste on Rejection ([D13](docs/decisions.md#L59))**: Rejected requests exceeding limits do not consume quota or extend rate-limiting windows.
* **Dynamic Multi-Tier Configuration ([D7](docs/decisions.md#L31))**: Per-key limit overrides stored in Redis with thread-safe, in-memory TTL caching (10s) to balance dynamic updates with sub-millisecond evaluation.
* **Startup Integrity Gate ([D17](docs/decisions.md#L75))**: Fails fast if baseline rate-limit rules (`ratelimit:config:default`) are missing.
* **Synthetic Chaos Injection ([D15](docs/decisions.md#L67))**: Upstream contract supports client-controlled latency and error simulation for fault-tolerance testing.
* **First-Class Observability ([D10](docs/decisions.md#L43))**: Native Prometheus metrics exporter (`/metrics`), Grafana dashboard, and Kubernetes-ready liveness (`/health`) and readiness (`/ready`) probes.

---

## Architecture Overview

```mermaid
flowchart LR
    Client["Client\n(HTTP/REST)"] -->|POST /api/v1/echo\nX-API-Key: ...| Gateway["FluxGate Gateway\n(:8080)"]
    
    subgraph Gateway Pipeline
        Auth["1. API Key Auth\n(401 if missing)"]
        Config["2. Dynamic Config\n(Redis + In-Memory Cache)"]
        Limiter["3. Atomic Rate Limiter\n(Redis Lua Script)"]
        Metrics["4. Prometheus Metrics\n(/metrics)"]
        Auth --> Config --> Limiter --> Metrics
    end
    
    Gateway --> Gateway Pipeline
    Limiter -.->|Sliding Window ZSET| Redis[("Redis 7\n(State Store)")]
    Limiter -->|If Exceeded: 429\nIf Redis Down: 503| Client
    
    Metrics -->|gRPC DemoBackend.Echo| Backend["Upstream Backend\n(:50051)"]
    Backend -->|gRPC Response| Gateway
    Gateway -->|200 OK + RateLimit Headers| Client
```

---

## Sliding Window Rate Limiting Deep-Dive

FluxGate avoids the boundary burst vulnerability of fixed-window counters and the leaky bucket queue delay by using a rolling **Sliding Window Log** via Redis Sorted Sets (`ZSET`):

1. **Eviction**: Expired timestamps older than $(now - windowMs)$ are removed atomically via `ZREMRANGEBYSCORE`.
2. **Counting**: Current entries remaining in the window are counted via `ZCARD`.
3. **Evaluation**:
   * If `count < limit`: The current timestamp is added with a unique entropy suffix via `ZADD`, the key TTL is refreshed (`PEXPIRE`), and the request is marked `allowed = 1`.
   * If `count >= limit`: The request is marked `allowed = 0` **without** adding to the ZSET ([D13](docs/decisions.md#L59)).
4. **Header Computation**: The Lua script returns `{ allowed, current_count, oldest_score_ms }` used by the gateway to populate standard rate-limiting headers:
   * `X-RateLimit-Limit`: Maximum requests permitted per window.
   * `X-RateLimit-Remaining`: Remaining capacity.
   * `X-RateLimit-Reset`: Unix epoch second when window capacity clears.
   * `Retry-After`: Wait time in seconds returned on `429 Too Many Requests`.

---

## Repository Structure

```
FluxGate/
├── cmd/
│   ├── gateway/main.go         # Gateway entrypoint & graceful shutdown coordinator
│   └── backend/main.go         # Demo gRPC backend service with chaos simulation
├── internal/
│   ├── apikey/extractor.go     # X-API-Key header parsing & validation
│   ├── config/config.go        # Dynamic Redis policy manager & local TTL cache
│   ├── httpedge/               # HTTP router, handlers, server, and health probes
│   ├── metrics/metrics.go      # Prometheus metrics definitions
│   ├── ratelimit/              # Sliding window Lua engine & rate limit middleware
│   ├── redisclient/client.go   # Connection pool, timeouts & error classifiers
│   └── upstream/grpc_client.go # Upstream gRPC client pool & Protobuf dispatch
├── proto/
│   ├── demo.proto              # gRPC service definition
│   └── gen/                    # Compiled Go Protobuf and gRPC stubs
├── scripts/lua/
│   └── sliding_window.lua      # Atomic sliding window script executed in Redis
├── deployments/
│   ├── docker-compose.yml      # Multi-container orchestration (Redis, Backend, Gateway, Prometheus, Grafana)
│   ├── Dockerfile.gateway      # Multi-stage container build for gateway
│   └── Dockerfile.backend      # Container build for gRPC backend
├── monitoring/prometheus/      # Prometheus scrape configuration
├── docs/
│   └── decisions.md            # Architecture & Design Decisions Log (D1 - D19)
└── tests/integration/          # End-to-end integration and chaos test suites
```

---

## Quickstart

### Prerequisites
* [Docker Desktop](https://www.docker.com/) (recommended) OR
* [Go 1.23+](https://golang.org/) and a local [Redis 7+](https://redis.io/) server.

---

### Option 1: Run with Docker Compose (Recommended)

1. **Start all services**:
   ```bash
   docker compose -f deployments/docker-compose.yml up --build -d
   ```

2. **Seed the default rate-limit policy in Redis ([D17](docs/decisions.md#L75))**:
   The gateway enforces that a baseline policy exists before serving traffic.
   ```bash
   docker exec -it fluxgate-redis redis-cli HSET ratelimit:config:default limit 10 window_ms 60000
   ```

3. **Restart the gateway if it booted prior to seeding**:
   ```bash
   docker restart fluxgate-gateway
   ```

4. **Verify container health**:
   ```bash
   docker compose -f deployments/docker-compose.yml ps
   ```

---

### Option 2: Run Locally with Native Go

1. **Start Redis**:
   ```bash
   docker run -d --name fluxgate-redis -p 6379:6379 redis:7-alpine
   ```

2. **Seed the default policy**:
   ```bash
   docker exec -it fluxgate-redis redis-cli HSET ratelimit:config:default limit 10 window_ms 60000
   ```

3. **Start the Upstream Backend (Terminal 1)**:
   ```bash
   export BACKEND_PORT=50051
   go run ./cmd/backend
   ```

4. **Start the FluxGate Gateway (Terminal 2)**:
   ```bash
   export GATEWAY_PORT=8080
   export REDIS_ADDR=127.0.0.1:6379
   export BACKEND_ADDR=localhost:50051
   export LUA_SCRIPT_PATH=scripts/lua/sliding_window.lua
   go run ./cmd/gateway
   ```

---

## API Usage & Examples

### 1. Health & Readiness Probes
```bash
curl -i http://localhost:8080/health
# Returns: 200 OK {"status":"UP"}

curl -i http://localhost:8080/ready
# Returns: 200 OK {"status":"READY"}
```

---

### 2. Standard Request (HTTP 200)
```bash
curl -i -X POST http://localhost:8080/api/v1/echo \
  -H "Content-Type: application/json" \
  -H "X-API-Key: client-alpha" \
  -d '{"message": "Hello FluxGate!"}'
```

**Response**:
```http
HTTP/1.1 200 OK
Content-Type: application/json
X-RateLimit-Limit: 10
X-RateLimit-Remaining: 9
X-RateLimit-Reset: 1727800060

{
  "data": {
    "message": "Hello FluxGate!",
    "server_timestamp_ms": 1727800000120
  }
}
```

---

### 3. Exceeded Limit (HTTP 429)
When sending more requests than permitted in the window:
```http
HTTP/1.1 429 Too Many Requests
Content-Type: application/json
Retry-After: 45
X-RateLimit-Limit: 10
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 1727800060

{
  "error": {
    "code": "RATE_LIMIT_EXCEEDED",
    "message": "Rate limit exceeded. Please slow down.",
    "retry_after_seconds": 45
  }
}
```

---

### 4. Missing API Key (HTTP 401)
```bash
curl -i -X POST http://localhost:8080/api/v1/echo \
  -H "Content-Type: application/json" \
  -d '{"message": "Anonymous"}'
```
```http
HTTP/1.1 401 Unauthorized
Content-Type: application/json

{
  "error": {
    "code": "API_KEY_REQUIRED",
    "message": "API key required"
  }
}
```

---

### 5. Synthetic Chaos Simulation
Test gateway timeouts or upstream failures by passing chaos injection flags to the backend:
```bash
# Injected Latency (1500ms delay)
curl -i -X POST http://localhost:8080/api/v1/echo \
  -H "Content-Type: application/json" \
  -H "X-API-Key: client-alpha" \
  -d '{"message": "test", "inject_latency_ms": 1500}'

# Injected Backend Error (Gateway returns 502 Bad Gateway)
curl -i -X POST http://localhost:8080/api/v1/echo \
  -H "Content-Type: application/json" \
  -H "X-API-Key: client-alpha" \
  -d '{"message": "test", "inject_error": true}'
```

---

## Dynamic Rate Limit Tiers

You can dynamically override limits for specific API keys in Redis at runtime without restarting any services:

```bash
# Assign custom high-tier limits to client-vip (100 requests per 10 seconds)
redis-cli HSET ratelimit:config:client-vip limit 100 window_ms 10000
```
FluxGate caches policies in-memory for 10 seconds, after which it automatically picks up the new limits.

---

## Observability

* **Prometheus Metrics**: Available at `http://localhost:8080/metrics`.
  * `gateway_requests_total{outcome="...", api_key="..."}`
  * `gateway_request_duration_seconds`
  * `gateway_redis_call_duration_seconds`
  * `gateway_backend_call_duration_seconds`
* **Prometheus UI**: `http://localhost:9090`
* **Grafana Dashboard**: `http://localhost:3000` (Default login: `admin` / `admin`)

---

## Running Tests

Execute unit and integration tests:
```bash
# Run unit tests
go test -v ./internal/ratelimit/...

# Run all integration tests (requires local Redis running on :6379)
go test -v ./tests/integration/...
```

---

## Architecture Decisions Log

For detailed technical rationale on boundary conditions, fail-closed mechanics, and design trade-offs, consult [docs/decisions.md](docs/decisions.md).
