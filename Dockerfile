# =============================================================================
# Stage 1: Build Stage (Statically compiled Go binaries)
# =============================================================================
ARG GO_VERSION=1.26
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache ca-certificates git

# Cache Go modules first for faster incremental builds
COPY amnezia-web-ui-go/go.mod amnezia-web-ui-go/go.sum ./
RUN go mod download

# Copy Go codebase
COPY amnezia-web-ui-go/ .

# Build stripped statically linked production binary with CGO disabled
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /build/bin/panel ./cmd/panel

# =============================================================================
# Stage 2: Production Stage (Lean Alpine runtime ~41MB)
# =============================================================================
FROM alpine:3.22

WORKDIR /app

# Install minimal runtime dependencies:
# - ca-certificates: TLS verification for RemnaWave API & remote HTTPS
# - tzdata: accurate timezone support
# - iproute2: IP & interface management for TUN devices (ip link, ip addr)
# - iptables: NAT / packet forwarding rules for VPN endpoint
# - curl: HTTP healthcheck probe
RUN apk add --no-cache \
        ca-certificates \
        tzdata \
        iproute2 \
        iptables \
        curl && \
    addgroup -S -g 1000 appgroup && \
    adduser -S -u 1000 -G appgroup appuser

# Copy panel binary from builder stage and create server symlink
COPY --from=builder /build/bin/panel /app/panel
RUN ln -s /app/panel /app/server

# Setup runtime directories and permissions:
# - /app/data: persistent SQLite database (panel.db), keyfile (.secret_key), backups
# - /var/run/amneziawg: userspace AWG control sockets
# - /dev/net: TUN device directory mount point
RUN mkdir -p /app/data /var/run/amneziawg /dev/net && \
    chown -R appuser:appgroup /app /var/run/amneziawg

# Expose ports: 5000 (HTTP Web Panel & API), 51820/udp (AmneziaWG VPN Endpoint)
EXPOSE 5000/tcp
EXPOSE 51820/udp

# Healthcheck probe verifying HTTP responsiveness via dedicated health endpoint
HEALTHCHECK --interval=30s --timeout=10s --retries=3 --start-period=20s \
    CMD curl -sf http://127.0.0.1:5000/api/health || exit 1

# Run as non-root user (capabilities granted at container level via compose cap_add)
USER appuser

# Default command: launch Amnezia Web Panel
CMD ["/app/panel"]

