# Server 部署到 Linux
# 构建：docker build -t mutagen-web -f scripts/Dockerfile build/
FROM ubuntu:22.04
RUN sed -i 's/archive.ubuntu.com/mirrors.ustc.edu.cn/g' /etc/apt/sources.list \
    && sed -i 's/security.ubuntu.com/mirrors.ustc.edu.cn/g' /etc/apt/sources.list
RUN apt update && apt install -y ca-certificates tzdata && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Server 二进制
COPY mutagen-web-server_linux ./server

# 前端静态文件
COPY web/ ./web/

# Agent 安装包所需的文件（供网页下载安装包使用）
COPY mutagen-web-agent.exe ./tools/
COPY mutagen.exe ./tools/
COPY mutagen-agents.tar.gz ./tools/

VOLUME /app/data

EXPOSE 8080

CMD ["./server", "-addr", ":8080", "-db", "/app/data/data.json", "-agent-bin", "/app/tools/mutagen-web-agent.exe"]