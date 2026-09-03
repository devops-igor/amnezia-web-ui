# Task: Phase 9 — Docker Packaging & Build Optimization (Issue #389)

## 1. Overview & Objectives
The goal of Phase 9 is to implement the containerization layer for the Go rewrite of Amnezia Web Panel, replacing the legacy Python multi-stage build with an optimized, secure, and production-ready multi-stage Go container image and updating the orchestration configurations (`docker-compose.yml`, `.env.example`).

### Primary Specifications
- [`docs/plans/2026-08-25-go-rewrite.md`](../../docs/plans/2026-08-25-go-rewrite.md) (§3 Phase 9)
- [`docs/specs/02-configuration.md`](../../docs/specs/02-configuration.md) (§1.1 Static Configuration)
- [`docs/specs/04-external-services.md`](../../docs/specs/04-external-services.md) (§2.1 Container Specifications)

---

## 2. Requirements & Deliverables

### 2.1 Multi-Stage Go `Dockerfile`
1. **Build Stage**:
   - Base image: `golang:1.22-alpine` (or matching Go toolchain version).
   - Install build tools/dependencies if necessary (e.g., `git`, `ca-certificates`).
   - Copy `amnezia-web-ui-go/` module files (`go.mod`, `go.sum`) first for efficient layer caching (`go mod download`).
   - Build statically linked binaries with `CGO_ENABLED=0` and stripped symbols (`-ldflags="-s -w"`):
     - `cmd/panel` (production server binary)
     - `cmd/server` (if packaged or symlinked)
2. **Production Stage**:
   - Base image: `alpine:3.19` (or latest stable Alpine).
   - Minimal runtime dependencies: `ca-certificates`, `tzdata`, `iproute2`, `iptables` (for TUN/routing operations when `VPN_ENABLED=true`), and `curl` (for healthcheck probe).
   - Target image size: **< 30MB**.
   - Create directories:
     - `/app/data` (for SQLite database `panel.db`, `.secret_key`, backups)
     - `/var/run/amneziawg/` (for userspace AWG control sockets)
     - `/app/` for binaries and assets if any external files needed (remember Go embeds templates and static assets).
   - Expose ports:
     - `5000/tcp` (HTTP Web Panel & API)
     - `51820/udp` (AmneziaWG VPN Endpoint listener)
   - Healthcheck:
     - Configure `HEALTHCHECK` probe targeting HTTP `/` or `/api/auth/captcha` (e.g. `curl -f http://127.0.0.1:5000/ || exit 1`).
   - Entrypoint/Cmd:
     - Run `/app/panel` as the default command.

### 2.2 Container Security & Capabilities Model
1. **Capabilities & Device Access**:
   - Container requires `CAP_NET_ADMIN` capability and access to `/dev/net/tun` for in-process AWG user tunnels.
   - Must NOT require `--privileged` container mode.
   - Support `read_only: true` container root filesystem where runtime writes are directed to mounted volumes (`/app/data`) and tmpfs (`/tmp`, `/var/run/amneziawg`).
2. **User & Permissions**:
   - Support running as `appuser` (with `CAP_NET_ADMIN` granted via `setcap` on binary or appropriate group/capability mapping) or root with dropped unnecessary capabilities.

### 2.3 `docker-compose.yml` Updates
1. **Panel Service (`amnezia-panel`)**:
   - Image tag: `${AMNEZIA_IMAGE:-ghcr.io/devops-igor/amnezia-web-ui:latest}`.
   - Ports:
     - `${APP_PORT:-5000}:5000` (HTTP)
     - `${VPN_LISTEN_PORT:-51820}:51820/udp` (AmneziaWG VPN Endpoint UDP)
   - Devices:
     - `/dev/net/tun:/dev/net/tun`
   - Capabilities:
     - `cap_add: [NET_ADMIN]`
   - Volumes & Tmpfs:
     - `${DATA_DIR:-./data}:/app/data`
     - `tmpfs: [/tmp, /var/run/amneziawg]`
   - Environment:
     - `SECRET_KEY`, `TRUSTED_PROXIES`, `DATA_DIR`
     - `VPN_ENABLED=${VPN_ENABLED:-false}`
     - `VPN_LISTEN_PORT=${VPN_LISTEN_PORT:-51820}`
     - `VPN_SUBNET=${VPN_SUBNET:-10.100.0.0/16}`
     - `PORT=5000`
   - Healthcheck:
     - `test: ["CMD-SHELL", "curl -sf http://127.0.0.1:5000/ || exit 1"]`
     - `interval: 30s`, `timeout: 10s`, `retries: 3`, `start_period: 20s`
2. **BunkerWeb Service & Docker Proxy Integration**:
   - Preserve all existing BunkerWeb reverse proxy labels, multisite configuration, Let's Encrypt support, and docker-proxy socket restrictions.

### 2.4 `.env.example` Updates
1. Document all newly added environment variables:
   - `VPN_ENABLED`: Enable in-process AmneziaWG VPN endpoint & load balancing subsystem (`true`/`false`, default `false`).
   - `VPN_LISTEN_PORT`: External UDP port for incoming client AmneziaWG tunnels (default `51820`).
   - `VPN_SUBNET`: Internal IPAM CIDR pool for connected clients (default `10.100.0.0/16`).
   - `VPN_ENDPOINT_PUBLIC_KEY`: Optional public key override / display metadata.
2. Update documentation and comments reflecting the Go binary architecture.

### 2.5 Build & Verification Scripts / Makefile
1. Update root and/or `amnezia-web-ui-go/Makefile` to include docker build/test targets if beneficial (`docker-build`, `docker-run`, etc.).
2. Ensure backward compatibility and no regressions in existing unit and regression test suites.

---

## 3. Hard Compilation & Quality Gates

The developer (`dev_bot`) MUST verify:
1. `cd amnezia-web-ui-go && go fmt ./... && go vet ./... && go build ./... && go test -race -cover -count=1 ./...`
2. `golangci-lint run ./...`
3. `gosec -quiet ./...`
4. `govulncheck ./...`
5. Full Python regression suite (`pytest --ignore=tests/e2e -q`) to ensure zero repo-level regressions.
6. Docker build verification: test `docker build` (or syntax / lint validation) for the new multi-stage `Dockerfile`.

---

## 4. Handoff Deliverable
`dev_bot` must emit `tasks/issue-389-docker-packaging/DEV_HANDOVER.md` containing:
- File change manifest
- Verbatim terminal outputs of all compilation and test gates
- Docker build test output and image size measurement
- Notes for `qa_bot` auditing
