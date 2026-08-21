# ============================================================
# Mutagen Web Console - Incremental Build Script
# Only rebuilds targets whose source files changed since last build.
# Output (under $ROOT/build/):
#   mutagen-web-server.exe       Windows server
#   mutagen-web-server_linux     Linux server (amd64)
#   mutagen-web-agent.exe        Windows agent
#   mutagen.exe                  Windows mutagen CLI (built from ./mutagen, -tags mutagencli)
#   mutagen-agents.tar.gz        Mutagen cross-platform agent bundle (built from ./mutagen/scripts/build.go)
#   web/                         Frontend static files (npm run build -> dist -> copy)
# ============================================================
$ErrorActionPreference = "Stop"

$ROOT = Split-Path $PSCommandPath -Parent
$BUILD = Join-Path $ROOT "build"
$MUTAGEN_ROOT = Join-Path $ROOT "mutagen"
$MUTAGEN_CLI = Join-Path $MUTAGEN_ROOT "cmd/mutagen"
$WEB_ROOT = Join-Path $ROOT "web"

New-Item -ItemType Directory -Path $BUILD -Force | Out-Null

$env:GOWORK = "off"

# ---------- helper: latest source mtime ----------

function Get-LatestMTime {
    param([string]$path, [string[]]$filters = @("*.go"))
    $max = [DateTime]::MinValue
    foreach ($f in $filters) {
        $files = Get-ChildItem -Path $path -Recurse -File -Filter $f -ErrorAction SilentlyContinue
        foreach ($file in $files) {
            if ($file.LastWriteTime -gt $max) { $max = $file.LastWriteTime }
        }
    }
    foreach ($extra in @("go.mod", "go.sum", "package.json", "package-lock.json")) {
        $p = Join-Path $path $extra
        if (Test-Path $p) {
            $t = (Get-Item $p).LastWriteTime
            if ($t -gt $max) { $max = $t }
        }
    }
    return $max
}

function Need-Build {
    param([string]$output, [datetime]$srcMTime)
    if (!(Test-Path $output)) { return $true }
    return $srcMTime -gt (Get-Item $output).LastWriteTime
}

$built = [System.Collections.ArrayList]::new()
$skipped = [System.Collections.ArrayList]::new()
$script:anyBuilt = $false

function Step {
    param([int]$n, [string]$name, [string]$output, [datetime]$srcMTime, [scriptblock]$block)
    Write-Host ""
    $need = Need-Build $output $srcMTime
    if ($need) {
        Write-Host "=== [$n/6] $name ===" -ForegroundColor Yellow
        Write-Host "  source changed, rebuilding..." -ForegroundColor Cyan
        & $block
        if ($LASTEXITCODE -ne 0) { throw "Step [$n/6] $name FAILED (exit=$LASTEXITCODE)" }
        $script:anyBuilt = $true
        $built.Add($name) | Out-Null
    } else {
        Write-Host "=== [$n/6] $name ===" -ForegroundColor DarkGray
        Write-Host "  skip (no changes since last build)" -ForegroundColor DarkGray
        $skipped.Add($name) | Out-Null
    }
}

# ---------- compute source mtimes ----------

$serverSrcTime = Get-LatestMTime (Join-Path $ROOT "server") @("*.go")
$agentSrcTime = Get-LatestMTime (Join-Path $ROOT "agent") @("*.go")
$mutagenSrcTime = Get-LatestMTime $MUTAGEN_ROOT @("*.go", "*.s")
$webSrcTime = Get-LatestMTime $WEB_ROOT @("*.vue", "*.js", "*.ts", "*.css", "*.json", "*.html")

# ---------- [1/6] mutagen-web-server.exe (Windows) ----------
Step -n 1 -name "mutagen-web-server.exe (Windows)" -output (Join-Path $BUILD "mutagen-web-server.exe") -srcMTime $serverSrcTime -block {
    Set-Location (Join-Path $ROOT "server")
    $env:GOOS = "windows"; $env:GOARCH = "amd64"
    go build -o (Join-Path $BUILD "mutagen-web-server.exe") .
    $env:GOOS = ""; $env:GOARCH = ""
}

# ---------- [2/6] mutagen-web-server_linux (Linux amd64) ----------
Step -n 2 -name "mutagen-web-server_linux (Linux amd64)" -output (Join-Path $BUILD "mutagen-web-server_linux") -srcMTime $serverSrcTime -block {
    Set-Location (Join-Path $ROOT "server")
    $env:GOOS = "linux"; $env:GOARCH = "amd64"
    go build -a -o (Join-Path $BUILD "mutagen-web-server_linux") .
    $env:GOOS = ""; $env:GOARCH = ""
}

# ---------- [3/6] mutagen-web-agent.exe (Windows) ----------
Step -n 3 -name "mutagen-web-agent.exe (Windows)" -output (Join-Path $BUILD "mutagen-web-agent.exe") -srcMTime $agentSrcTime -block {
    Set-Location (Join-Path $ROOT "agent")
    $env:GOOS = "windows"; $env:GOARCH = "amd64"
    go build -o (Join-Path $BUILD "mutagen-web-agent.exe") .
    $env:GOOS = ""; $env:GOARCH = ""
}

