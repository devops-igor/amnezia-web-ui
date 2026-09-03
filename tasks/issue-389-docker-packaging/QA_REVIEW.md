# QA Review: Phase 9 — Docker Packaging & Build Optimization (Remediated & Verified)

**Issue**: [#389](https://github.com/devops-igor/amnezia-web-ui/issues/389)  
**Target Repository**: `amnezia-web-ui` & `amnezia-web-ui-go`  
**Auditor**: `qa_bot` (Quality Gatekeeper & Adversarial Auditor)  
**Date**: 2026-09-04  
**Final Verdict**: **APPROVED**

---

## 1. Executive Summary

Phase 9 delivers the complete production containerization and orchestration packaging for the Go rewrite of Amnezia Web Panel, replacing the legacy Python multi-stage build with an optimized, non-root, minimal multi-stage Go container image, harmonizing `docker-compose.yml`, `.env.example`, and `.dockerignore`.

Following both the Senior Adversarial Code Review (`CODE_REVIEW.md`) and the Senior Verification Review (`CODE_REVIEW_FIX_VERIFICATION.md`), all initial findings and residual operational edge cases have been resolved and verified through live independent testing:
1. **Container Execution Identity & Least Privilege**: Non-root execution is enforced via `USER appuser` (UID 1000) in `Dockerfile` and `user: "1000:1000"` in `docker-compose.yml`. Dead `setcap`/`libcap` commands are removed; only `cap_add: [NET_ADMIN]` is granted alongside `cap_drop: [ALL]` and `no-new-privileges:true`.
2. **Database Writability Preflight ([HIGH-2 Residual])**: Implemented startup directory writability probes in [`internal/database/preflight.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/database/preflight.go) and integrated into `cmd/panel/main.go`, `cmd/server/main.go`, and `internal/database/database.go`. Live adversarial tests prove that unwritable directories cleanly abort startup with exit code 1 and emit the actionable remediation instruction (`sudo chown -R 1000:1000 <path>`) rather than failing with cryptic SQLite CANTOPEN or memory errors.
3. **Host Requirements & TUN-less Fallback ([HIGH-3 Residual])**: Documented `/dev/net/tun` and `cap_add: [NET_ADMIN]` in `docker-compose.yml`, explicitly detailing how operators on TUN-less unprivileged LXC/OpenVZ containers can comment out these sections when running in management-only mode (`VPN_ENABLED=false`).
4. **Environment Staging Status ([HIGH-3 Residual])**: Updated `.env.example` explicitly marking `VPN_ENDPOINT_PUBLIC_KEY` as reserved for the future in-process VPN endpoint phase and currently unused by the management panel.
5. **Toolchain Pinning & Vulnerability Elimination ([MEDIUM-4 Residual])**: Added `toolchain go1.26.6` to `amnezia-web-ui-go/go.mod`, ensuring builds use patched standard libraries. `govulncheck` confirms **0 affected vulnerabilities**.
6. **Physical Binary & Honest Alpine 3.22 Accounting ([MEDIUM-5 & HIGH-4 Residual])**: Stripped Go binary `amnezia-web-ui-go/bin/panel` physically measured on disk at **16,523,529 bytes** (~15.76 MB). Updated documentation to reflect accurate Alpine 3.22 package metrics (~7.5 MB base + ~15.5 MB runtime closure + ~15.7 MB binary = **~38.6 MB total image footprint**).
7. **Quality Gate Verification**: Independent execution of all 8 CI/CD quality gates passed with zero errors, zero data races, zero lint violations, zero security findings, and 1,130 Python unit tests passing.

---

## 2. Stage 1: Automated Gate Execution Results

All commands were executed independently by `qa_bot` on the live environment with exact wall-clock durations and verbatim logs recorded:

| Gate | Target / Scope | Command | Wall-Clock Duration | Exit Code | Gate Verdict | Output Summary |
|---|---|---|:---:|:---:|:---:|---|
| **Gate 1: Format** | `amnezia-web-ui-go` | `go fmt ./...` | 0m0.212s | 0 | **PASS** | 0 files unformatted |
| **Gate 2: Static Analysis** | `amnezia-web-ui-go` | `go vet ./...` | 0m0.275s | 0 | **PASS** | 0 warnings or issues |
| **Gate 3: Compilation** | `amnezia-web-ui-go` | `go build ./...` | 0m0.636s | 0 | **PASS** | `cmd/panel` & `cmd/server` compiled cleanly |
| **Gate 4: Race Suite** | All 29 Go packages | `go test -race -cover -count=1 ./...` | 1m03.967s | 0 | **PASS** | 29/29 packages PASS, **0 data races**, statement coverage up to 100.0% |
| **Gate 5: Linter** | `amnezia-web-ui-go` | `golangci-lint run ./...` | 0m0.747s | 0 | **PASS** | 0 lint violations |
| **Gate 6: AST Security** | `amnezia-web-ui-go` | `gosec -quiet ./...` | 0m1.185s | 0 | **PASS** | 0 security findings |
| **Gate 7: Vuln Check** | `amnezia-web-ui-go` | `govulncheck ./...` | 0m5.947s | 0 | **PASS** | **0 affected vulnerabilities** (toolchain go1.26.6) |
| **Gate 8: Python Regression** | Root test suite | `pytest -m "not e2e"` | 2m05.553s | 0 | **PASS** | **1,130 passed**, 0 failed, 36 deselected (in 119.12s) |

### Gate 4 Test Suite Detail (29 Packages under `-race`):
- `cmd/panel`: 79.1% coverage (1.722s)
- `cmd/server`: 71.6% coverage (1.193s)
- `internal/config`: 84.8% coverage (1.042s)
- `internal/database`: 89.3% coverage (6.823s)
- `internal/handlers`: 85.3% coverage (60.026s)
- `internal/manager`: 85.7% coverage (1.025s)
- `internal/manager/awg`: 86.7% coverage (1.040s)
- `internal/manager/awg/cps`: 85.6% coverage (1.022s)
- `internal/manager/awg/health`: 85.5% coverage (1.126s)
- `internal/manager/awg/tc`: 86.1% coverage (1.019s)
- `internal/manager/dns`: 88.7% coverage (1.019s)
- `internal/manager/mtproxyl`: 88.5% coverage (1.023s)
- `internal/manager/ssh`: 88.1% coverage (4.211s)
- `internal/middleware`: 81.2% coverage (1.428s)
- `internal/models`: 92.0% coverage (1.021s)
- `internal/router`: 90.6% coverage (4.723s)
- `internal/security`: 89.2% coverage (26.802s)
- `internal/service`: 93.8% coverage (1.096s)
- `internal/service/orchestrator`: 87.2% coverage (5.676s)
- `internal/service/reconciliation`: 90.1% coverage (1.815s)
- `internal/service/remnawave`: 88.2% coverage (1.288s)
- `internal/service/supervisor`: 92.9% coverage (1.328s)
- `internal/service/userops`: 86.7% coverage (1.652s)
- `internal/vpn`: 90.2% coverage (1.695s)
- `internal/vpn/endpoint`: 88.6% coverage (1.939s)
- `internal/vpn/forwarder`: 95.4% coverage (1.882s)
- `internal/vpn/loadbalancer`: 97.9% coverage (1.141s)
- `internal/vpn/tunnel`: 92.9% coverage (1.669s)
- `web`: 100.0% coverage (1.025s)

---

## 3. Stage 2: Test & Mock Fidelity Audit

### 3.1 Multi-Stage `Dockerfile` Optimization
- **Builder Stage**:
  - Image: `golang:${GO_VERSION}-alpine` (default `1.26`).
  - Caching: `go.mod` and `go.sum` cached via `go mod download` before source copy.
  - Build command: `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /build/bin/panel ./cmd/panel`.
  - Static assets embedded in binary via `//go:embed` in `web/web.go`.
- **Production Stage**:
  - Base Image: `alpine:3.22` (actively supported through May 2027).
  - Runtime packages: `ca-certificates`, `libcrypto3`, `tzdata` (0.42 MB), `iproute2`, `iptables`, `curl`. Dead `libcap` package removed.
  - Binary symlink: `/app/server -> /app/panel` avoids ~16MB duplicate binary while satisfying legacy entrypoint conventions.
  - Non-Root: Dedicated `appuser:appgroup` (UID/GID 1000).

### 3.2 Orchestration Configuration (`docker-compose.yml`)
- Non-Root: `user: "1000:1000"`.
- Ports: `${APP_PORT:-5000}:5000` (HTTP) and dynamic `"${VPN_LISTEN_PORT:-51820}:${VPN_LISTEN_PORT:-51820}/udp"`.
- Device & Capabilities: `/dev/net/tun` mapped; `cap_drop: [ALL]`, `cap_add: [NET_ADMIN]`, `no-new-privileges:true`.
- TUN-less host instructions: Documented in lines 30-35 how to comment out `devices` and `cap_add` when `VPN_ENABLED=false`.
- Filesystem: `read_only: true` container root; persistent data in `${DATA_DIR:-./data}:/app/data`; tmpfs for `/tmp` and `/var/run/amneziawg`.
- BunkerWeb integration: Preserved on `bw-net` targeting `http://amnezia-panel:5000`.

### 3.3 Configuration Environment (`.env.example`)
- Data migration documentation:
  ```bash
  sudo chown -R 1000:1000 ./data
  ```
- VPN staging status: Clarified architectural staging status of in-process subsystem.
- `VPN_ENDPOINT_PUBLIC_KEY`: Labeled as reserved for future in-process VPN endpoint phase and currently unused by management panel.

### 3.4 Physical Binary & Alpine 3.22 Footprint
- Binary target: `/home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/bin/panel` physically exists with exact byte count **16,523,529 bytes** (~15.76 MB).
- Honest image footprint:
  - Base Alpine 3.22: ~7.5 MB
  - Runtime dependencies: ~15.5 MB
  - Go binary: ~15.7 MB (16,523,529 bytes)
  - Total container footprint: **~38.6 MB** (documented in `docs/deployment.md` and `docs/plans/2026-08-25-go-rewrite.md`).

### 3.5 Deployment Documentation Alignment (`docs/deployment.md`)
- Step 2 ("Prepare Config Directory", lines 120–124) updated from legacy `appuser UID 100, GID 101` and `chown 100:101 data` to `appuser UID 1000, GID 1000` and `sudo chown -R 1000:1000 data`.
- Fully harmonized across Quick Start (line 74), `.env.example` (line 37), preflight diagnostics (`internal/database/preflight.go`), `Dockerfile`, and `docker-compose.yml`.
- Repository-wide grep scan verified zero remaining operational references to `100:101`, `UID 100`, `GID 101`, or `chown 100`.

---

## 4. Stage 3: Adversarial Correctness, Security & Failure-Mode Audit

### 4.1 Startup Writability Preflight Verification
Live adversarial test executed against an unwritable directory (`chmod 555`) simulating legacy Python directory ownership:
```
time=2026-09-04T00:00:59.937+03:00 level=ERROR msg="data directory \"/tmp/tmp.uyi5K5rL3n/unwritable_data\" is not writable by current user (UID 1000, GID 1000); if upgrading from legacy Python deployment, please run: sudo chown -R 1000:1000 \"/tmp/tmp.uyi5K5rL3n/unwritable_data\"" path=/tmp/tmp.uyi5K5rL3n/unwritable_data uid=1000 gid=1000 err="open /tmp/tmp.uyi5K5rL3n/unwritable_data/.perm_probe_1788469259937659020: permission denied"
time=2026-09-04T00:00:59.937+03:00 level=ERROR msg="Application terminated with error" err="data directory \"/tmp/tmp.uyi5K5rL3n/unwritable_data\" is not writable by current user (UID 1000, GID 1000); if upgrading from legacy Python deployment, please run: sudo chown -R 1000:1000 \"/tmp/tmp.uyi5K5rL3n/unwritable_data\": open /tmp/tmp.uyi5K5rL3n/unwritable_data/.perm_probe_1788469259937659020: permission denied"
Binary exited with code: 1
```
- Fail-fast verification: Process halts immediately at Step 3 before SQLite initializes.
- Actionable messaging: Clear guidance directing the operator to run `sudo chown -R 1000:1000 <path>`.
- Elimination of cryptic errors: SQLite CANTOPEN or memory allocation error (14) cannot be reached.

### 4.2 Healthcheck & Signal Lifecycle
- Dedicated healthcheck probe: `curl -sf http://127.0.0.1:5000/api/health || exit 1`.
- Clean non-destructive error handling: Startup/migration failure does not touch or delete existing data.
- Read-only rootfs compliance: All writes directed to `/app/data`, `/tmp`, or `/var/run/amneziawg`.

---

## 5. Adversarial Verification Checklist

| Check | Target | Status | Notes |
|---|---|:---:|---|
| **Multi-Stage Build** | `Dockerfile` | **PASS** | `golang:alpine` builder $\to$ `alpine:3.22` runtime |
| **Go Dependency Caching** | `Dockerfile` | **PASS** | `go.mod` + `go.sum` cached prior to source copy |
| **Stripped Static Binary** | `Dockerfile` & disk | **PASS** | Physically verified: 16,523,529 bytes (`-trimpath -ldflags="-s -w"`) |
| **Container Size Accounting** | Real package closure | **PASS** | Accurate Alpine 3.22 footprint ~38.6MB (~7.5MB base + ~15.5MB closure + ~15.7MB binary) |
| **Non-Root Execution** | `Dockerfile` & compose | **PASS** | `USER appuser` and `user: "1000:1000"` |
| **Capability Containment** | `docker-compose.yml` | **PASS** | `cap_drop: [ALL]`, `cap_add: [NET_ADMIN]`, `no-new-privileges:true` |
| **TUN Host Guidance** | `docker-compose.yml` | **PASS** | Documented `/dev/net/tun` requirements and fallback for TUN-less unprivileged containers |
| **Rootfs Immutability** | `docker-compose.yml` | **PASS** | `read_only: true` with volume `/app/data` & tmpfs `[/tmp, /var/run/amneziawg]` |
| **Dynamic Port Exposure** | `docker-compose.yml` | **PASS** | `5000:5000` (HTTP) and dynamic `${VPN_LISTEN_PORT:-51820}:${VPN_LISTEN_PORT:-51820}/udp` |
| **BunkerWeb Integration** | `docker-compose.yml` | **PASS** | Preserved on `bw-net` targeting `http://amnezia-panel:5000` |
| **Healthcheck Probe** | `Dockerfile` & compose | **PASS** | `curl -sf http://127.0.0.1:5000/api/health || exit 1` (verified live: HTTP 200) |
| **Data Migration Guidance** | `.env.example` | **PASS** | `sudo chown -R 1000:1000 ./data` documented for upgrade path |
| **Writability Preflight** | `internal/database` | **PASS** | Live tested: clean exit code 1 with actionable chown instruction on unwritable directory |
| **Toolchain Pinning** | `amnezia-web-ui-go/go.mod` | **PASS** | Pinned `toolchain go1.26.6`; `govulncheck` confirms 0 affected vulnerabilities |
| **Build Context Filter** | `.dockerignore` | **PASS** | Excludes legacy Python application and test artifacts |
| **Data Safety on Failure** | `internal/database` | **PASS** | Zero destructive data wipe on startup or migration abort |
| **Race Conditions** | Go test suite | **PASS** | 0 data races across all 29 packages under `-race` |
| **AST Security Scan** | `gosec` | **PASS** | 0 security findings |
| **Module Vulnerabilities** | `govulncheck` | **PASS** | 0 application/module vulnerabilities |
| **Deployment Doc Parity** | `docs/deployment.md` | **PASS** | Step 2 specifies `appuser UID 1000, GID 1000` & `sudo chown -R 1000:1000 data`; 0 legacy operational references |

---

## 6. Summary of Findings

- **CRITICAL**: 0
- **HIGH**: 0
- **MEDIUM**: 0
- **LOW**: 0
- **INFORMATIONAL**: 0

All 13 findings from `CODE_REVIEW.md` and all residual verification items from `CODE_REVIEW_FIX_VERIFICATION.md` are completely resolved and verified.

---

## 7. QA Verdict

**APPROVED**

The Phase 9 containerization and deployment packaging has been completely remediated, hardened, and verified. The application runs under least privilege (`user: "1000:1000"`), includes actionable database writability preflight diagnostics, guides operators on TUN-less environments, pins a secure Go 1.26.6 toolchain with 0 vulnerabilities, provides physically verified binary and Alpine 3.22 metrics (~38.6MB total image footprint), and passes 100% of compilation, lint, security, and race test gates.
