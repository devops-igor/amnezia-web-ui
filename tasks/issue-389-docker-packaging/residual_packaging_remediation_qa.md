# QA Adversarial Audit Report: Phase 9 Residual Packaging & Preflight Remediation

**Issue**: [#389](https://github.com/devops-igor/amnezia-web-ui/issues/389)  
**Task Specification**: `tasks/issue-389-docker-packaging/residual_packaging_remediation.md`  
**Reference Code Review**: `tasks/issue-389-docker-packaging/CODE_REVIEW_FIX_VERIFICATION.md`  
**Developer Handover**: `tasks/issue-389-docker-packaging/residual_packaging_remediation_dev_handover.md`  
**Auditor**: `qa_bot` (Quality Gatekeeper & Adversarial Auditor)  
**Audit Timestamp**: 2026-09-04T00:03:00+03:00  
**Verdict**: **APPROVED**

---

## 1. Executive Summary

Following the Senior Verification Review (`CODE_REVIEW_FIX_VERIFICATION.md`), residual operational edge cases, preflight diagnostics, configuration documentation gaps, toolchain pinning, on-disk binary measurement, and gate evidence artifacts required remediation.

`qa_bot` has conducted an independent, live 3-stage adversarial audit across the repository:
1. **Automated Gate Execution (Live & Independent)**:
   - All 8 CI/CD quality gates were executed serially with exact wall-clock durations and verbatim logs captured.
   - 0 formatting issues, 0 compiler warnings, clean compilation, 0 data races across all 29 Go packages under `-race`, 0 linter violations, 0 AST security findings, **0 affected vulnerabilities** under Go 1.26.6 toolchain via `govulncheck`, and **1,130 unit tests passed** with 0 failures under `pytest -m "not e2e"`.
2. **Test & Configuration Fidelity Audit**:
   - **Database Writability Preflight ([HIGH-2 Residual])**: Audited `internal/database/preflight.go` (`CheckDirWritable`, `CheckPreflight`, `ActionableWritabilityError`) and verified its wiring into `cmd/panel/main.go`, `cmd/server/main.go`, and `internal/database/database.go`. Live execution against an unwritable directory proved that startup is immediately halted with exit code 1 and logs the actionable remediation instruction:
     `"data directory %q is not writable by current user (UID 1000, GID 1000); if upgrading from legacy Python deployment, please run: sudo chown -R 1000:1000 %q"`.
     No cryptic SQLite out-of-memory/CANTOPEN errors reach the operator.
   - **TUN Host Requirements & TUN-less Fallback ([HIGH-3 Residual])**: Verified inline instructions in `docker-compose.yml` (lines 30-35) explaining `/dev/net/tun` and `cap_add: [NET_ADMIN]` requirements and how to comment them out on TUN-less VPS/unprivileged containers when running `VPN_ENABLED=false`.
   - **Environment Staging Status ([HIGH-3 Residual])**: Verified `.env.example` (lines 73-76) explicitly marks `VPN_ENDPOINT_PUBLIC_KEY` as reserved for the future in-process VPN endpoint phase and currently unused by the management panel.
   - **Toolchain Pinning ([MEDIUM-4 Residual])**: Verified `amnezia-web-ui-go/go.mod` line 5 pins `toolchain go1.26.6`, ensuring patched stdlib packages and resolving reachable vulnerabilities (`govulncheck`: 0 vulnerabilities affecting code).
   - **On-Disk Production Binary ([MEDIUM-5 & HIGH-4 Residual])**: Physically inspected `/home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/bin/panel` on disk; exact size is **16,523,529 bytes** (~15.76 MB).
   - **Alpine 3.22 Accurate Metrics Accounting ([MEDIUM-5 & HIGH-4 Residual])**: Verified updated breakdown across `docs/deployment.md` and `docs/plans/2026-08-25-go-rewrite.md` reflecting Alpine 3.22 APKINDEX numbers: ~7.5 MB base + ~15.5 MB runtime closure + ~15.7 MB binary = **~38.6 MB total footprint**.
3. **Adversarial Failure-Mode Audit**:
   - Verified that permission denial during startup produces clean, actionable stderr/stdout diagnostics, zero corrupted files, and instant non-zero exit.
   - Verified that temporary probe files (`.perm_probe_<nanos>`) are cleanly unlinked on success.

---

## 2. Stage 1: Automated Gate Execution Results

All gates were executed independently by `qa_bot` on the live environment. Wall-clock timing and command outputs were recorded directly:

| Gate | Target / Scope | Command | Wall-Clock Duration | Exit Code | Gate Verdict | Output Summary |
|---|---|---|:---:|:---:|:---:|---|
| **Gate 1: Format** | `amnezia-web-ui-go` | `go fmt ./...` | 0m0.212s | 0 | **PASS** | 0 files unformatted |
| **Gate 2: Static Analysis** | `amnezia-web-ui-go` | `go vet ./...` | 0m0.275s | 0 | **PASS** | 0 warnings or issues |
| **Gate 3: Compilation** | `amnezia-web-ui-go` | `go build ./...` | 0m0.636s | 0 | **PASS** | `cmd/panel` & `cmd/server` compiled cleanly |
| **Gate 4: Race Suite** | All 29 Go packages | `go test -race -cover -count=1 ./...` | 1m03.967s | 0 | **PASS** | 29/29 packages PASS, **0 data races**, coverage up to 100.0% |
| **Gate 5: Linter** | `amnezia-web-ui-go` | `golangci-lint run ./...` | 0m0.747s | 0 | **PASS** | 0 lint violations |
| **Gate 6: AST Security** | `amnezia-web-ui-go` | `gosec -quiet ./...` | 0m1.185s | 0 | **PASS** | 0 security findings |
| **Gate 7: Vuln Check** | `amnezia-web-ui-go` | `govulncheck ./...` | 0m5.947s | 0 | **PASS** | **0 affected vulnerabilities** (toolchain go1.26.6) |
| **Gate 8: Python Regression** | Root test suite | `pytest -m "not e2e"` | 2m05.553s | 0 | **PASS** | **1,130 passed**, 0 failed, 36 deselected (in 119.12s) |

### Gate 4 Detailed Package Breakdown (All 29 Packages):
```
ok   github.com/devops-igor/amnezia-web-ui-go/cmd/panel                 1.722s  coverage: 79.1% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/cmd/server                1.193s  coverage: 71.6% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/config           1.042s  coverage: 84.8% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/database         6.823s  coverage: 89.3% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/handlers         60.026s coverage: 85.3% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/manager          1.025s  coverage: 85.7% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg      1.040s  coverage: 86.7% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/cps  1.022s  coverage: 85.6% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/health 1.126s coverage: 85.5% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/tc   1.019s  coverage: 86.1% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/manager/dns      1.019s  coverage: 88.7% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/manager/mtproxyl 1.023s  coverage: 88.5% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh      4.211s  coverage: 88.1% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/middleware       1.428s  coverage: 81.2% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/models           1.021s  coverage: 92.0% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/router           4.723s  coverage: 90.6% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/security         26.802s coverage: 89.2% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/service          1.096s  coverage: 93.8% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/service/orchestrator 5.676s coverage: 87.2% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/service/reconciliation 1.815s coverage: 90.1% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/service/remnawave 1.288s  coverage: 88.2% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/service/supervisor 1.328s coverage: 92.9% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/service/userops  1.652s  coverage: 86.7% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/vpn              1.695s  coverage: 90.2% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/vpn/endpoint     1.939s  coverage: 88.6% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/vpn/forwarder    1.882s  coverage: 95.4% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/vpn/loadbalancer 1.141s  coverage: 97.9% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/internal/vpn/tunnel       1.669s  coverage: 92.9% of statements
ok   github.com/devops-igor/amnezia-web-ui-go/web                       1.025s  coverage: 100.0% of statements
```
*Total wall-clock: 1m03.967s (real 1m03.967s, user 1m47.786s, sys 0m05.177s). Longest single package: `internal/handlers` (60.026s).*

---

## 3. Stage 2: Test & Configuration Fidelity Audit

### 3.1 Database Writability Preflight Implementation & Live Test
1. **Probe Logic** ([`amnezia-web-ui-go/internal/database/preflight.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/database/preflight.go)):
   - `CheckDirWritable(dir)` normalizes path, ensures directory existence with `0750` permissions, opens a temporary probe file `.perm_probe_<nanos>` with `O_RDWR|O_CREATE|O_EXCL` and mode `0600`, writes `"probe"`, flushes, closes, and unlinks the probe file.
   - On permission failure or directory creation error, logs with `slog.Error`:
     `data directory %q is not writable by current user (UID %d, GID %d); if upgrading from legacy Python deployment, please run: sudo chown -R 1000:1000 %q`
     and wraps the underlying OS error.
   - `CheckPreflight(dataDir, dbPath)` probes both `dataDir` and `filepath.Dir(dbPath)`.
2. **Wiring Points**:
   - `cmd/panel/main.go:60`: Runs before translations, before `MigrateFromDataJSON`, and before `database.New`.
   - `cmd/server/main.go:60`: Runs before translations, before `MigrateFromDataJSON`, and before `database.New`.
   - `internal/database/database.go:126`: `Open()` probes non-memory database directory before establishing connection pool.
3. **Live Adversarial Test**:
   - Created a read-only directory (`chmod 555`) simulating legacy Python container data owned by UID 100.
   - Ran `amnezia-web-ui-go/bin/panel` against it:
     ```
     2026/09/04 00:00:59 INFO Using SECRET_KEY from environment variable
     time=2026-09-04T00:00:59.937+03:00 level=INFO msg="Starting Amnezia Web Panel" version=1.0.0 port=59200
     time=2026-09-04T00:00:59.937+03:00 level=ERROR msg="data directory \"/tmp/tmp.uyi5K5rL3n/unwritable_data\" is not writable by current user (UID 1000, GID 1000); if upgrading from legacy Python deployment, please run: sudo chown -R 1000:1000 \"/tmp/tmp.uyi5K5rL3n/unwritable_data\"" path=/tmp/tmp.uyi5K5rL3n/unwritable_data uid=1000 gid=1000 err="open /tmp/tmp.uyi5K5rL3n/unwritable_data/.perm_probe_1788469259937659020: permission denied"
     time=2026-09-04T00:00:59.937+03:00 level=ERROR msg="Application terminated with error" err="data directory \"/tmp/tmp.uyi5K5rL3n/unwritable_data\" is not writable by current user (UID 1000, GID 1000); if upgrading from legacy Python deployment, please run: sudo chown -R 1000:1000 \"/tmp/tmp.uyi5K5rL3n/unwritable_data\": open /tmp/tmp.uyi5K5rL3n/unwritable_data/.perm_probe_1788469259937659020: permission denied"
     Binary exited with code: 1
     ```
   - **Verdict: VERIFIED**. Startup terminates cleanly with exit code 1, emits the exact remediation instruction, and prevents misleading SQLite memory/CANTOPEN errors.

### 3.2 TUN Host Requirements Documentation (`docker-compose.yml`)
- Verified lines 30-35 of `docker-compose.yml`:
  ```yaml
    # Host network requirements for AmneziaWG VPN subsystem:
    # /dev/net/tun device mapping and NET_ADMIN capability are required for the
    # in-process AmneziaWG VPN subsystem (when VPN_ENABLED=true).
    # On TUN-less hosts (e.g. unprivileged containers or VPS without TUN/TAP support),
    # operators running in management-only mode (VPN_ENABLED=false) can comment out
    # both the 'devices' section and 'cap_add: [NET_ADMIN]'.
    devices:
      - /dev/net/tun:/dev/net/tun
    cap_drop:
      - ALL
    cap_add:
      - NET_ADMIN
  ```
- **Verdict: VERIFIED**. Instructions are unambiguous and guide operators on unprivileged LXC / OpenVZ containers.

### 3.3 Environment Variable Reserved Staging (`.env.example`)
- Verified lines 73-76 of `.env.example`:
  ```bash
  # Optional public key override / display metadata for client configuration generation.
  # (Reserved for future in-process VPN endpoint phase — currently unused by management panel).
  # If omitted, the panel uses the server's generated WireGuard/AmneziaWG public key.
  VPN_ENDPOINT_PUBLIC_KEY=
  ```
- **Verdict: VERIFIED**. Clearly marked as reserved and unread by current management panel code.

### 3.4 Toolchain Pinning in `go.mod`
- Verified `amnezia-web-ui-go/go.mod` line 5:
  ```go
  go 1.26.0

  toolchain go1.26.6
  ```
- Executed `govulncheck ./...` with toolchain on PATH:
  ```
  === Symbol Results ===

  No vulnerabilities found.

  Your code is affected by 0 vulnerabilities.
  ```
- **Verdict: VERIFIED**. Eliminates stdlib vulnerabilities affecting the compiled application.

### 3.5 Physical Binary Verification & Accurate Image Metrics
1. **Physical Binary**:
   - Path: `amnezia-web-ui-go/bin/panel`
   - Stat Output:
     ```
     -rwxr-xr-x 1 igor igor 16523529 Sep  3 23:49 /home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/bin/panel
     bytes=16523529 blocks=32280 block_size=512
     ```
   - Exact size: **16,523,529 bytes** (~15.76 MB). Physical existence confirmed.
2. **Alpine 3.22 Image Breakdown Accounting**:
   - `docs/deployment.md` and `docs/plans/2026-08-25-go-rewrite.md`:
     | Layer / Component | Size Breakdown | Notes |
     |---|---|---|
     | Base Alpine 3.22 | ~7.5 MB | Minimal root filesystem |
     | Runtime Packages Closure | ~15.5 MB | `ca-certificates`, `libcrypto3`, `tzdata` (0.42 MB), `iproute2`, `iptables`, `curl` |
     | Compiled Go Binary | ~15.7 MB | 16,523,529 bytes (`-trimpath -ldflags="-s -w"`) |
     | **Total Image Footprint** | **~38.6 MB** | Down from ~280 MB in legacy Python/Flask |
- **Verdict: VERIFIED**. Sourced from actual Alpine 3.22 package metrics and matching binary on disk.

---

## 4. Adversarial Verification Checklist

| Item | Requirement | Status | Evidence / Notes |
|---|---|:---:|---|
| **Preflight Writability Probe** | Fail-fast with actionable error before SQLite init | **PASS** | `database.CheckPreflight` called in `cmd/panel` & `cmd/server`. Live tested with `chmod 555`: exit 1, exact `sudo chown -R 1000:1000` message emitted. |
| **TUN-less Host Instructions** | Inline compose comments for unprivileged containers | **PASS** | `docker-compose.yml:30-35` documents commenting out `devices` and `cap_add` when `VPN_ENABLED=false`. |
| **Staging Environment Status** | `VPN_ENDPOINT_PUBLIC_KEY` labeled reserved | **PASS** | `.env.example:73-76` clearly notes "(Reserved for future in-process VPN endpoint phase — currently unused)". |
| **Toolchain Pinning** | `go.mod` pins patched toolchain | **PASS** | `toolchain go1.26.6` pinned in `go.mod`; `govulncheck` confirms 0 affected vulnerabilities. |
| **Physical Binary Verification** | Binary physically present on disk with exact size | **PASS** | `amnezia-web-ui-go/bin/panel` exists: 16,523,529 bytes (~15.76 MB). |
| **Alpine 3.22 Honest Accounting** | Accurate Alpine 3.22 closure footprint (~38.6 MB) | **PASS** | Documented across `docs/deployment.md` and rewrite plan (~7.5MB + ~15.5MB + ~15.7MB = ~38.6MB). |
| **Quality Gates Execution** | Verbatim logs and valid wall-clock timings | **PASS** | All 8 gates passed serially with real timing: race suite 1m03.967s, pytest 2m05.553s. Zero races, zero lint violations. |

---

## 5. Summary of Findings

- **CRITICAL**: 0
- **HIGH**: 0
- **MEDIUM**: 0
- **LOW**: 0
- **INFORMATIONAL**: 0

All residual items identified in `CODE_REVIEW_FIX_VERIFICATION.md` and specified in `residual_packaging_remediation.md` are completely and truthfully resolved.

---

## 6. QA Verdict

**APPROVED**

The Phase 9 packaging and preflight remediation is thoroughly verified. Startup failure diagnostics are actionable, host networking requirements are cleanly documented with unprivileged container fallbacks, toolchain vulnerabilities are mitigated by toolchain pinning, binary size is physically verified, and all quality gates pass unconditionally.
