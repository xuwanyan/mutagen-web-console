#!/bin/bash
# 构建 mutagen-web Docker 镜像
# 用法：在项目根目录执行 bash scripts/docker-build.sh
# 前提：build/ 目录下已有 build.ps1 生成的产物

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
BUILD_DIR="$PROJECT_ROOT/build"

# 检查必要文件
for f in mutagen-web-server_linux mutagen-web-agent.exe mutagen.exe mutagen-agents.tar.gz; do
  if [ ! -f "$BUILD_DIR/$f" ]; then
    echo "错误: $BUILD_DIR/$f 不存在，请先在 Windows 上运行 build.ps1 生成构建产物"
    exit 1
  fi
done

if [ ! -d "$BUILD_DIR/web" ]; then
  echo "错误: $BUILD_DIR/web/ 不存在，请先在 Windows 上运行 build.ps1 生成前端产物"
  exit 1
fi

echo "开始构建 mutagen-web 镜像..."
docker build -t mutagen-web -f "$SCRIPT_DIR/Dockerfile" "$BUILD_DIR"

echo "构建完成。"
echo "运行：docker run -d -p 8080:8080 -v mutagen-web-data:/app/data mutagen-web"
