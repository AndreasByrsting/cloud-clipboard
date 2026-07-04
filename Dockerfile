# syntax=docker/dockerfile:1

FROM golang:1.26-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X 'cloud-clipboard/version.Version=${VERSION}' -X 'cloud-clipboard/version.Commit=${COMMIT}' -X 'cloud-clipboard/version.BuildTime=${BUILD_TIME}'" \
    -o /out/cloud-clipboard .

FROM debian:bookworm-slim
WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
RUN groupadd -g 1000 app && useradd -u 1000 -g 1000 -M -d /app -s /usr/sbin/nologin app
RUN mkdir -p /data/uploads && chown -R 1000:1000 /app /data

COPY --from=builder /out/cloud-clipboard /usr/local/bin/cloud-clipboard

ENV APP_LISTEN_ADDR=:8080
ENV APP_DB_PATH=/data/clipboard.db
ENV APP_UPLOAD_DIR=/data/uploads

VOLUME ["/data"]
EXPOSE 8080

USER 1000:1000
ENTRYPOINT ["/usr/local/bin/cloud-clipboard"]
