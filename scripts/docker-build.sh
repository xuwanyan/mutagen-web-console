#!/bin/bash
# ============================================================
# Mutagen Web Console - Linux Build & Docker Image Script
# 在 Linux 上完整构建所有产物并打包 Docker 镜像。
# 产物（位于 $ROOT/build/）：
#   mutagen-web-server_linux     Linux server (amd64)
#   mutagen-web-agent.exe        Windows agent (cross-compile)
#   mutagen.exe                  Windows mutagen CLI (-tags mutagencli)
#   mutagen-agents.tar.gz        Mutagen cross-platform agent bundle
#   web/                         前端静态文件 (npm run build)
#   scripts/                     部署脚本（含 Dockerfile）
# 用法：bash scripts/docker-build.sh
# 前置：Go 1.22+, Node.js 18+, npm, Docker
# ============================================================
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
BUILD_DIR="$PROJECT_ROOT/build"
MUTAGEN_ROOT="$PROJECT_ROOT/mutagen"
MUTAGEN_CLI="$MUTAGEN_ROOT/cmd/mutagen"
WEB_ROOT="$PROJECT_ROOT/web"

mkdir -p "$BUILD_DIR"

export GOWORK=off

# 颜色输出
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
GREEN='\033[0;32m'
DARK='\033[2;37m'
RED='\033[0;31m'
NC='\033[0m'

# ---------- 环境检查 ----------
echo -e "${CYAN}=== 检查构建环境 ===${NC}"
for cmd in go npm docker; do
    if ! command -v $cmd >/dev/null 2>&1; then
        echo -e "${RED}错误: 未找到 $cmd，请先安装${NC}"
        exit 1
    fi
done
echo "  go:      $(go version)"
echo "  node:    $(node --version)"
echo "  npm:     $(npm --version)"
echo "  docker:  $(docker --version)"
echo ""

# ---------- helper: 取目录下指定后缀文件的最新 mtime（epoch 秒）----------
latest_mtime() {
    local dir="$1"
    shift
    local max=0
    # 遍历所有指定的 glob 模式
    while IFS= read -r -d '' f; do
        local t
        t=$(stat -c %Y "$f" 2>/dev/null || echo 0)
        if [ "$t" -gt "$max" ]; then max=$t; fi
    done < <(find "$dir" -type f \( "$@" \) -print0 2>/dev/null)
    # 检查 go.mod/go.sum/package.json/package-lock.json
    for extra in go.mod go.sum package.json package-lock.json; do
        if [ -f "$dir/$extra" ]; then
            local t
            t=$(stat -c %Y "$dir/$extra")
            if [ "$t" -gt "$max" ]; then max=$t; fi
        fi
    done
    echo "$max"
}

need_build() {
    # 返回 0=需要构建，1=跳过
    local output="$1"
    local src_mtime="$2"
    if [ ! -f "$output" ]; then return 0; fi
    local out_mtime
    out_mtime=$(stat -c %Y "$output")
    if [ "$src_mtime" -gt "$out_mtime" ]; then return 0; fi
    return 1
}

BUILT=""
SKIPPED=""
ANY_BUILT=0

step() {
    local n="$1"
    local name="$2"
    local output="$3"
    local src_mtime="$4"
    shift 4
    echo ""
    if need_build "$output" "$src_mtime"; then
        echo -e "${YELLOW}=== [$n/6] $name ===${NC}"
        echo -e "${CYAN}  source changed, rebuilding...${NC}"
        "$@" || { echo -e "${RED}Step [$n/6] $name FAILED${NC}"; exit 1; }
        ANY_BUILT=1
        BUILT="$BUILT\n  * $name"
    else
        echo -e "${DARK}=== [$n/6] $name ===${NC}"
        echo -e "${DARK}  skip (no changes since last build)${NC}"
        SKIPPED="$SKIPPED\n  - $name"
    fi
}

# ---------- 计算源码 mtime ----------
SERVER_SRC_TIME=$(latest_mtime "$PROJECT_ROOT/server" -name '*.go')
AGENT_SRC_TIME=$(latest_mtime "$PROJECT_ROOT/agent" -name '*.go')
MUTAGEN_SRC_TIME=$(latest_mtime "$MUTAGEN_ROOT" -name '*.go' -o -name '*.s')
WEB_SRC_TIME=$(latest_mtime "$WEB_ROOT" -name '*.vue' -o -name '*.js' -o -name '*.ts' -o -name '*.css' -o -name '*.json' -o -name '*.html')

# ---------- [1/6] mutagen-web-server_linux (Linux amd64) ----------
build_server_linux() {
    cd "$PROJECT_ROOT/server"
    GOOS=linux GOARCH=amd64 go build -a -o "$BUILD_DIR/mutagen-web-server_linux" .
}
step 1 "mutagen-web-server_linux (Linux amd64)" "$BUILD_DIR/mutagen-web-server_linux" "$SERVER_SRC_TIME" build_server_linux

