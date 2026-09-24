# Janus — Air-Gapped Installation Guide

This guide covers deploying Janus in a fully offline, air-gapped environment with no internet connectivity. All steps must be completed on a machine with internet access first, then the resulting bundle is transferred to the isolated environment.

---

## Prerequisites (online machine)

| Requirement | Version |
|---|---|
| Windows 10/11 x64 | 22H2 or later |
| Go toolchain | 1.22+ |
| Git | Any |
| NVIDIA GPU drivers | 560+ (for Vulkan) |
| Vulkan SDK | 1.3+ |

---

## Step 1 — Build the offline bundle (online machine)

```powershell
# Clone the repository
git clone https://github.com/Vibra-Ingenn/Janus.git
cd janus

# Build the main binary and keygen tool
go build -o dist/janus.exe   ./cmd/janus
go build -o dist/keygen.exe  ./cmd/keygen

# Download your GGUF model (example)
# Place it in the models/ directory
# e.g.: models/Llama-3.2-3B-Instruct.Q8_0.gguf
```

### Required DLL files (place in `dist/`)

Copy these DLLs from your llama.cpp build into `dist/`:

```
dist/
  llama.dll
  ggml.dll
  ggml-vulkan.dll
  ggml-base.dll
  ggml-cpu-zen4.dll   (or ggml-cpu.dll for non-AVX512)
  ggml-rpc.dll
```

Pre-built Windows DLLs can be downloaded from:  
https://github.com/ggerganov/llama.cpp/releases

---

## Step 2 — Generate a license key (online machine, optional)

If deploying an Enterprise or Professional license:

```powershell
dist\keygen.exe `
  -licensee "Your Organization" `
  -tier enterprise `
  -expires 2027-01-01 `
  -seats 10 `
  -secret "YOUR-HMAC-SECRET" `
  -out license.key
```

Keep `YOUR-HMAC-SECRET` secure. The same secret must be compiled into the binary via `-ldflags`.

---

## Step 3 — Create the transfer bundle

```powershell
# Create a zip with everything needed
Compress-Archive -Path @(
    "dist\",
    "models\",
    "webui.html",
    "license.key",   # if applicable
    ".env.example"
) -DestinationPath janus-airgap-bundle.zip
```

Transfer `janus-airgap-bundle.zip` to the air-gapped machine via USB, CD, or secure file transfer.

---

## Step 4 — Install on the air-gapped machine

```powershell
# Extract
Expand-Archive janus-airgap-bundle.zip -DestinationPath C:\Janus

# Create required directories
New-Item -ItemType Directory -Force -Path C:\Janus\logs
New-Item -ItemType Directory -Force -Path C:\Janus\data
New-Item -ItemType Directory -Force -Path C:\Janus\workspace
```

---

## Step 5 — Configure the environment

Copy `.env.example` to `.env` and edit:

```ini
# Inference backend — use 'vulkan' for GPU, 'cpu' for CPU-only
INFERENCE_BACKEND=vulkan

# Path to your GGUF model
JANUS_MODEL_PATH=C:\Janus\models\Llama-3.2-3B-Instruct.Q8_0.gguf

# GPU layers (-1 = all layers on GPU, 0 = CPU only)
JANUS_GPU_LAYERS=-1

# Optional Janus VRAM budget in MiB (internal ceiling)
# JANUS_VRAM_CEILING_MB=9728

# Maximum tokens per inference call
JANUS_MAX_TOKENS=512

# Local-only mode — prefer local inference (set false to allow cloud backends)
JANUS_LOCAL_ONLY=true

# Disable browser auto-open if running headless
JANUS_NO_BROWSER=true
```

---

## Step 6 — Run Janus

```powershell
cd C:\Janus
.\dist\janus.exe
```

Access the UI at: http://127.0.0.1:8990

For a production service, install as a Windows Service using NSSM:

```powershell
nssm install Janus "C:\Janus\dist\janus.exe"
nssm set Janus AppDirectory "C:\Janus"
nssm set Janus AppEnvironmentExtra "JANUS_LOCAL_ONLY=true"
nssm start Janus
```

---

## Step 7 — Verify air-gapped operation

1. Confirm the log shows `janus: local engine ready [vulkan]`
2. Navigate to http://127.0.0.1:8990 → Advanced → Config
3. Confirm the active model path and backend show `vulkan` or `cpu`
4. Check `logs/audit.jsonl` is growing on each inference call (if audit is enabled)
5. Verify no outbound network connections using: `netstat -an | findstr ESTABLISHED`

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `local engine unavailable` | DLLs missing or wrong path | Verify all DLLs in `dist/`, check `JANUS_LIB_PATH` |
| `HMAC signature invalid` | Wrong signing secret | Rebuild with correct `-ldflags` secret |
| UI shows `Community` tier | `license.key` not found | Place `license.key` in same dir as `janus.exe` |
| Vulkan falls back to CPU | GPU driver too old | Update to NVIDIA driver 560+ |
| Port 8990 in use | Another service | Set `JANUS_LISTEN_ADDR=127.0.0.1:8991` in `.env` |

---

## Security hardening

- Run `janus.exe` as a non-admin Windows service account
- Use Windows Firewall to block all outbound connections from the service account
- Enable Windows Event Log forwarding from `logs/audit.jsonl` to your SIEM
- Rotate the HMAC signing secret annually and re-issue license keys
- Store `license.key` with read-only permissions for the service account

---

*Janus Air-Gapped Install Guide v1.0*  
*Contact: support@janus-ai.com*
