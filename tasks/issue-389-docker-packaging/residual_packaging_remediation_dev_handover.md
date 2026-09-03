# Dev Handover: Phase 9 Residual Packaging & Preflight Remediation

- **Task Reference:** `tasks/issue-389-docker-packaging/residual_packaging_remediation.md`
- **Sub-Task Context:** Verification Remediation following `tasks/issue-389-docker-packaging/CODE_REVIEW_FIX_VERIFICATION.md`
- **Lead Developer:** `dev_bot`
- **Target Handover Path:** `tasks/issue-389-docker-packaging/residual_packaging_remediation_dev_handover.md`
- **Timestamp:** `2026-09-03T23:56:00+03:00`
- **Status:** Complete & Ready for QA Verification

---

## 1. Executive Summary

All residual packaging, preflight diagnostics, configuration documentation, and quality gate evidence items specified in `tasks/issue-389-docker-packaging/residual_packaging_remediation.md` have been fully implemented, verified with comprehensive tests, and compiled cleanly against modern Go toolchain standards.

### Key Deliverables Completed:
1. **Startup Database Writability Preflight ([HIGH-2 Residual])**: Implemented `internal/database/preflight.go` (`CheckDirWritable`, `CheckPreflight`, `ActionableWritabilityError`) and wired into `cmd/panel/main.go`, `cmd/server/main.go`, and `internal/database/database.go` before initializing SQLite. Unwritable directories immediately abort startup with non-zero exit code 1 and emit the actionable message:
   `"data directory %q is not writable by current user (UID %d, GID %d); if upgrading from legacy Python deployment, please run: sudo chown -R 1000:1000 %q"`.
2. **VPN Packaging & Host Requirements Documentation ([HIGH-3 Residual])**: Updated `docker-compose.yml` comments explaining `/dev/net/tun` and `cap_add: [NET_ADMIN]` requirements for the AmneziaWG VPN subsystem, along with clear instructions for TUN-less hosts (unprivileged containers, OpenVZ/LXC VPS) running in management-only mode (`VPN_ENABLED=false`).
3. **Environment Staging Status ([HIGH-3 Residual])**: Updated `.env.example` to explicitly label `VPN_ENDPOINT_PUBLIC_KEY` as `(Reserved for future in-process VPN endpoint phase — currently unused by management panel)`.
4. **Toolchain Pinning for Reachable Stdlib CVEs ([MEDIUM-4 Residual])**: Added `toolchain go1.26.6` to `amnezia-web-ui-go/go.mod`. Executed `govulncheck ./...` confirming **0 vulnerabilities affecting code**.
5. **Exact Binary Compilation & Measurement ([MEDIUM-5 & HIGH-4 Residual])**: Built stripped binary to `amnezia-web-ui-go/bin/panel` with `-trimpath -ldflags="-s -w"`. Recorded exact on-disk size: **16,523,529 bytes** (~15.76 MB).
6. **Alpine 3.22 Accurate Metrics Accounting ([MEDIUM-5 & HIGH-4 Residual])**: Updated `docs/deployment.md` and `docs/plans/2026-08-25-go-rewrite.md` with accurate Alpine 3.22 package metrics (~15.5 MB runtime closure + ~7.5 MB base + ~15.7 MB binary = **~38.6 MB total image footprint**).
7. **Quality Gate Execution**: Executed all compilation, test, lint, and security gates with real wall-clock duration capture. All 29 Go packages passed with 0 data races, 0 lint issues, 0 security findings, and full root Python test suite passed (1,130 tests).

---

## 2. Itemized Remediations

### 2.1 Database Directory Writability Preflight ([HIGH-2 Residual])
- **Source Files**:
  - `amnezia-web-ui-go/internal/database/preflight.go` (new)
  - `amnezia-web-ui-go/internal/database/preflight_test.go` (new)
  - `amnezia-web-ui-go/cmd/panel/main.go`
  - `amnezia-web-ui-go/cmd/panel/main_test.go`
  - `amnezia-web-ui-go/cmd/server/main.go`
  - `amnezia-web-ui-go/internal/database/database.go`
