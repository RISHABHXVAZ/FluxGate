@echo off
title FluxGate - Stopping System
echo Stopping FluxGate system...
docker compose -f deployments/docker-compose.yml down
echo.
echo FluxGate stopped cleanly.
