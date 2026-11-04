# Start Janus for automated tests: no browser, no console window, test port only.
param(
    [Parameter(Mandatory = $true)]
    [string]$Dist,
    [Parameter(Mandatory = $true)]
    [string]$Port,
    [hashtable]$ExtraEnv = @{}
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$exe = Join-Path $Dist "janus.exe"
if (-not (Test-Path $exe)) {
    throw "janus.exe not found: $exe"
}

$env:JANUS_LISTEN_ADDR = "127.0.0.1:$Port"
$env:JANUS_NO_BROWSER = "1"
$env:JANUS_DEFER_MODEL_LOAD = "1"
$env:JANUS_AUTO = "0"
$env:JANUS_AUTH = "false"
$env:JANUS_SCAN_USER_PATHS = "0"
$env:INFERENCE_BACKEND = "vulkan"
$env:GOWORK = "off"

foreach ($k in $ExtraEnv.Keys) {
    Set-Item -Path "Env:$k" -Value $ExtraEnv[$k]
}

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $exe
$psi.WorkingDirectory = $Dist
$psi.UseShellExecute = $false
$psi.CreateNoWindow = $true
$psi.WindowStyle = [System.Diagnostics.ProcessWindowStyle]::Hidden

foreach ($entry in [System.Environment]::GetEnvironmentVariables("Process").GetEnumerator()) {
    if ($entry.Key -match '^JANUS_|^GOWORK$|^INFERENCE_BACKEND$') {
        $psi.Environment[$entry.Key] = [string]$entry.Value
    }
}
$psi.Environment["JANUS_LISTEN_ADDR"] = "127.0.0.1:$Port"
$psi.Environment["JANUS_NO_BROWSER"] = "1"

$p = [System.Diagnostics.Process]::Start($psi)
Write-Output $p.Id
