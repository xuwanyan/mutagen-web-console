# Server 部署到 Linux，客户端（Agent）部署到 Windows
# 构建：docker build -t mutagen-web -f scripts/Dockerfile build/
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# web-console/build/ 目录下需包含：
#   mutagen-web-server_linux  - Linux server 二进制
#   web/                      - 前端静态文件
COPY mutagen-web-server_linux ./server
COPY web/ ./web/

VOLUME /app/data

EXPOSE 18080

CMD ["./server", "-addr", ":18080", "-db", "/app/data/data.json"]
