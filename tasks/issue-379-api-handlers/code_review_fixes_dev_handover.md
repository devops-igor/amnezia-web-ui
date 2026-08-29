# Developer Handover: Issue #379 Code Review Security & Parity Remediation

## Executive Summary
This remediation resolves all findings identified in the senior code review (`tasks/issue-379-api-handlers/CODE_REVIEW.md`). All high, medium, and low severity issues have been addressed in `amnezia-web-ui-go`, verified through expanded regression tests, and passed all mandatory compilation gates with 0 data races, 0 linter errors, and statement coverage meeting or exceeding requirements (`internal/handlers`: 85.0%, `internal/router`: 91.5%, `internal/database`: 89.7%).

---

## Implemented Remediations

### 1. Sensitive Secrets Masking & Round-Trip Preservation (HIGH #1 & HIGH #4)
- **Files**: `internal/handlers/settings.go`, `internal/handlers/settings_test.go`
- **Implementation**:
  - `GetSettingsHandler`: Masks `syncCfg.RemnawaveAPIKey` (`"********"`) and telegram `bot_token`/`token` (`"********"`), and hides SSL `KeyText`/`CertText` (empty string).
  - `SaveSettingsHandler`: Extracted `preserveSecretsOnSave` helper. When saving `ssl`, `sync`, or `telegram`, if incoming secrets are empty or `"********"`, existing values are loaded from the database and preserved.
  - Extracted `persistSettings` helper and propagated database errors from `SetSetting` calls instead of discarding with `_ =`.
- **Regression Test**: `TestSettingsSave_PreservesSSLCertAndSecrets` in `internal/handlers/settings_test.go`.

### 2. Username Uniqueness & First-Run Setup Race (HIGH #2)
- **Files**: `internal/database/schema.sql`, `internal/database/database.go`, `internal/database/users.go`, `internal/handlers/auth.go`, `internal/handlers/users.go`
- **Implementation**:
  - Defined `CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username);` in `schema.sql`.
  - Added migration `migrateUniqueUsernameIndex` in `database.go` inside `runMigrationsLocked` to drop existing non-unique index and create unique index.
  - Added `ErrUserAlreadyExists` and `isUniqueConstraintError` helper in `internal/database/users.go`.
  - Added `setupMu sync.Mutex` in `internal/handlers/handlers.go` and synchronized `APISetupHandler` in `internal/handlers/auth.go` to eliminate check-then-act races during first-run admin initialization. Returning HTTP 409 `setup_already_done` on race/duplicate.
  - Updated `AddUserHandler` in `internal/handlers/users.go` to return HTTP 400 `user_exists` when duplicate username constraint triggers.
- **Regression Test**: `TestSetupRace_UniqueAdminConstraint` in `internal/handlers/auth_test.go`.

### 3. Session Revocation & Disabled/Deleted User Checks (HIGH #3)
- **Files**: `internal/middleware/session.go`, `internal/router/router.go`, `internal/handlers/auth_test.go`
- **Implementation**:
  - Added `UserLookupFunc` and `SetUserLookup` callback mechanism in `internal/middleware/session.go`.
  - Wired DB user lookup in `NewRouterWithOptions` in `internal/router/router.go`.
  - Updated `RequireAuth`, `RequireAdmin`, and `RequireAdminOrSupport` to look up the user in DB via `checkUserActive`. If user is missing or `Enabled == false`, `ClearSessionCookie(w)` is called, and HTTP 401 `unauthorized` is returned (or redirected to `/login` for HTML routes). Active user's role is refreshed dynamically.
- **Regression Test**: `TestDisabledUser_SessionRejected` in `internal/handlers/auth_test.go`.

### 4. Robust Error Propagation on Mutating Handlers (MEDIUM #1)
- **Files**: `internal/handlers/servers.go`, `internal/handlers/server_connections.go`, `internal/handlers/settings.go`, `internal/handlers/users.go`
- **Implementation**:
  - `servers.go`: Checked errors from SSH commands and database mutations in `DeleteServerHandler`, `RebootServerHandler`, `ClearServerHandler`, `ToggleContainerHandler`, `SetClientSpeedLimitHandler`, and `SetAWGSpeedLimitConfigHandler`.
  - `server_connections.go`: Propagated errors in `RemoveServerConnectionHandler` and `EditServerConnectionHandler`.
  - `settings.go`: Propagated errors in `SaveSettingsHandler` and `SyncDeleteHandler`.
  - `users.go`: Propagated errors in `DeleteUserHandler` and `ToggleUserHandler`.
