FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY mutagen-web-server_linux ./server
COPY web/ ./web/
COPY mutagen-web-agent.exe ./tools/
COPY mutagen.exe ./tools/
COPY mutagen-agents.tar.gz ./tools/

VOLUME /app/data

EXPOSE 8080

CMD ["./server", "-addr", ":8080", "-db", "/app/data/data.json", "-agent-bin", "/app/tools/mutagen-web-agent.exe"]