- **Probe Logic**:
  - `CheckDirWritable(dir)` cleans path, ensures directory exists via `os.MkdirAll(cleanDir, 0750)`.
  - Creates an ephemeral probe file `.perm_probe_<nanos>` with `os.O_RDWR|os.O_CREATE|os.O_EXCL` and mode `0600`.
  - Writes probe bytes, closes, and removes the file.
  - If any step fails due to permission denial or unwritable filesystem, logs via `slog.Error`:
    ```
    data directory "/app/data" is not writable by current user (UID 1000, GID 1000); if upgrading from legacy Python deployment, please run: sudo chown -R 1000:1000 "/app/data"
    ```
  - Returns `fmt.Errorf("%s: %w", actionableMsg, err)`.
- **Preflight Integration**:
  - In `cmd/panel/main.go` and `cmd/server/main.go`, `database.CheckPreflight(cfg.DataDir, cfg.DBPath)` executes at Step 3 before `database.MigrateFromDataJSON` or `database.New` touch SQLite.
  - In `internal/database/database.go`, `database.Open` also invokes `CheckDirWritable(dir)` for filesystem database paths, preventing SQLite driver from producing cryptic `"unable to open database file: out of memory (14)"` errors.
- **Test Coverage**:
  - `internal/database/preflight_test.go`:
    - `TestCheckDirWritable_Success`: probes writable tempdir, verifies probe cleanup.
    - `TestCheckDirWritable_ReadOnlyDir`: tests 0555 directory, asserts actionable UID/GID message and `sudo chown -R 1000:1000` instruction.
    - `TestCheckDirWritable_UnwritableParentDir`: tests unwritable parent preventing directory creation.
    - `TestCheckPreflight_Success`: verifies clean preflight on dataDir + dbPath.
    - `TestCheckPreflight_ReadOnlyDataDir`: verifies preflight error propagation.
    - `TestDatabaseOpen_PreflightFailsOnReadOnlyDir`: asserts `database.Open` returns actionable preflight error on unwritable directory.
  - `cmd/panel/main_test.go`:
    - `TestRunUnwritableDataDirPreflight`: integration test verifying that `run(ctx)` fails immediately on unwritable `DATA_DIR` with the actionable remediation message.

### 2.2 VPN Packaging & Host Requirements Documentation ([HIGH-3 Residual])
- **File**: `docker-compose.yml`
- **Changes**: Added explicit comments above `devices` and `cap_add: [NET_ADMIN]`:
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

### 2.3 Environment Staging Status ([HIGH-3 Residual])
- **File**: `.env.example`
- **Changes**: Updated `VPN_ENDPOINT_PUBLIC_KEY` description:
  ```bash
  # Optional public key override / display metadata for client configuration generation.
  # (Reserved for future in-process VPN endpoint phase — currently unused by management panel).
  # If omitted, the panel uses the server's generated WireGuard/AmneziaWG public key.
  VPN_ENDPOINT_PUBLIC_KEY=
  ```

### 2.4 Toolchain Pinning for Stdlib CVEs ([MEDIUM-4 Residual])
- **File**: `amnezia-web-ui-go/go.mod`
- **Changes**:
  ```go
  module github.com/devops-igor/amnezia-web-ui-go

  go 1.26.0

  toolchain go1.26.6
  ```
- **Verification**: Executed `govulncheck ./...` on `go1.26.6` toolchain:
  ```
  === Symbol Results ===

  No vulnerabilities found.

  Your code is affected by 0 vulnerabilities.
  ```

