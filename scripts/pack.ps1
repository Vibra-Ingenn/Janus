#Requires -Version 5.1
param(
    [string]$OutDir = "C:\Dev\_test\janus_release_zip"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

& "$Root\build.ps1"
if ($LASTEXITCODE -ne 0) { throw "build.ps1 failed" }

$stamp = Get-Date -Format "yyyyMMdd-HHmm"
$zipName = "janus-portable-$stamp.zip"
Remove-Item -Recurse -Force $OutDir -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$stage = Join-Path $OutDir "stage"
New-Item -ItemType Directory -Force -Path $stage | Out-Null

Copy-Item -Path "$Root\dist\*" -Destination $stage -Recurse -Force
Copy-Item -Path "$Root\backend_config.example.json" -Destination $stage -Force -ErrorAction SilentlyContinue
Copy-Item -Path "$Root\webui.html" -Destination $stage -Force -ErrorAction SilentlyContinue
Copy-Item -Path "$Root\start-janus.ps1" -Destination $stage -Force -ErrorAction SilentlyContinue

$zipPath = Join-Path $OutDir $zipName
if (Test-Path $zipPath) { Remove-Item $zipPath -Force }
Compress-Archive -Path (Join-Path $stage "*") -DestinationPath $zipPath
Write-Host "Wrote $zipPath"
