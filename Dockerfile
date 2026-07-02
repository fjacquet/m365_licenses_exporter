# Stage 1: Build
FROM golang:1.26.4 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static build.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o licenses_exporter .

# Stage 2: Runtime
FROM alpine:latest

# Create the runtime user and log dir. These are busybox builtins (no network).
RUN adduser -D -u 10001 licenses && \
    mkdir -p /var/log/licenses_exporter && \
    chown licenses:licenses /var/log/licenses_exporter

# Copy the CA bundle from the builder stage instead of `apk add ca-certificates`.
# The latter fetches from the Alpine CDN over TLS, which fails behind a corporate
# MITM proxy: the bare alpine image has no CA bundle yet to validate the proxy
# cert (chicken-and-egg). The Debian-based golang builder already ships the bundle.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

COPY --from=builder /app/licenses_exporter /usr/bin/licenses_exporter
COPY config.yaml /etc/licenses_exporter/config.yaml

EXPOSE 9105

USER licenses

ENTRYPOINT ["/usr/bin/licenses_exporter"]
CMD ["--config", "/etc/licenses_exporter/config.yaml"]
