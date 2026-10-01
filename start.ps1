Write-Host "========================================================" -ForegroundColor Cyan
Write-Host "       Starting FluxGate Full Stack System...           " -ForegroundColor Cyan
Write-Host "========================================================" -ForegroundColor Cyan
Write-Host ""

docker compose -f deployments/docker-compose.yml up --build -d

if ($LASTEXITCODE -eq 0) {
    Write-Host ""
    Write-Host "All containers started successfully!" -ForegroundColor Green
    Write-Host "Opening Dashboard at http://localhost:5173 ..." -ForegroundColor Yellow
    Start-Process "http://localhost:5173"
    
    Write-Host ""
    Write-Host "--------------------------------------------------------" -ForegroundColor DarkGray
    Write-Host "  Frontend Dashboard : http://localhost:5173" -ForegroundColor Green
    Write-Host "  Gateway API Edge   : http://localhost:8080" -ForegroundColor White
    Write-Host "  Prometheus Metrics : http://localhost:9090" -ForegroundColor White
    Write-Host "  Grafana Dashboard  : http://localhost:3000" -ForegroundColor White
    Write-Host "--------------------------------------------------------" -ForegroundColor DarkGray
    Write-Host "To shut down all services anytime, run: .\stop.ps1" -ForegroundColor Gray
} else {
    Write-Host ""
    Write-Host "[ERROR] Failed to start containers. Please ensure Docker Desktop is running." -ForegroundColor Red
}
