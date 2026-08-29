# QA Audit Report: Phase 5 — API Handlers & Business Logic (Issue #379) — Sub-task: api_fixes

**Date**: 2026-08-29  
**Auditor**: qa_bot (Quality Gatekeeper)  
**Verdict**: **APPROVED**  
**Task Specification**: [`tasks/issue-379-api-handlers/TASK.md`](file:///home/igor/Amnezia-Web-Panel/tasks/issue-379-api-handlers/TASK.md)  
**Sub-Task Specification**: [`tasks/issue-379-api-handlers/api_fixes.md`](file:///home/igor/Amnezia-Web-Panel/tasks/issue-379-api-handlers/api_fixes.md)  
**Dev Review**: [`tasks/issue-379-api-handlers/DEV_REVIEW.md`](file:///home/igor/Amnezia-Web-Panel/tasks/issue-379-api-handlers/DEV_REVIEW.md)  
**Dev Handover**: [`tasks/issue-379-api-handlers/api_fixes_dev_handover.md`](file:///home/igor/Amnezia-Web-Panel/tasks/issue-379-api-handlers/api_fixes_dev_handover.md)  
**Primary Specifications**: [`docs/plans/2026-08-25-go-rewrite.md`](file:///home/igor/Amnezia-Web-Panel/docs/plans/2026-08-25-go-rewrite.md) (Section 2.9 & Phase 5), [`docs/specs/01-domain-model.md`](file:///home/igor/Amnezia-Web-Panel/docs/specs/01-domain-model.md), [`docs/specs/02-api-endpoints.md`](file:///home/igor/Amnezia-Web-Panel/docs/specs/02-api-endpoints.md), [`docs/specs/03-database.md`](file:///home/igor/Amnezia-Web-Panel/docs/specs/03-database.md)

---

## 1. Executive Summary

An independent, rigorous QA audit and test verification was conducted on Phase 5 (API Handlers & Business Logic, Issue #379) and the subsequent security hardening and parity fixes (`api_fixes.md`) in `amnezia-web-ui-go/internal/handlers/`, `internal/router/`, `internal/models/`, and `go.mod`.

All security vulnerabilities and parity gaps identified in `DEV_REVIEW.md` have been resolved and verified with exhaustive test coverage:
1. **Strict Protocol & Container Whitelisting**: [`ToggleContainerHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L455-L520) strictly validates protocols via [`models.IsValidProtocol`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/models/models.go#L36-L38) and container names via [`models.ContainerNameForProtocol`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/models/models.go#L43-L54), and restricts actions to `start`, `stop`, `restart`. No unvalidated user input is interpolated into shell commands.
2. **Path Traversal Prevention on Remote Configs**: [`GetServerConfigHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L523-L570) and [`SaveServerConfigHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L573-L600) resolve target paths exclusively via [`models.ConfigPathForProtocol`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/models/models.go#L58-L69) using an explicit whitelist (`/opt/amnezia/awg/awg0.conf`, `/opt/amnezia/dns/unbound.conf`, `/opt/mtproxyl/settings.conf`).
3. **Dependency CVE Remediation**: Upgraded `golang.org/x/crypto` to `v0.55.0` in [go.mod](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/go.mod#L9), reducing third-party called module vulnerabilities to **zero** in `govulncheck`.
4. **Protocol Installation Guard**: Added [`isProtocolInstalled`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/handlers.go#L243-L259) to block connection creation for uninstalled protocols on the target server with HTTP 400 `protocol_not_installed`.
5. **Speed Limit Sync**: [`ApplyDefaultSpeedLimitsHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L845-L932) actively applies speed limits across existing AWG peers via `h.awgMgr.EditClient` with audit logging and updated client counts.
6. **User Connections Reachability & Limits**: [`UserGetMyConnectionsHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections.go#L57-L121) resolves live server reachability from cache and calculates effective per-user limit overrides.
7. **Coverage Gate & Concurrency Safety**: Statement coverage is **85.6%** in `internal/handlers` and **91.4%** in `internal/router` (both exceeding the $\ge 85.0\%$ gate), with **0 data races** detected under `-race`.

---

## 2. Quality Gates & Verification Protocol

All mandatory compilation, static analysis, security scanning, and test execution gates passed cleanly:

| Gate | Execution Command | Result | Findings / Notes |
|---|---|---|---|
| **Code Formatting** | `go fmt ./...` | **PASS** | 0 diffs / cleanly formatted |
| **Go Vet** | `go vet ./...` | **PASS** | 0 errors |
| **Go Build** | `go build ./...` | **PASS** | Succeeded with exit code 0 |
| **Go Handlers & Router Tests** | `go test -race -cover ./internal/handlers/... ./internal/router/...` | **PASS** | Handlers: **85.6%**, Router: **91.4%**, 0 races |
| **Full Repository Go Tests** | `go test -count=1 -race ./...` | **PASS** | 100% PASS across all 23 Go packages |
| **Go Linter** | `golangci-lint run ./...` | **PASS** | 0 issues reported |
| **Go Security Scanner** | `gosec -quiet ./...` | **PASS** | 0 security findings |
| **Vulnerability Scanner** | `govulncheck ./...` | **PASS** | **0 called third-party module vulnerabilities** (10 stdlib-only warnings from compiler toolchain `go1.26.2`) |
| **Python Regression Tests** | `python3 -m pytest --tb=no -q --ignore=tests/e2e` | **PASS** | **1130 passed, 0 failed** in 105.78s |
| **Python Code Format & Lint** | `black --check . && flake8 .` | **PASS** | 112 files clean, 0 errors |

---

## 3. Package Statement Coverage Matrix

Both packages touched by Phase 5 and the sub-task exceed the mandatory $\ge 85.0\%$ statement coverage threshold:

| Package Path | Statement Coverage | Minimum Requirement | Status |
|---|---|---|---|
| `internal/handlers` | **85.6%** | $\ge 85.0\%$ | **PASS** |
| `internal/router` | **91.4%** | $\ge 85.0\%$ | **PASS** |
| `internal/models` | **92.0%** | $\ge 85.0\%$ | **PASS** |

---

## 4. Security Audit & Findings Resolution

### 4.1 Protocol Injection & Arbitrary Command Execution (HIGH #1) — RESOLVED
- **Vulnerability**: Potential command injection if arbitrary protocol strings were passed to Docker commands.
- **Remediation**:
  - [`models.ContainerNameForProtocol`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/models/models.go#L43-L54) maps only `awg -> amnezia-awg`, `telemt -> telemt`, and `dns -> amnezia-dns`.
  - In [`ToggleContainerHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L455-L520), invalid protocols return HTTP 400 `invalid_protocol`.
  - Actions are restricted strictly to `start`, `stop`, `restart` (defaulting to `restart` if empty), rejecting invalid inputs with HTTP 400.
  - Verified by unit test: `ToggleContainerHandler Rejects Unknown Protocol` in [`servers_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers_test.go#L405-L417).

### 4.2 Path Traversal in Server Config Endpoints — RESOLVED
- **Vulnerability**: Potential arbitrary file read/write if protocol strings were concatenated into file paths.
- **Remediation**:
  - [`models.ConfigPathForProtocol`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/models/models.go#L58-L69) resolves fixed canonical paths: `/opt/amnezia/awg/awg0.conf`, `/opt/amnezia/dns/unbound.conf`, `/opt/mtproxyl/settings.conf`.
  - [`GetServerConfigHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L523-L570) and [`SaveServerConfigHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L573-L600) reject any unknown protocol with HTTP 400 `invalid_protocol`.
  - Verified by unit tests: `GetServerConfigHandler Rejects Unknown Protocol` and `SaveServerConfigHandler Rejects Unknown Protocol` in [`servers_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers_test.go#L419-L437).

### 4.3 Dependency Vulnerabilities (HIGH #2) — RESOLVED
- **Remediation**: Updated `golang.org/x/crypto` from `v0.52.0` to `v0.55.0` in [go.mod](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/go.mod#L9) and [go.sum](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/go.sum).
- **Audit Verification**: Ran `govulncheck ./...`. Found **0 vulnerabilities in called application modules**. All remaining findings belong to the standard library of the local Go runtime toolchain (`go1.26.2`).

### 4.4 Protocol Installation Guard (MEDIUM #6) — RESOLVED
- **Remediation**:
  - Implemented [`isProtocolInstalled`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/handlers.go#L243-L259).
  - Enforced guard in [`UserAddConnectionHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections.go#L182-L186) and [`AddServerConnectionHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/server_connections.go#L119-L123).
  - Returns HTTP 400 `protocol_not_installed` if the target protocol is not installed on the server.
  - Verified by unit tests in [`connections_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections_test.go#L156-L160) and [`server_connections_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/server_connections_test.go#L222-L241).

### 4.5 Apply Default Speed Limits Real Implementation (MEDIUM #3) — RESOLVED
- **Remediation**:
  - In [`ApplyDefaultSpeedLimitsHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L845-L932), retrieves default download/upload limits from the server's AWG configuration (checking both nested `awg_speed_limit_config` and flat keys).
  - Iterates over existing AWG peers and calls `awgMgr.EditClient` to update remote traffic control limits.
  - Returns total updated count and logs audit event `server.awg_apply_default_speed_limits`.
  - Verified by unit test: `ApplyDefaultSpeedLimitsHandler` in [`servers_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers_test.go#L701-L745).

### 4.6 My Connections Reachability & User Limits Parity (MEDIUM #5) — RESOLVED
- **Remediation**:
  - In [`UserGetMyConnectionsHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections.go#L57-L121), queries [`serverReachabilityInfo`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections.go#L41-L54) to populate actual `server_status` and boolean `server_reachable`.
  - Uses [`effectiveMaxConnectionsPerUser`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections.go#L17-L24) and [`effectiveRateLimit`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections.go#L28-L38) to respect custom user-level limits overriding global defaults.
  - Verified by unit tests in [`connections_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections_test.go#L60-L116).

---

## 5. QA Checklist

- [x] All 75 API endpoints and web routes properly mounted and verified
- [x] Go unit and integration tests pass with race detector enabled (`0 data races`)
- [x] Statement coverage gate met: `internal/handlers` (**85.6%** $\ge 85.0\%$) and `internal/router` (**91.4%** $\ge 85.0\%$)
- [x] Full Go test suite passes across the entire project (`23/23 packages PASS`)
- [x] `golangci-lint` clean with 0 issues
- [x] `gosec` clean with 0 issues
- [x] `govulncheck` clean with 0 called third-party module vulnerabilities
- [x] Full Python regression test suite clean (**1130 passed, 0 failed**)
- [x] Python formatting and linting clean (`black` and `flake8`)
- [x] Strict protocol validation and Docker container name whitelisting in place
- [x] Remote configuration file path traversal prevented via explicit canonical mapping
- [x] State-changing handlers perform audit logging (`h.audit`)
- [x] Error responses sanitized with generic safe messages
- [x] Cyclomatic complexity maintained within thresholds across all handler methods

---

## 6. Audit Verdict

**APPROVED**

Phase 5 (API Handlers & Business Logic) and the security hardening sub-task (`api_fixes.md`) are fully verified, robust, and ready for merging into the main codebase.