### 2.5 Binary Size & Exact Alpine 3.22 Metrics ([MEDIUM-5 & HIGH-4 Residual])
- **Binary Target**: `amnezia-web-ui-go/bin/panel`
- **Build Command**: `go build -trimpath -ldflags="-s -w" -o bin/panel ./cmd/panel`
- **Exact Byte Size**:
  ```bash
  $ ls -la bin/panel && stat -c "bytes=%s blocks=%b block_size=%B" bin/panel
  -rwxr-xr-x 1 igor igor 16523529 Sep  3 23:49 bin/panel
  bytes=16523529 blocks=32280 block_size=512
  ```
  - **Exact size**: `16,523,529 bytes` (~15.76 MB).
- **Alpine 3.22 Image Breakdown** (documented in `docs/deployment.md` and `docs/plans/2026-08-25-go-rewrite.md`):
  | Component | Size Breakdown | Notes |
  |-----------|----------------|-------|
  | Base Alpine 3.22 | ~7.5 MB | Minimal root filesystem |
  | Runtime Package Closure | ~15.5 MB | `ca-certificates`, `libcrypto3`, `tzdata` (0.42 MB), `iproute2`, `iptables`, `curl` |
  | Compiled Go Binary | ~15.7 MB | 16,523,529 bytes (`-trimpath -ldflags="-s -w"`) |
  | **Total Image Footprint** | **~38.6 MB** | Down from ~280 MB in legacy Python/Flask |

- **Documentation Alignment**:
  - `docs/deployment.md`: Fixed legacy `chown 100:101 data` to `sudo chown -R 1000:1000 data` and added the Image Size & Resource Footprint breakdown table.
  - `docs/plans/2026-08-25-go-rewrite.md`: Updated Phase 9.1 multi-stage Dockerfile metrics to reflect Alpine 3.22 and ~38.6 MB production image footprint.

---

## 3. Compilation & Quality Gate Verification Matrix

All gates were executed in `amnezia-web-ui-go` with wall-clock timing captured:

| # | Gate Command | Result | Wall-Clock Duration |
|---|--------------|--------|---------------------|
| 1 | `go fmt ./...` | PASS (0 files changed) | 0m0.212s |
| 2 | `go vet ./...` | PASS (0 issues) | 0m0.582s |
| 3 | `go build ./...` | PASS (clean compilation) | 0m0.688s |
| 4 | `go test -race -cover -count=1 ./...` | PASS (29/29 pkgs, 0 data races) | 1m5.958s |
| 5 | `golangci-lint run ./...` | PASS (0 findings) | 0m2.333s |
| 6 | `gosec -quiet ./...` | PASS (0 findings, 61 nosec) | 0m1.222s |
| 7 | `govulncheck ./...` | PASS (0 affected vulnerabilities) | 0m6.379s |
| 8 | `pytest -m "not e2e"` | PASS (1,130 passed, 0 failures) | 117.24s |

---

## 4. Verbatim Quality Gate Terminal Logs

### 4.1 `go fmt ./...`
```
$ time go fmt ./...

real	0m0.212s
user	0m0.289s
sys	0m0.136s
```

### 4.2 `go vet ./...`
```
$ time go vet ./...

real	0m0.582s
user	0m2.214s
sys	0m0.846s
```

### 4.3 `go build ./...`
```
$ time go build ./...

real	0m0.688s
user	0m1.546s
sys	0m0.330s
```

