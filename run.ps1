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
Set-Location -LiteralPath $ProjectRoot

# Stop only janus.exe launched from this repo tree (same port as other local installs).
$rootNorm = $ProjectRoot.TrimEnd('\', '/')
Get-CimInstance Win32_Process -Filter "Name='janus.exe'" -ErrorAction SilentlyContinue |
    Where-Object {
        $_.ExecutablePath -and
        ($_.ExecutablePath.StartsWith($rootNorm + '\', [StringComparison]::OrdinalIgnoreCase) -or
         $_.ExecutablePath.StartsWith($rootNorm + '/', [StringComparison]::OrdinalIgnoreCase))
    } |
    ForEach-Object {
        Write-Host "[run] stopping $($_.ExecutablePath) (PID $($_.ProcessId))..." -ForegroundColor Yellow
        Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
    }
Start-Sleep -Milliseconds 400

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