# ---------- [2/6] mutagen-web-agent.exe (Windows, cross-compile) ----------
build_agent() {
    cd "$PROJECT_ROOT/agent"
    GOOS=windows GOARCH=amd64 go build -o "$BUILD_DIR/mutagen-web-agent.exe" .
}
step 2 "mutagen-web-agent.exe (Windows)" "$BUILD_DIR/mutagen-web-agent.exe" "$AGENT_SRC_TIME" build_agent

# ---------- [3/6] mutagen.exe (Windows, from forked ./mutagen) ----------
build_mutagen_cli() {
    cd "$MUTAGEN_CLI"
    GOOS=windows GOARCH=amd64 go build -tags mutagencli -o "$BUILD_DIR/mutagen.exe" .
}
step 3 "mutagen.exe (from mutagen/cmd/mutagen, -tags mutagencli)" "$BUILD_DIR/mutagen.exe" "$MUTAGEN_SRC_TIME" build_mutagen_cli

# ---------- [4/6] mutagen-agents.tar.gz (from forked ./mutagen) ----------
build_agents_tar() {
    cd "$MUTAGEN_ROOT"
    go run scripts/build.go
    local src="$MUTAGEN_ROOT/build/mutagen-agents.tar.gz"
    if [ ! -f "$src" ]; then
        echo -e "${RED}mutagen-agents.tar.gz was NOT produced at $src${NC}"
        exit 1
    fi
    cp "$src" "$BUILD_DIR/mutagen-agents.tar.gz"
}
step 4 "mutagen-agents.tar.gz (mutagen/scripts/build.go)" "$BUILD_DIR/mutagen-agents.tar.gz" "$MUTAGEN_SRC_TIME" build_agents_tar

# ---------- [5/6] web frontend ----------
build_web() {
    cd "$WEB_ROOT"
    if [ ! -d "$WEB_ROOT/node_modules" ]; then
        echo -e "${YELLOW}  node_modules missing, running npm install...${NC}"
        npm install --registry=https://registry.npmmirror.com
    fi
    npm run build
    if [ ! -d "$WEB_ROOT/dist" ]; then
        echo -e "${RED}web/dist was NOT produced${NC}"
        exit 1
    fi
    rm -rf "$BUILD_DIR/web"
    mkdir -p "$BUILD_DIR/web"
    cp -r "$WEB_ROOT/dist/." "$BUILD_DIR/web/"
}
step 5 "web frontend (npm run build)" "$BUILD_DIR/web/index.html" "$WEB_SRC_TIME" build_web

# ---------- [6/6] copy scripts ----------
copy_scripts() {
    rm -rf "$BUILD_DIR/scripts"
    mkdir -p "$BUILD_DIR/scripts"
    cp -r "$PROJECT_ROOT/scripts/." "$BUILD_DIR/scripts/"
    echo -e "${GREEN}  copied install scripts to build/scripts/${NC}"
}
echo ""
echo -e "${YELLOW}=== [6/6] copy install scripts ===${NC}"
copy_scripts

# ---------- Tests ----------
if [ "$ANY_BUILT" -eq 1 ]; then
    echo ""
    echo -e "${CYAN}=== Running agent tests ===${NC}"
    cd "$PROJECT_ROOT/agent"
    go test ./... -count=1 || { echo -e "${RED}agent tests FAILED${NC}"; exit 1; }

    echo ""
    echo -e "${CYAN}=== Running server tests ===${NC}"
    cd "$PROJECT_ROOT/server"
    go test ./... -count=1 || { echo -e "${RED}server tests FAILED${NC}"; exit 1; }
else
    echo ""
    echo -e "${DARK}=== No changes detected, skipping tests ===${NC}"
fi

# ---------- Build Docker image ----------
echo ""
echo -e "${CYAN}=== Building Docker image ===${NC}"
docker build -t mutagen-web -f "$SCRIPT_DIR/Dockerfile" "$BUILD_DIR" || {
    echo -e "${RED}Docker build FAILED${NC}"
    exit 1
}

# ---------- Summary ----------
echo ""
echo -e "${GREEN}============================================================${NC}"
echo -e "${GREEN} BUILD COMPLETE${NC}"
echo -e "${GREEN}============================================================${NC}"
if [ -n "$BUILT" ]; then
    echo ""
    echo -e "${YELLOW} Rebuilt:${NC}"
    echo -e "$BUILT"
fi
if [ -n "$SKIPPED" ]; then
    echo ""
    echo -e "${DARK} Skipped (no changes):${NC}"
    echo -e "$SKIPPED"
fi
echo ""
echo -e "${GREEN} Docker image: mutagen-web${NC}"
echo -e "${GREEN} Artifacts in build/:${NC}"
find "$BUILD_DIR" -maxdepth 2 -type f -printf "  %P  %s bytes  %TY-%Tm-%Td %TH:%TM:%TS\n" | sort
echo ""
echo -e "${GREEN}Done.${NC}"
echo ""
echo -e "${CYAN}运行容器：${NC}"
echo "  docker run -d \\"
echo "    --name mutagen-web \\"
echo "    --restart unless-stopped \\"
echo "    -p 8080:8080 \\"
echo "    -v /opt/mutagen-web/data:/app/data \\"
echo "    mutagen-web"
