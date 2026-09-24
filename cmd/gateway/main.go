package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fluxgate/internal/config"
	"fluxgate/internal/httpedge"
	"fluxgate/internal/metrics"
	"fluxgate/internal/ratelimit"
	"fluxgate/internal/redisclient"
	"fluxgate/internal/upstream"

	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	port := os.Getenv("GATEWAY_PORT")
	if port == "" {
		port = "8080"
	}

	backendAddr := os.Getenv("BACKEND_ADDR")
	if backendAddr == "" {
		backendAddr = "localhost:50051"
	}

	log.Println("Starting FluxGate API Gateway...")

	// 1. Initialize Redis Client
	rCfg := redisclient.DefaultConfig()
	rClient, err := redisclient.NewClient(rCfg)
	if err != nil {
		log.Fatalf("Fatal: Failed to connect to Redis at %s: %v", rCfg.Addr, err)
	}
	defer rClient.Close()
	log.Printf("Connected to Redis at %s", rCfg.Addr)

	// 2. Initialize Dynamic Config Manager & Assert Default Policy (D17)
	cfgMgr := config.NewManager(rClient, 10*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := cfgMgr.EnsureDefaultConfig(ctx); err != nil {
		log.Fatalf("Fatal: Startup integrity check failed (D17): %v", err)
	}
	log.Println("Default rate limit configuration validated.")

	// 3. Initialize Sliding Window Engine
	luaPath := os.Getenv("LUA_SCRIPT_PATH")
	if luaPath == "" {
		luaPath = "scripts/lua/sliding_window.lua"
	}

	scriptBytes, err := os.ReadFile(luaPath)
	if err != nil {
		log.Fatalf("Fatal: Failed to read Lua script at %s: %v", luaPath, err)
	}

	engine, err := ratelimit.NewEngine(rClient, string(scriptBytes))
	if err != nil {
		log.Fatalf("Fatal: Failed to load Lua script into Redis: %v", err)
	}
	log.Println("Sliding Window Engine initialized.")

	// 4. Initialize Upstream gRPC Client Pool
	upClient, err := upstream.NewClient(backendAddr)
	if err != nil {
		log.Fatalf("Fatal: Failed to connect to upstream gRPC backend at %s: %v", backendAddr, err)
	}
	defer upClient.Close()
	log.Printf("Upstream gRPC client connected to %s", backendAddr)

	// 5. Initialize Observability & HTTP Edge Layer
	m := metrics.NewMetrics(prometheus.DefaultRegisterer)
	rlMiddleware := ratelimit.NewMiddleware(engine, cfgMgr, m)
	handler := httpedge.NewHandler(upClient, rClient.Ping)
	router := httpedge.NewRouter(handler, rlMiddleware)
	server := httpedge.NewServer(port, router)

	// 6. Graceful Shutdown Coordinator
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("FluxGate HTTP Gateway listening on :%s", port)
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Gateway server terminated unexpectedly: %v", err)
		}
	}()

	<-stop
	log.Println("Shutdown signal received. Draining connections...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error during server shutdown: %v", err)
	}
	log.Println("FluxGate Gateway stopped cleanly.")
}
