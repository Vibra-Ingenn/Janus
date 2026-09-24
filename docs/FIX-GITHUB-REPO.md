# Fix Vibra-Ingenn/Janus on GitHub

Problem today: code is inside a `janus/` subfolder and root README is broken plain text.

Goal layout:

```text
Janus/                 (repo root)
  README.md
  build.ps1
  go.mod
  cmd/
  internal/
  ...
```

## Option A — git push (best)

Sign into GitHub as **Vibra-Ingenn** (not x0x000).

```powershell
cd C:\Dev\janus
git remote remove origin 2>$null
git remote add origin https://github.com/Vibra-Ingenn/Janus.git
git add -A
git commit -m "Flatten repo layout and fix README for GitHub display"
git push -u origin master:main --force
```

Use `--force` only if you are sure the GitHub copy should match this local folder exactly.

## Option B — browser (README only)

1. Open https://github.com/Vibra-Ingenn/Janus/blob/main/janus/README.md
2. Click **Raw** → copy all
3. Edit root https://github.com/Vibra-Ingenn/Janus/blob/main/README.md
4. Replace all → paste → **Preview** → commit

That fixes the main page text. You still need Option A to move code out of `janus/` folder.

## Verify

- Repo root shows `go.mod` next to `README.md` (not inside `janus/`)
- Main Code tab renders README below file list
- Clone URL works: `git clone https://github.com/Vibra-Ingenn/Janus.git`
