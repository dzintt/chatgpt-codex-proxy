# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.0

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/chatgpt-codex-proxy ./cmd/api

FROM alpine:3.22
WORKDIR /app

RUN apk add --no-cache ca-certificates && \
    addgroup -S app && \
    adduser -S -G app -h /app app && \
    mkdir -p /app/data && \
    chown -R app:app /app

COPY --from=build /out/chatgpt-codex-proxy /usr/local/bin/chatgpt-codex-proxy
RUN chown app:app /usr/local/bin/chatgpt-codex-proxy

ENV PORT=8080
ENV DATA_DIR=/app/data
ENV GIN_MODE=release

EXPOSE 8080
VOLUME ["/app/data"]
USER app

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O - "http://127.0.0.1:${PORT}/health/live" >/dev/null || exit 1

ENTRYPOINT ["chatgpt-codex-proxy"]
