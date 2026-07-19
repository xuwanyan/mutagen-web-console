FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# 构建时需要把 build/ 目录作为上下文传入
# docker build -t mutagen-web -f scripts/Dockerfile build/
COPY mutagen-web-server_linux ./server
COPY web/ ./web/

VOLUME /app/data

EXPOSE 18080

CMD ["./server", "-addr", ":18080", "-db", "/app/data/data.json"]
