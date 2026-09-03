# Sub-Task: Phase 9 Docker Packaging & Security Hardening Fixes (`docker_packaging_fixes.md`)

## 1. Context & Objectives
During the Senior Adversarial Code Review (`CODE_REVIEW.md`), several security, configuration, and documentation defects were identified in the Phase 9 implementation. This sub-task specifies the required remediations to make the containerization layer production-grade, secure, and accurate.

---

## 2. Required Remediation Items

### 2.1 Restore Non-Root Execution & Clean Capabilities ([HIGH-1], [HIGH-2], [MEDIUM-2])
1. **`Dockerfile`**:
   - Add `USER appuser` at the end of the production stage.
   - Remove `apk add/del libcap` and `setcap cap_net_admin=+ep /app/panel` (dead code rendered ineffective by subsequent `chown` and `no-new-privileges:true`).
   - Ensure `/app/data` and `/var/run/amneziawg` directories are owned by `appuser:appgroup` (`1000:1000`).
2. **`docker-compose.yml`**:
   - Add `user: "1000:1000"`.
   - Retain `cap_drop: [ALL]` and `cap_add: [NET_ADMIN]`.
3. **Data Ownership Migration**:
   - Document in `.env.example` and comments that existing deployments upgrading from Python (UID 100) must run `chown -R 1000:1000 ./data` (or add non-fatal startup warning if permission issues occur).

### 2.2 Alpine Base Image Upgrade ([MEDIUM-3])
1. **`Dockerfile`**:
   - Upgrade production base image from EOL `alpine:3.19` to `alpine:3.22`.

### 2.3 Healthcheck Endpoint Hardening ([LOW-1])
1. **`Dockerfile` & `docker-compose.yml`**:
   - Update healthcheck command to probe `/api/health` directly:
     `CMD-SHELL curl -sf http://127.0.0.1:5000/api/health || exit 1`
   - This ensures the probe checks the dedicated JSON health endpoint (returning 200 OK) without coupling to auth redirect status codes.

### 2.4 VPN Configuration & Port Mapping Alignment ([HIGH-3], [MEDIUM-1], [INFO-1])
1. **`docker-compose.yml`**:
   - Fix UDP port mapping so container-side port dynamically matches `VPN_LISTEN_PORT`:
     `"${VPN_LISTEN_PORT:-51820}:${VPN_LISTEN_PORT:-51820}/udp"`
2. **`.env.example`**:
   - Clarify documentation: indicate that the in-process AWG VPN endpoint subsystem is currently in architectural staging (in-process forwarder/IPAM foundation implemented, live TUN lifecycle wired in subsequent phases).

### 2.5 `.dockerignore` Optimization ([LOW-2])
1. Update `.dockerignore` to exclude legacy Python directories (`app/`, `static/`, `templates/`, `translations/`, `tests/`, `*.py`, `requirements.txt`) so build context is kept minimal.

### 2.6 Toolchain & Gate Verification ([HIGH-4], [MEDIUM-4], [MEDIUM-5])
1. Run all gates with honest execution and record verbatim outputs:
   - `cd amnezia-web-ui-go && go fmt ./... && go vet ./... && go build ./... && go test -race -cover -count=1 ./...`
   - `golangci-lint run ./...`
   - `gosec -quiet ./...`
   - `govulncheck ./...`
   - `pytest --ignore=tests/e2e -q`
2. Report the honest image size projection (~41MB) reflecting all included networking tools.

---

## 3. Output Deliverable
`dev_bot` MUST emit its handover report strictly to:
`tasks/issue-389-docker-packaging/docker_packaging_fixes_dev_handover.md`
