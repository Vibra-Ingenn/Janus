# Janus Local AI Server Startup Script
# This starts Janus with your local GGUF model on RTX 5070 Vulkan

$ErrorActionPreference = "Stop"

$ProjectPath = Split-Path -Parent $MyInvocation.MyCommand.Path
$JanusExe = Join-Path $ProjectPath "dist\janus.exe"

if (-not (Test-Path $JanusExe)) {
    Write-Error "Janus binary not found at $JanusExe. Run .\run.ps1 first."
    exit 1
}

Set-Location -LiteralPath $ProjectPath

Write-Host "🜁 Starting Janus Local AI Server..." -ForegroundColor Cyan
Write-Host "   Install: $ProjectPath" -ForegroundColor DarkGray
Write-Host "   API: http://127.0.0.1:8990 (set JANUS_LISTEN_ADDR in .env to use another port)" -ForegroundColor Green
Write-Host ""

& $JanusExe
