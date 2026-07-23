# Stage 1: Build
FROM golang:1.26.5 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static build.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o m365_licenses_exporter .

# Stage 2: Runtime
FROM alpine:latest

# Create the runtime user and log dir. These are busybox builtins (no network).
RUN adduser -D -u 10001 licenses && \
    mkdir -p /var/log/m365_licenses_exporter && \
    chown licenses:licenses /var/log/m365_licenses_exporter

# Copy the CA bundle from the builder stage instead of `apk add ca-certificates`.
# The latter fetches from the Alpine CDN over TLS, which fails behind a corporate
# MITM proxy: the bare alpine image has no CA bundle yet to validate the proxy
# cert (chicken-and-egg). The Debian-based golang builder already ships the bundle.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

COPY --from=builder /app/m365_licenses_exporter /usr/bin/m365_licenses_exporter
COPY config.yaml /etc/m365_licenses_exporter/config.yaml

EXPOSE 9105

USER licenses

ENTRYPOINT ["/usr/bin/m365_licenses_exporter"]
CMD ["--config", "/etc/m365_licenses_exporter/config.yaml"]