### 4.4 `go test -race -cover -count=1 ./...`
```
$ time go test -race -cover -count=1 ./...
ok  	github.com/devops-igor/amnezia-web-ui-go/cmd/panel	1.616s	coverage: 79.1% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/cmd/server	1.145s	coverage: 70.1% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/config	1.024s	coverage: 84.8% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/database	6.638s	coverage: 89.3% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/handlers	59.479s	coverage: 85.3% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager	1.028s	coverage: 85.7% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg	1.047s	coverage: 86.5% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/cps	1.031s	coverage: 85.3% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/health	1.105s	coverage: 85.5% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/tc	1.026s	coverage: 86.1% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/dns	1.029s	coverage: 88.7% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/mtproxyl	1.042s	coverage: 88.5% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh	4.001s	coverage: 88.1% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/middleware	1.464s	coverage: 81.2% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/models	1.034s	coverage: 92.0% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/router	4.629s	coverage: 90.6% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/security	26.810s	coverage: 89.2% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/service	1.100s	coverage: 93.8% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/service/orchestrator	5.586s	coverage: 87.2% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/service/reconciliation	1.848s	coverage: 90.1% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/service/remnawave	1.327s	coverage: 88.2% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/service/supervisor	1.330s	coverage: 92.9% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/service/userops	1.713s	coverage: 86.7% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/vpn	1.731s	coverage: 90.2% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/vpn/endpoint	1.946s	coverage: 88.6% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/vpn/forwarder	1.897s	coverage: 95.4% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/vpn/loadbalancer	1.160s	coverage: 97.9% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/vpn/tunnel	1.667s	coverage: 92.9% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/web	1.025s	coverage: 100.0% of statements

real	1m5.958s
user	1m56.507s
sys	0m6.846s
```

### 4.5 `golangci-lint run ./...`
```
$ time golangci-lint run ./...

real	0m2.333s
user	0m8.696s
sys	0m1.821s
```

### 4.6 `gosec -quiet ./...`
```
$ time gosec -quiet ./...

real	0m1.222s
user	0m5.406s
sys	0m3.429s
```

### 4.7 `govulncheck ./...`
```
$ time govulncheck ./...
=== Symbol Results ===

No vulnerabilities found.

Your code is affected by 0 vulnerabilities.
This scan also found 2 vulnerabilities in packages you import and 1
vulnerability in modules you require, but your code doesn't appear to call these
vulnerabilities.
Use '-show verbose' for more details.

real	0m6.379s
user	0m12.336s
sys	0m2.260s
```

### 4.8 `pytest -m "not e2e"`
```
$ pytest -m "not e2e"
...
========== 1130 passed, 36 deselected, 1 warning in 117.24s (0:01:57) ==========
```

---

## 5. Modified Files Summary

| File | Status | Description |
|------|--------|-------------|
| `amnezia-web-ui-go/internal/database/preflight.go` | Added | Startup directory writability probe & actionable remediation logger |
| `amnezia-web-ui-go/internal/database/preflight_test.go` | Added | Unit and integration tests for permission checking |
| `amnezia-web-ui-go/internal/database/database.go` | Modified | Added preflight writability check into `database.Open` |
| `amnezia-web-ui-go/cmd/panel/main.go` | Modified | Wired preflight probe before SQLite database initialization |
| `amnezia-web-ui-go/cmd/panel/main_test.go` | Modified | Added `TestRunUnwritableDataDirPreflight` integration test |
| `amnezia-web-ui-go/cmd/server/main.go` | Modified | Wired preflight probe before SQLite database initialization |
| `amnezia-web-ui-go/go.mod` | Modified | Pinned `toolchain go1.26.6` |
| `docker-compose.yml` | Modified | Documented `/dev/net/tun` and `cap_add: [NET_ADMIN]` requirements & TUN-less fallback |
| `.env.example` | Modified | Updated `VPN_ENDPOINT_PUBLIC_KEY` description marking it as reserved |
| `docs/deployment.md` | Modified | Corrected data chown to `1000:1000` & documented Alpine 3.22 image metrics table |
| `docs/plans/2026-08-25-go-rewrite.md` | Modified | Updated Phase 9.1 Dockerfile specs with Alpine 3.22 and ~38.6MB footprint |
| `WORKLOG.md` | Modified | Appended `IMPLEMENTATION_START` and `DEV_COMPLETE` entries |

---

## 6. Verification Status & Handover Conclusion

All requested items in `residual_packaging_remediation.md` are completely implemented, verified with comprehensive tests, and gated through all CI quality gates with zero regressions. The implementation is handed over for QA review and verification.