# ---------- [4/6] mutagen.exe (Windows, from forked ./mutagen) ----------
Step -n 4 -name "mutagen.exe (from mutagen/cmd/mutagen, -tags mutagencli)" -output (Join-Path $BUILD "mutagen.exe") -srcMTime $mutagenSrcTime -block {
    Set-Location $MUTAGEN_CLI
    $env:GOOS = "windows"; $env:GOARCH = "amd64"
    go build -tags mutagencli -o (Join-Path $BUILD "mutagen.exe") .
    $env:GOOS = ""; $env:GOARCH = ""
}

# ---------- [5/6] mutagen-agents.tar.gz (from forked ./mutagen) ----------
Step -n 5 -name "mutagen-agents.tar.gz (mutagen/scripts/build.go)" -output (Join-Path $BUILD "mutagen-agents.tar.gz") -srcMTime $mutagenSrcTime -block {
    Set-Location $MUTAGEN_ROOT
    go run scripts/build.go
    $src = Join-Path $MUTAGEN_ROOT "build/mutagen-agents.tar.gz"
    if (!(Test-Path $src)) { throw "mutagen-agents.tar.gz was NOT produced at $src" }
    Copy-Item $src (Join-Path $BUILD "mutagen-agents.tar.gz") -Force
}

# ---------- [6/6] web frontend ----------
Step -n 6 -name "web frontend (npm run build)" -output (Join-Path $BUILD "web/index.html") -srcMTime $webSrcTime -block {
    Set-Location $WEB_ROOT
    if (!(Test-Path (Join-Path $WEB_ROOT "node_modules"))) {
        Write-Host "  node_modules missing, running npm install..." -ForegroundColor Yellow
        npm install --registry=https://registry.npmmirror.com
    }
    npm run build
    $dist = Join-Path $WEB_ROOT "dist"
    if (!(Test-Path $dist)) { throw "web/dist was NOT produced" }
    $webDest = Join-Path $BUILD "web"
    if (Test-Path $webDest) { Remove-Item $webDest -Recurse -Force }
    New-Item -ItemType Directory $webDest -Force | Out-Null
    Copy-Item (Join-Path $dist "*") $webDest -Recurse -Force
}

# ---------- Copy install scripts ----------
$scriptsSrc = Join-Path $ROOT "scripts"
if (Test-Path $scriptsSrc) {
    $scriptsDest = Join-Path $BUILD "scripts"
    if (Test-Path $scriptsDest) { Remove-Item $scriptsDest -Recurse -Force }
    New-Item -ItemType Directory $scriptsDest -Force | Out-Null
    Copy-Item (Join-Path $scriptsSrc "*") $scriptsDest -Recurse -Force
    Write-Host ""
    Write-Host "=== Copied install scripts to build/scripts/ ===" -ForegroundColor Green
}

# ---------- Tests ----------
if ($script:anyBuilt) {
    Write-Host ""
    Write-Host "=== Running agent tests ===" -ForegroundColor Cyan
    Set-Location (Join-Path $ROOT "agent")
    go test ./... -count=1
    if ($LASTEXITCODE -ne 0) { throw "agent tests FAILED" }

    Write-Host ""
    Write-Host "=== Running server tests ===" -ForegroundColor Cyan
    Set-Location (Join-Path $ROOT "server")
    go test ./... -count=1
    if ($LASTEXITCODE -ne 0) { throw "server tests FAILED" }
} else {
    Write-Host ""
    Write-Host "=== No changes detected, skipping tests ===" -ForegroundColor DarkGray
}

# ---------- Summary ----------
Write-Host ""
Write-Host "============================================================" -ForegroundColor Green
Write-Host " BUILD COMPLETE" -ForegroundColor Green
Write-Host "============================================================" -ForegroundColor Green

if ($built.Count -gt 0) {
    Write-Host ""
    Write-Host " Rebuilt (need to deploy):" -ForegroundColor Yellow
    foreach ($b in $built) { Write-Host "   * $b" -ForegroundColor Yellow }
}
if ($skipped.Count -gt 0) {
    Write-Host ""
    Write-Host " Skipped (no changes):" -ForegroundColor DarkGray
    foreach ($s in $skipped) { Write-Host "   - $s" -ForegroundColor DarkGray }
}

Write-Host ""
Write-Host " Artifacts in build/:" -ForegroundColor Green
Get-ChildItem $BUILD -Recurse -Depth 1 |
    Where-Object { !$_.PSIsContainer } |
    ForEach-Object {
        $rel = $_.FullName.Substring($BUILD.Length + 1)
        $size = if ($_.Length -ge 1MB) { "{0:N1} MB" -f ($_.Length/1MB) } else { "{0:N0} B" -f $_.Length }
        Write-Host ("  {0,-42} {1,10}  {2}" -f $rel, $size, $_.LastWriteTime.ToString("yyyy/MM/dd HH:mm:ss"))
    }
Write-Host ""
Write-Host "Done." -ForegroundColor Green
