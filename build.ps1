# build.ps1 — Janus Windows build script
# Downloads pre-built llama.cpp Vulkan DLLs from GitHub Releases,
# then builds the janus.exe binary.
#
# Usage:
#   .\build.ps1                    # auto-detect latest llama.cpp release
#   .\build.ps1 -LlamaVersion b5000 # pin a specific release tag
#   .\build.ps1 -SkipDownload      # use DLLs already in lib\windows\

param(
    [string]$LlamaVersion = "",
    [switch]$SkipDownload
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$Root     = $PSScriptRoot
$LibDir   = Join-Path $Root "lib\windows"
$DistDir  = Join-Path $Root "dist"
$BinName  = "janus.exe"

# ---------------------------------------------------------------------------
# 1. Resolve the llama.cpp release version to download
# ---------------------------------------------------------------------------

function Get-LatestLlamaRelease {
    $api = "https://api.github.com/repos/ggerganov/llama.cpp/releases/latest"
    $headers = @{ "User-Agent" = "janus-build-script" }
    try {
        $resp = Invoke-RestMethod -Uri $api -Headers $headers
        return $resp.tag_name
    } catch {
        Write-Warning "Could not fetch latest release from GitHub: $_"
        return "b5000"
    }
}

if (-not $SkipDownload) {
    if ($LlamaVersion -eq "") {
        Write-Host "Fetching latest llama.cpp release tag..."
        $LlamaVersion = Get-LatestLlamaRelease
    }
    Write-Host "Using llama.cpp $LlamaVersion"
}

# ---------------------------------------------------------------------------
# 2. Download and extract Vulkan DLLs
# ---------------------------------------------------------------------------

if (-not $SkipDownload) {
    $ZipName = "llama-$LlamaVersion-bin-win-vulkan-x64.zip"
    $ZipUrl  = "https://github.com/ggerganov/llama.cpp/releases/download/$LlamaVersion/$ZipName"
    $ZipPath = Join-Path $env:TEMP $ZipName

    Write-Host "Downloading $ZipName..."
    Invoke-WebRequest -Uri $ZipUrl -OutFile $ZipPath -UseBasicParsing

    Write-Host "Extracting to $LibDir ..."
    if (-not (Test-Path $LibDir)) { New-Item -ItemType Directory -Path $LibDir | Out-Null }

    $ExtractDir = Join-Path $env:TEMP "llama-extract-$PID"
    Expand-Archive -Path $ZipPath -DestinationPath $ExtractDir -Force

    # Copy DLL files — the zip may have a sub-folder
    Get-ChildItem -Path $ExtractDir -Filter "*.dll" -Recurse |
        ForEach-Object { Copy-Item $_.FullName $LibDir -Force }

    Remove-Item $ExtractDir -Recurse -Force
    Remove-Item $ZipPath    -Force

    Write-Host "DLLs extracted:"
    Get-ChildItem $LibDir -Filter "*.dll" | ForEach-Object { Write-Host "  $($_.Name)" }
} else {
    Write-Host "Skipping DLL download (-SkipDownload)."
}

# Verify required DLLs are present
$Required = @("llama.dll")
foreach ($dll in $Required) {
    $dllPath = Join-Path $LibDir $dll
    if (-not (Test-Path $dllPath)) {
        Write-Error "Required DLL not found: $dllPath`nRun without -SkipDownload to fetch it."
    }
}

# ---------------------------------------------------------------------------
# 3. Build the Go binary
# ---------------------------------------------------------------------------

Write-Host "Running go mod tidy..."
& go mod tidy
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

if (-not (Test-Path $DistDir)) { New-Item -ItemType Directory -Path $DistDir | Out-Null }

Write-Host "Building janus.exe..."
$env:GOFLAGS = ""
& go build -ldflags "-s -w" -o (Join-Path $DistDir $BinName) ./cmd/janus
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# ---------------------------------------------------------------------------
# 4. Assemble dist/ — copy DLLs alongside the binary
# ---------------------------------------------------------------------------

Write-Host "Assembling dist/ ..."
Get-ChildItem $LibDir -Filter "*.dll" |
    ForEach-Object { Copy-Item $_.FullName $DistDir -Force }

# Copy models directory structure (not the .gguf files themselves — too large)
$ModelsDir = Join-Path $Root "models"
$DistModels = Join-Path $DistDir "models"
if (-not (Test-Path $DistModels)) { New-Item -ItemType Directory -Path $DistModels | Out-Null }

Write-Host ""
Write-Host "Build complete:"
Write-Host "  $DistDir\"
Get-ChildItem $DistDir | ForEach-Object { Write-Host "    $($_.Name)" }
Write-Host ""
Write-Host "Usage:"
Write-Host "  Copy dist\ anywhere. Place your .gguf model in dist\models\"
Write-Host "  Set JANUS_MODEL_PATH=.\models\yourmodel.gguf in .env"
Write-Host "  Run: dist\janus.exe"
