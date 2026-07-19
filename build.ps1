# ============================================
# Mutagen Web Console - Build Script
# Output: build/
#   mutagen-web-server_linux     - Linux server (amd64)
#   mutagen-web-agent.exe        - Windows agent
#   web\                         - Frontend static files
# ============================================
$ErrorActionPreference = "Stop"
$ROOT = Split-Path $PSCommandPath -Parent
$BUILD = Join-Path $ROOT "build"

Write-Host "=== Building mutagen-web-server_linux ==="
Set-Location (Join-Path $ROOT "server")
$env:GOOS = "linux"; $env:GOARCH = "amd64"
go build -o (Join-Path $BUILD "mutagen-web-server_linux") .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Remove-Item Env:\GOOS, Env:\GOARCH -ErrorAction SilentlyContinue

Write-Host "=== Building mutagen-web-agent.exe ==="
$env:GOOS = "windows"; $env:GOARCH = "amd64"
Set-Location (Join-Path $ROOT "agent")
go build -o (Join-Path $BUILD "mutagen-web-agent.exe") .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Remove-Item Env:\GOOS, Env:\GOARCH -ErrorAction SilentlyContinue

Write-Host "=== Building frontend ==="
Set-Location (Join-Path $ROOT "web")
npm run build
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "=== Copying frontend ==="
Copy-Item (Join-Path $ROOT "web\dist\*") (Join-Path $BUILD "web\") -Recurse -Force

Write-Host ""
Write-Host "Build complete!"
Get-ChildItem $BUILD -Exclude web | Select-Object Name, Length