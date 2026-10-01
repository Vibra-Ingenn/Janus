# Janus Local AI Server Startup Script
# This starts Janus with your local GGUF model on RTX 5070 Vulkan

$ErrorActionPreference = "Stop"

$ProjectPath = Split-Path -Parent $MyInvocation.MyCommand.Path
$JanusExe = Join-Path $ProjectPath "dist\janus.exe"

if (-not (Test-Path $JanusExe)) {
    Write-Error "Janus binary not found at $JanusExe. Run build first."
    exit 1
}

Write-Host "🜁 Starting Janus Local AI Server..." -ForegroundColor Cyan
Write-Host "   Backend: Vulkan (RTX 5070)" -ForegroundColor Green
Write-Host "   Model: $env:JANUS_MODEL_PATH" -ForegroundColor Green
Write-Host "   API: http://127.0.0.1:8990" -ForegroundColor Green
Write-Host ""

& $JanusExe
