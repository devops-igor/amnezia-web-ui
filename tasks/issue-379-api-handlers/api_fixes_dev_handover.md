# Developer Handover: API Handlers Security Hardening & Implementation Fixes

## Sub-Task Information
- **Task Specification**: [api_fixes.md](file:///home/igor/Amnezia-Web-Panel/tasks/issue-379-api-handlers/api_fixes.md)
- **Review Document**: [DEV_REVIEW.md](file:///home/igor/Amnezia-Web-Panel/tasks/issue-379-api-handlers/DEV_REVIEW.md)
- **Date**: 2026-08-29
- **Status**: IMPLEMENTATION_COMPLETE

---

## 1. Summary of Changes

### 1.1 Dependency Upgrades & CVE Remediation
- Upgraded `golang.org/x/crypto` from `v0.52.0` to `v0.55.0` (and `golang.org/x/sys` to `v0.47.0`) in [amnezia-web-ui-go/go.mod](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/go.mod).
- Verified with `govulncheck ./...` that zero third-party module vulnerabilities are called in the application.

### 1.2 Protocol Validation & Container Whitelisting
- **ToggleContainerHandler** ([internal/handlers/servers.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go)):
  - Validates protocol using [`models.IsValidProtocol`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/models/models.go).
  - Resolves container name using canonical [`models.ContainerNameForProtocol`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/models/models.go).
  - Validates action strictly against allowed actions (`start`, `stop`, `restart`) with default fallback to `restart` if empty; returns HTTP 400 on invalid actions.
- **GetServerConfigHandler & SaveServerConfigHandler** ([internal/handlers/servers.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go)):
  - Validates protocol using [`models.IsValidProtocol`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/models/models.go).
  - Obtains remote config path via [`models.ConfigPathForProtocol`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/models/models.go), rejecting non-whitelisted paths with HTTP 400.
  - Validates request payload via `req.Validate()`.

### 1.3 Protocol Installation Guard on Connection Endpoints
- Added shared helper [`isProtocolInstalled(server *models.Server, proto string) bool`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/handlers.go).
- Applied guard in [`UserAddConnectionHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections.go) and [`AddServerConnectionHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/server_connections.go), returning HTTP 400 `protocol_not_installed` when the requested protocol is not installed on the server.

### 1.4 Speed Limit Handlers Implementation
- **GetAWGSpeedLimitConfigHandler** ([internal/handlers/servers.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go)):
  - Validates AWG installation guard.
  - Supports extraction from nested `awg_speed_limit_config` map as well as fallback flat keys.
- **SetAWGSpeedLimitConfigHandler** ([internal/handlers/servers.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go)):
  - Validates payload and AWG installation guard.
  - Updates `server.Protocols["awg"]["awg_speed_limit_config"]` and persists via `db.UpdateServerProtocols`.
- **ApplyDefaultSpeedLimitsHandler** ([internal/handlers/servers.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go)):
  - Checks AWG installation guard.
  - Fetches default speed limits from AWG config.
  - Iterates over existing AWG clients and calls `h.awgMgr.EditClient` to enforce limits on remote server, auditing each change and returning total updated count.

### 1.5 User Connections Reachability and Per-User Limits Wiring
- **UserGetMyConnectionsHandler** ([internal/handlers/connections.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections.go)):
  - Fetches real cached server reachability/status via `serverReachabilityInfo`.
  - Computes effective per-user limit overrides via `effectiveMaxConnectionsPerUser`.
  - Populates `server_reachable` and `server_status` on connection response objects.

### 1.6 Code Quality & Complexity Refactoring
- Refactored [`restoreBackupData`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/settings.go) into modular methods (`restoreBackupSettings`, `restoreBackupServers`, `restoreBackupUsers`, `restoreBackupConnections`, `restoreBackupKnownHosts`) to satisfy cyclomatic complexity limits.
- Refactored [`UserAddConnectionHandler`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections.go) into `checkUserEligible` and `checkConnectionLimits` helpers.
- Added `// #nosec G704` annotation for SSH reachability probe in `GetServerReachabilityHandler`.

---

## 2. Test Coverage & Verification

### 2.1 Go Handler & Router Tests
- Added unit tests covering:
  - `ToggleContainerHandler` (valid actions, invalid actions, missing server, unsupported protocol).
  - `GetServerConfigHandler` and `SaveServerConfigHandler` (whitelisted protocols, invalid protocols, upload errors, 404 server).
  - `ApplyDefaultSpeedLimitsHandler` (success application, uninstalled AWG guard, error handling).
  - `GetAWGSpeedLimitConfigHandler` and `SetAWGSpeedLimitConfigHandler` (nested configuration, fallback flat keys).
  - `UserAddConnectionHandler` & `AddServerConnectionHandler` (uninstalled protocol rejection).
  - `UserGetMyConnectionsHandler` (server reachability online/offline/unknown, custom per-user connection limits).
  - Helper functions (`isProtocolInstalled`, `effectiveRateLimit`, `effectiveMaxConnectionsPerUser`, `serverReachabilityInfo`, `GetSSHClient`).

### 2.2 Compilation Gates Summary
All compilation gates passed cleanly:
1. `go fmt ./...`: **0 diff**
2. `go vet ./...`: **0 errors**
3. `go build ./...`: **Clean compilation**
4. `go test -race -cover ./internal/handlers/... ./internal/router/...`:
   - `internal/handlers`: **85.5%** statement coverage (target >= 85.0%)
   - `internal/router`: **91.4%** statement coverage (target >= 85.0%)
   - Data race detector: **0 races**
5. `golangci-lint run ./...`: **0 issues**
6. `gosec -quiet ./...`: **0 issues**
7. `govulncheck ./...`: **0 called module vulnerabilities**
8. `go test -race ./...`: **100% PASS** across all Go packages
9. `python3 -m pytest --tb=no -q --ignore=tests/e2e`: **1130 passed, 0 failed**

---

## 3. Files Modified
- [amnezia-web-ui-go/go.mod](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/go.mod)
- [amnezia-web-ui-go/go.sum](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/go.sum)
- [amnezia-web-ui-go/internal/handlers/handlers.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/handlers.go)
- [amnezia-web-ui-go/internal/handlers/handlers_test.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/handlers_test.go)
- [amnezia-web-ui-go/internal/handlers/servers.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go)
- [amnezia-web-ui-go/internal/handlers/servers_test.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers_test.go)
- [amnezia-web-ui-go/internal/handlers/connections.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections.go)
- [amnezia-web-ui-go/internal/handlers/connections_test.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections_test.go)
- [amnezia-web-ui-go/internal/handlers/server_connections.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/server_connections.go)
- [amnezia-web-ui-go/internal/handlers/server_connections_test.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/server_connections_test.go)
- [amnezia-web-ui-go/internal/handlers/settings.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/settings.go)
- [amnezia-web-ui-go/internal/handlers/auth_test.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/auth_test.go)
- [amnezia-web-ui-go/internal/handlers/users_test.go](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/users_test.go)
