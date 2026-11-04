# Stop only janus.exe running from this repo's dist\ folder (no wmic).
param(
    [string]$Root = (Split-Path $PSScriptRoot -Parent)
)

$ErrorActionPreference = "Stop"
$distExe = (Join-Path $Root "dist\janus.exe")
$distNorm = [System.IO.Path]::GetFullPath($distExe)

Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
    Where-Object {
        $_.Name -match '(?i)^janus\.exe$' -and
        $_.ExecutablePath -and
        ([System.IO.Path]::GetFullPath($_.ExecutablePath) -eq $distNorm)
    } |
    ForEach-Object {
        Write-Host "Stopping dist janus PID $($_.ProcessId)"
        Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
    }

Start-Sleep -Milliseconds 400
