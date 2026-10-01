#!/usr/bin/env bash
echo "Stopping FluxGate Full Stack System..."
docker compose -f deployments/docker-compose.yml down
echo "All FluxGate services stopped."
