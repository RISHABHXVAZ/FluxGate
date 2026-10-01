#!/usr/bin/env bash
set -e

echo "========================================================"
echo "       Starting FluxGate Full Stack System...           "
echo "========================================================"
echo ""

docker compose -f deployments/docker-compose.yml up --build -d

echo ""
echo "All containers started successfully!"
echo "Opening Dashboard at http://localhost:5173 ..."

# Attempt to open browser on macOS, Linux, or WSL
if command -v xdg-open > /dev/null; then
    xdg-open "http://localhost:5173" > /dev/null 2>&1 &
elif command -v open > /dev/null; then
    open "http://localhost:5173" > /dev/null 2>&1 &
fi

echo ""
echo "--------------------------------------------------------"
echo "  Frontend Dashboard : http://localhost:5173"
echo "  Gateway API Edge   : http://localhost:8080"
echo "  Prometheus Metrics : http://localhost:9090"
echo "  Grafana Dashboard  : http://localhost:3000"
echo "--------------------------------------------------------"
echo "To shut down all services anytime, run: ./stop.sh"
