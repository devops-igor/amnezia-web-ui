# Sub-Task: API Handlers Fixes and Hardening (Phase 5 — Issue #379)

## Context & Objectives
Following the independent code review in `tasks/issue-379-api-handlers/DEV_REVIEW.md`, `dev_bot` must resolve the identified security and parity gaps in `amnezia-web-ui-go/internal/handlers/` and its dependencies.

---

## Required Changes

### 1. Protocol Validation & Container Whitelisting (HIGH finding #1)
- **`ToggleContainerHandler` (`internal/handlers/servers.go`)**:
  - Do not interpolate unvalidated protocol strings into `docker` commands.
  - Define or use an explicit container name mapping (e.g. `awg` -> `amnezia-awg`, `awg2` -> `amnezia-awg2`, `awg-legacy` -> `amnezia-awg-legacy`, `telemt` -> `telemt`, `dns` -> `amnezia-dns`).
  - Validate protocol using `models.IsValidProtocol(proto)` or container map lookup; return HTTP 400 `invalid_protocol` ("Unknown protocol") on invalid/unsupported protocol.
  - Validate action to ensure it is strictly `"start"`, `"stop"`, or `"restart"`.
- **`GetServerConfigHandler` and `SaveServerConfigHandler` (`internal/handlers/servers.go`)**:
  - Validate protocol via `models.IsValidProtocol(proto)` and map to safe explicit config paths (e.g. `/opt/amnezia/awg/config.json`, etc.). Reject invalid protocols with HTTP 400.

### 2. Dependency Vulnerabilities Resolution (HIGH finding #2)
- Update `golang.org/x/crypto` in `amnezia-web-ui-go/go.mod` (e.g., `go get golang.org/x/crypto@latest` or `>= v0.36.0`, followed by `go mod tidy`) to fix the known SSH-related CVEs reported by `govulncheck`.

### 3. Protocol Installed Guard on Connection Creation (MEDIUM finding #6)
- In `internal/handlers/connections.go` (`UserAddConnectionHandler`) and `internal/handlers/server_connections.go` (`ServerAddConnectionHandler`):
  - Ensure the requested protocol is actually installed on the target server (`server.Protocols[proto].Installed`).
  - If not installed, return HTTP 400 with a clean user error: `"Protocol is not installed on this server"`.

### 4. Apply Default Speed Limits Implementation (MEDIUM finding #3)
- In `internal/handlers/servers.go` (`ApplyDefaultSpeedLimitsHandler`):
  - Properly apply default speed limits to clients via the protocol manager / shaper rather than merely counting clients and returning without changes. If no clients need updating or manager handles bulk sync, perform the real sync.

### 5. My Connections & User Limits Parity (MEDIUM finding #5)
- In `internal/handlers/connections.go` (`UserGetMyConnectionsHandler`):
  - Honor per-user connection limits if specified on the user record, falling back to global settings.
  - Populate server reachability/status from server status stored in DB / cache rather than hardcoding static placeholders.

### 6. Minor Polish & Test Fixes (LOW findings)
- Remove redundant duplicate `w.WriteHeader` in `internal/handlers/connections.go`.
- In `internal/handlers/settings_test.go`, assert the specific counts returned in the `restored` map in `RestoreBackupHandler_Full_JSON`.
- Add test coverage for invalid protocol in `ToggleContainerHandler`, `GetServerConfigHandler`, and `SaveServerConfigHandler`.
- Add test coverage for "protocol not installed" check in connection creation.

---

## Compilation Gate (HARD MANDATORY GATE)
Run from `amnezia-web-ui-go/`:
```bash
go fmt ./...
go vet ./...
go build ./...
go test -race -cover ./internal/handlers/... ./internal/router/...
golangci-lint run ./...
gosec -quiet ./...
govulncheck ./...
```
- Test coverage across `internal/handlers` and `internal/router` MUST remain `>= 85.0%`.
- 0 data races.
- 0 lint issues.
- All tests in the full Go test suite (`go test -race ./...`) and Python non-e2e test suite (`python3 -m pytest --tb=no -q --ignore=tests/e2e` from repo root) must pass.

---

## Deliverables & Handover
- Emit developer handover strictly to:
  `tasks/issue-379-api-handlers/api_fixes_dev_handover.md`
- Append `IMPLEMENTATION_COMPLETE` entry to `WORKLOG.md`.
- Hand back to `pm_bot` for smoke verification and QA delegation.
- **DO NOT commit or push git changes.**
