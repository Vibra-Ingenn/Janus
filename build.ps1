# build.ps1 — Janus Windows build script
# Downloads pre-built llama.cpp Vulkan DLLs from GitHub Releases,
# then builds the janus.exe binary.
#
# Usage:
#   .\build.ps1                    # auto-detect latest llama.cpp release
#   .\build.ps1 -LlamaVersion b5000 # pin a specific release tag
#   .\build.ps1 -SkipDownload      # use DLLs already in lib\windows\

param(
    [string]$LlamaVersion = "b12098",
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
    # /releases/latest returns v0.x.x (no binaries). Binary builds use bXXXX tags.
    $api = "https://api.github.com/repos/ggerganov/llama.cpp/releases?per_page=20"
    $headers = @{ "User-Agent" = "janus-build-script" }
    try {
        $releases = Invoke-RestMethod -Uri $api -Headers $headers
        $binRelease = $releases | Where-Object { $_.tag_name -match '^b\d+$' -and $_.assets.Count -gt 0 } | Select-Object -First 1
        if ($binRelease) {
            return $binRelease.tag_name
        }
        Write-Warning "No binary release found in recent releases, using fallback."
        return "b11326"
    } catch {
        Write-Warning "Could not fetch releases from GitHub: $_"
        return "b11326"
    }
}

if (-not $SkipDownload) {
    if ($LlamaVersion -eq "") {
        $LlamaVersion = "b12098"
    }
    Write-Host "Using llama.cpp $LlamaVersion (pinned release tag)"
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

# If lib is empty, reuse DLLs already shipped next to janus.exe
foreach ($dll in @("llama.dll")) {
    $libPath = Join-Path $LibDir $dll
    $distPath = Join-Path $DistDir $dll
    if (-not (Test-Path $libPath) -and (Test-Path $distPath)) {
        if (-not (Test-Path $LibDir)) { New-Item -ItemType Directory -Path $LibDir | Out-Null }
        Write-Host "Copying $dll from dist\ to lib\windows\ ..."
        Copy-Item $distPath $libPath -Force
    }
}

# Still missing? Download once (double-click Rebuild must not fail on empty lib)
$needDll = Join-Path $LibDir "llama.dll"
if (-not (Test-Path $needDll)) {
    Write-Host "llama.dll missing in lib and dist - downloading..."
    if ($LlamaVersion -eq "") {
        $LlamaVersion = Get-LatestLlamaRelease
    }
    Write-Host "Using llama.cpp $LlamaVersion"
    $ZipName = "llama-$LlamaVersion-bin-win-vulkan-x64.zip"
    $ZipUrl  = "https://github.com/ggerganov/llama.cpp/releases/download/$LlamaVersion/$ZipName"
    $ZipPath = Join-Path $env:TEMP $ZipName
    Invoke-WebRequest -Uri $ZipUrl -OutFile $ZipPath -UseBasicParsing
    if (-not (Test-Path $LibDir)) { New-Item -ItemType Directory -Path $LibDir | Out-Null }
    $ExtractDir = Join-Path $env:TEMP "llama-extract-$PID"
    Expand-Archive -Path $ZipPath -DestinationPath $ExtractDir -Force
    Get-ChildItem -Path $ExtractDir -Filter "*.dll" -Recurse |
        ForEach-Object { Copy-Item $_.FullName $LibDir -Force }
    Remove-Item $ExtractDir -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item $ZipPath -Force -ErrorAction SilentlyContinue
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
$commit = ""
try {
  $commit = (git -C $Root rev-parse --short HEAD 2>$null)
} catch {}
if (-not $commit) { $commit = "local" }
$built = (Get-Date -Format "yyyy-MM-dd_HH:mm:ss")
$ld = "-s -w -X main.buildTime=$built -X main.buildCommit=$commit"
$env:GOFLAGS = ""
# Windows won't overwrite a running exe, but it will rename one. Move a running janus.exe aside so the
# build works while Janus is open; the next start of the new janus.exe replaces the old copy itself.
$exePath = Join-Path $DistDir $BinName
$oldPath = Join-Path $DistDir "janus.old.exe"
if (Test-Path $oldPath) { Remove-Item $oldPath -Force -ErrorAction SilentlyContinue }
if ((Test-Path $exePath) -and -not (Test-Path $oldPath)) {
  try { Move-Item $exePath $oldPath -Force -ErrorAction Stop } catch {}
}
& go build -ldflags $ld -o $exePath ./cmd/janus
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# ---------------------------------------------------------------------------
# 4. Assemble dist/ — copy DLLs alongside the binary
# ---------------------------------------------------------------------------

Write-Host "Assembling dist/ ..."
# A running Janus locks its DLLs. If the copy in dist\ is already the same size and date, skip it;
# otherwise say which file is locked instead of failing the whole build.
Get-ChildItem $LibDir -Filter "*.dll" |
    ForEach-Object {
        $dest = Join-Path $DistDir $_.Name
        $same = (Test-Path $dest) -and ((Get-Item $dest).Length -eq $_.Length) -and ((Get-Item $dest).LastWriteTime -eq $_.LastWriteTime)
        if (-not $same) {
            try { Copy-Item $_.FullName $DistDir -Force -ErrorAction Stop }
            catch { Write-Warning "$($_.Exception.Message) Close Janus and build again to update this file." }
        }
    }

Copy-Item (Join-Path $Root "webui.html") $DistDir -Force -ErrorAction SilentlyContinue
Copy-Item (Join-Path $Root "console.js") $DistDir -Force -ErrorAction SilentlyContinue
Copy-Item (Join-Path $Root "tool.json") $DistDir -Force -ErrorAction SilentlyContinue
$StaticSrc = Join-Path $Root "static"
$StaticDst = Join-Path $DistDir "static"
if (Test-Path $StaticSrc) {
    Copy-Item $StaticSrc $StaticDst -Recurse -Force -ErrorAction SilentlyContinue
}

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
