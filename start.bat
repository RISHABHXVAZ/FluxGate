@echo off
title FluxGate - Starting System
echo ========================================================
echo        Starting FluxGate Full Stack System...
echo ========================================================
echo.

docker compose -f deployments/docker-compose.yml up --build -d

if %ERRORLEVEL% EQU 0 (
    echo.
    echo All services started successfully!
    echo Opening FluxGate Dashboard in your browser...
    start http://localhost:5173
    echo.
    echo --------------------------------------------------------
    echo   Frontend Dashboard : http://localhost:5173
    echo   Gateway API Edge   : http://localhost:8080
    echo   Prometheus Metrics : http://localhost:9090
    echo   Grafana Dashboard  : http://localhost:3000
    echo --------------------------------------------------------
    echo.
    echo To stop everything anytime, type: stop.bat
) else (
    echo.
    echo [ERROR] Failed to start containers.
    echo Please make sure Docker Desktop is running and try again.
)