- **Regression Test**: `TestServerHandlers/Upload_Failure_and_Speed_Removal`, `TestHandlers_EdgeCasesAndErrorBranches`.

### 5. Connection Limit TOCTOU Race Protection (MEDIUM #2)
- **Files**: `internal/handlers/handlers.go`, `internal/handlers/connections.go`, `internal/handlers/connections_test.go`
- **Implementation**:
  - Added per-user mutex map `userConnMu` and `lockUser(userID string) func()` helper in `internal/handlers/handlers.go`.
  - Synchronized `UserAddConnectionHandler` in `internal/handlers/connections.go` across limit check and connection creation.
- **Regression Test**: `TestConnectionLimit_ConcurrentAdds` in `internal/handlers/connections_test.go`.

### 6. Backup Settings Allowlist (MEDIUM #3)
- **Files**: `internal/handlers/settings.go`, `internal/handlers/settings_test.go`
- **Implementation**:
  - Allowlisted valid setting keys (`appearance`, `sync`, `captcha`, `telegram`, `ssl`, `limits`) in `restoreBackupSettings` in `internal/handlers/settings.go`. Unrecognized keys are ignored.
- **Regression Test**: Verified in `TestSettingsSave_PreservesSSLCertAndSecrets` in `internal/handlers/settings_test.go`.

### 7. Empty SecretKey Fast-Fail (MEDIUM #4)
- **Files**: `internal/handlers/auth.go`, `internal/handlers/auth_test.go`
- **Implementation**:
  - In `CaptchaHandler` and `APILoginHandler`, if `h.cfg == nil || h.cfg.SecretKey == ""`, return HTTP 500 `internal_error` with detail `"Session signing key not configured"`.
- **Regression Test**: `TestEmptySecretKey_ReturnsError` in `internal/handlers/auth_test.go`.

### 8. Language Parameter Validation (LOW)
- **Files**: `internal/handlers/auth.go`, `internal/handlers/auth_test.go`
- **Implementation**:
  - In `SetLangHandler`, validated `lang` strictly against `en` or `ru`, normalizing input and safely falling back to `en` on path traversals, arbitrary characters, or invalid inputs.
- **Regression Test**: `TestSetLang_Validation` in `internal/handlers/auth_test.go`.

---

## Compilation Gate Verification Results

| Gate / Command | Status | Notes |
|---|---|---|
| `go fmt ./...` | **PASS** | Formatted cleanly |
| `go vet ./...` | **PASS** | 0 warnings |
| `go build ./...` | **PASS** | Binary builds with 0 errors |
| `go test -race -cover ./internal/handlers/... ./internal/router/... ./internal/middleware/... ./internal/database/...` | **PASS** | `handlers`: 85.0%, `router`: 91.5%, `database`: 89.7%, `middleware`: 81.2% |
| `go test -race ./...` | **PASS** | 0 data races across all packages |
| `golangci-lint run ./...` | **PASS** | 0 linter issues / gocyclo compliant |
| `gosec -quiet ./...` | **PASS** | 0 security findings |
| `govulncheck ./...` | **PASS** | 0 application module vulnerabilities |
| `python3 -m pytest --tb=no -q --ignore=tests/e2e` | **PASS** | 1130 passed in 112.58s |

---

## Key Files Modified / Created
- `internal/database/schema.sql`
- `internal/database/database.go`
- `internal/database/users.go`
- `internal/middleware/session.go`
- `internal/router/router.go`
- `internal/router/router_test.go`
- `internal/handlers/handlers.go`
- `internal/handlers/auth.go`
- `internal/handlers/auth_test.go`
- `internal/handlers/connections.go`
- `internal/handlers/connections_test.go`
- `internal/handlers/servers.go`
- `internal/handlers/servers_test.go`
- `internal/handlers/server_connections.go`
- `internal/handlers/settings.go`
- `internal/handlers/settings_test.go`
- `internal/handlers/users.go`
- `internal/handlers/edge_cases_test.go` (new)

---

## Git Policy Compliance
- **No Git Commit / Push**: In strict compliance with guidelines, no `git commit` or `git push` commands were executed. All changes reside in the workspace.
