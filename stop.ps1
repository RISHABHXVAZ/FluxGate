Write-Host "Stopping FluxGate system..." -ForegroundColor Yellow
docker compose -f deployments/docker-compose.yml down
Write-Host "FluxGate stopped cleanly." -ForegroundColor Green
