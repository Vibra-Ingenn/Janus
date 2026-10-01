# run.ps1 — rebuild janus.exe and launch it.
#
# Usage (from project root):
#   .\run.ps1              # rebuild and run
#   .\run.ps1 -NoBuild     # just run the existing dist/janus.exe
#   .\run.ps1 -Test        # run full test suite before building

[CmdletBinding()]
param(
    [switch]$NoBuild,
    [switch]$Test
)

$ErrorActionPreference = "Stop"
$ProjectRoot = $PSScriptRoot
Set-Location $ProjectRoot

# Kill any previous instance so the .exe isn't locked during rebuild.
$existing = Get-Process -Name "janus" -ErrorAction SilentlyContinue
if ($existing) {
    Write-Host "[run] stopping existing janus (PID $($existing.Id))..." -ForegroundColor Yellow
    $existing | Stop-Process -Force
    Start-Sleep -Milliseconds 500
}

if ($Test) {
    Write-Host "[run] go test ./..." -ForegroundColor Cyan
    go test ./... -count=1
    if ($LASTEXITCODE -ne 0) { Write-Error "tests failed"; exit 1 }
}

if (-not $NoBuild) {
    Write-Host "[run] building dist\janus.exe ..." -ForegroundColor Cyan
    go build -o dist\janus.exe .\cmd\janus
    if ($LASTEXITCODE -ne 0) { Write-Error "build failed"; exit 1 }
    Write-Host "[run] build OK" -ForegroundColor Green
}

Write-Host "[run] launching dist\janus.exe ..." -ForegroundColor Cyan
Write-Host ""
& .\dist\janus.exe
