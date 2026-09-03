# Sub-Task: Phase 9 Residual Packaging & Preflight Remediation (`residual_packaging_remediation.md`)

## 1. Context & Objectives
Following the Senior Verification Review in `tasks/issue-389-docker-packaging/CODE_REVIEW_FIX_VERIFICATION.md`, residual operational edge cases, preflight diagnostics, configuration documentation gaps, and gate evidence artifacts require remediation.

---

## 2. Required Remediation Items

### 2.1 Database Directory Writability Preflight ([HIGH-2 Residual])
1. **`cmd/panel/main.go` & `cmd/server/main.go` (or `internal/database`)**:
   - Implement a startup writability preflight before initializing SQLite:
     - Check directory permissions for `cfg.DATA_DIR` and `filepath.Dir(cfg.DBPath)`.
     - Attempt to create/write/remove a temporary probe file (e.g. `.perm_probe`).
     - If unwritable or permission denied, log a clear, human-actionable error with `slog.Error`:
       `"data directory %q is not writable by current user (UID %d, GID %d); if upgrading from legacy Python deployment, please run: sudo chown -R 1000:1000 %q"`
     - Terminate startup with a non-zero exit code immediately rather than letting SQLite fail with cryptic `"unable to open database file: out of memory (14)"`.
   - Add unit/integration test verifying the preflight error handling on unwritable directories.

### 2.2 VPN Packaging & Environment Documentation Hardening ([HIGH-3 Residual])
1. **`docker-compose.yml`**:
   - Add inline comments documenting host requirements: explain that `/dev/net/tun` and `cap_add: [NET_ADMIN]` are required for the AmneziaWG VPN subsystem. If running in management-only mode on TUN-less VPS/containers, operators can comment out `devices` and `cap_add`.
2. **`.env.example`**:
   - Update `VPN_ENDPOINT_PUBLIC_KEY` description: explicitly note `(Reserved for future in-process VPN endpoint phase — currently unused by management panel)`.

### 2.3 Toolchain Pinning for Stdlib CVEs ([MEDIUM-4 Residual])
1. **`amnezia-web-ui-go/go.mod`**:
   - Add `toolchain go1.26.6` (or appropriate Go 1.26 patch toolchain) to ensure toolchains targeting this module build with patched standard library packages.

### 2.4 Verbatim Real Quality Gate Execution & Exact Alpine 3.22 Metrics ([MEDIUM-5 & HIGH-4 Residual])
1. **Compilation & Packaging**:
   - Ensure the binary is compiled and written to `amnezia-web-ui-go/bin/panel` (`go build -trimpath -ldflags="-s -w" -o bin/panel ./cmd/panel`).
   - Measure actual binary size with `stat` / `ls -l` and record the exact byte count.
2. **Alpine 3.22 Accurate Breakdown**:
   - Update image breakdown with the real Alpine 3.22 APKINDEX numbers:
     - Base Alpine 3.22: ~7.5 MB
     - Alpine 3.22 dependencies closure (ca-certificates + libcrypto3, tzdata, iproute2, iptables, curl and libraries): ~15.5 MB (tzdata is 0.42 MB in 3.22)
     - Go binary: ~15.7 MB (16,490,761 bytes)
     - Total honest image footprint: **~38.6 MB**.
3. **Execution Logs**:
   - Execute all gates with real wall-clock timing capture.

---

## 3. Output Deliverable
`dev_bot` MUST emit its handover report strictly to:
`tasks/issue-389-docker-packaging/residual_packaging_remediation_dev_handover.md`
