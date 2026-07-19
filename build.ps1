# ============================================
# Mutagen Web Console - Unified Build Script
# Output: build/
#   mutagen-web-server.exe       - Windows server
#   mutagen-web-server_linux     - Linux server
#   mutagen-web-agent.exe        - Windows agent
#   agent-setup.exe              - Installer
#   web\                         - Frontend
# ============================================
$ErrorActionPreference = "Stop"
$ROOT = Split-Path $PSCommandPath -Parent
$BUILD = Join-Path $ROOT "build"

Write-Host "=== Building mutagen-web-server.exe ==="
Set-Location (Join-Path $ROOT "server")
go build -o (Join-Path $BUILD "mutagen-web-server.exe") .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "=== Building mutagen-web-server_linux ==="
$env:GOOS = "linux"; $env:GOARCH = "amd64"
go build -o (Join-Path $BUILD "mutagen-web-server_linux") .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Remove-Item Env:\GOOS, Env:\GOARCH -ErrorAction SilentlyContinue

Write-Host "=== Building mutagen-web-agent.exe ==="
Set-Location (Join-Path $ROOT "agent")
go build -o (Join-Path $BUILD "mutagen-web-agent.exe") .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "=== Building agent-setup.exe ==="
Set-Location (Join-Path $ROOT "agent-setup")
go build -o (Join-Path $BUILD "agent-setup.exe") .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "=== Building frontend ==="
Set-Location (Join-Path $ROOT "web")
npm run build
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "=== Copying frontend ==="
Copy-Item (Join-Path $ROOT "web\dist\*") (Join-Path $BUILD "web\") -Recurse -Force

Write-Host ""
Write-Host "Build complete!"
Get-ChildItem $BUILD -Exclude web | Select-Object Name, Length