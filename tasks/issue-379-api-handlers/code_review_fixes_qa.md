# QA Audit Report: Senior Code Review Remediation (Issue #379)

**Date**: 2026-08-29  
**Auditor**: qa_bot (Quality Gatekeeper)  
**Verdict**: **APPROVED**  
**Senior Code Review**: [`tasks/issue-379-api-handlers/CODE_REVIEW.md`](file:///home/igor/Amnezia-Web-Panel/tasks/issue-379-api-handlers/CODE_REVIEW.md)  
**Remediation Spec**: [`tasks/issue-379-api-handlers/code_review_fixes.md`](file:///home/igor/Amnezia-Web-Panel/tasks/issue-379-api-handlers/code_review_fixes.md)  
**Dev Handover**: [`tasks/issue-379-api-handlers/code_review_fixes_dev_handover.md`](file:///home/igor/Amnezia-Web-Panel/tasks/issue-379-api-handlers/code_review_fixes_dev_handover.md)  

---

## 1. Executive Summary

An independent QA audit and security verification was executed on the remediations implemented in `amnezia-web-ui-go` following the adversarial senior code review (`tasks/issue-379-api-handlers/CODE_REVIEW.md`).

All identified HIGH, MEDIUM, and LOW severity vulnerabilities and design flaws have been resolved, verified through targeted unit & concurrency regression test suites, and validated against all mandatory compilation and security gates.

---

## 2. Compilation & Scanner Verification Gates

All mandatory gates passed cleanly with zero warnings, zero data races, zero linter issues, and zero called application vulnerabilities:

| Check / Tool | Command Line | Status | Result / Notes |
|---|---|---|---|
| **Go Code Formatting** | `go fmt ./...` | **PASS** | Cleanly formatted, 0 diffs |
| **Go Vet** | `go vet ./...` | **PASS** | 0 warnings/errors |
| **Go Build** | `go build ./...` | **PASS** | Succeeded (code 0) |
| **Go Tests & Coverage** | `go test -race -cover ./internal/handlers/... ./internal/router/... ./internal/middleware/... ./internal/database/...` | **PASS** | `handlers`: **85.4%**, `router`: **91.5%**, `database`: **89.7%**, `middleware`: **81.2%** |
| **Full Go Test Suite (Race)** | `go test -race ./...` | **PASS** | 23/23 packages passed with 0 data races |
| **Go Linter** | `golangci-lint run ./...` | **PASS** | 0 linter issues |
| **Go Security Scanner** | `gosec -quiet ./...` | **PASS** | 0 security issues |
| **Vulnerability Scanner** | `govulncheck ./...` | **PASS** | 0 application module vulnerabilities |
| **Python Regression Tests** | `python3 -m pytest --tb=no -q --ignore=tests/e2e` | **PASS** | 1130 passed in 106.45s |
| **Python Formatting & Lint** | `black --check . && flake8 .` | **PASS** | 112 files clean, 0 errors |

---

## 3. Package Statement Coverage Matrix

| Package Path | Coverage | Gate Requirement | Status |
|---|---|---|---|
| `internal/handlers` | **85.4%** | $\ge 85.0\%$ | **PASS** |
| `internal/router` | **91.5%** | $\ge 85.0\%$ | **PASS** |
| `internal/database` | **89.7%** | $\ge 80.0\%$ | **PASS** |
| `internal/middleware` | **81.2%** | $\ge 80.0\%$ | **PASS** |

---

## 4. Detailed Audit & Finding Verifications

### 4.1 Sensitive Secrets Masking & Round-Trip Preservation (HIGH #1 & HIGH #4) — VERIFIED
- **Audit Findings**:
  - `GetSettingsHandler` ([`settings.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/settings.go#L16-L58)) masks `syncCfg.RemnawaveAPIKey` (`"********"`), telegram `bot_token` / `token` (`"********"`), and strips SSL `KeyText` / `CertText` (`""`).
  - `SaveSettingsHandler` ([`settings.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/settings.go#L60-L134)) invokes `preserveSecretsOnSave`, which loads existing stored SSL keys/certs, RemnaWave API key, and telegram tokens whenever incoming values are empty or equal to the mask sentinel `"********"`.
  - `persistSettings` propagates all errors from `SetSetting`, returning HTTP 500 `database_error` instead of silently discarding errors with `_ =`.
- **Regression Test**: `TestSettingsSave_PreservesSSLCertAndSecrets` in [`settings_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/settings_test.go#L307-L420) verifies masking on GET, preservation on POST round-trip, and DB persistence.

### 4.2 Username Uniqueness & First-Run Setup Race (HIGH #2) — VERIFIED
- **Audit Findings**:
  - Defined `CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username);` in [`schema.sql`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/database/schema.sql#L150).
  - Added migration in [`database.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/database/database.go#L213-L216) (`migrateUniqueUsernameIndex`).
  - In `CreateUser` ([`users.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/database/users.go#L254-L260)), UNIQUE constraint violations return `ErrUserAlreadyExists`.
  - In `APISetupHandler` ([`auth.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/auth.go#L215-L294)), check-then-act race is synchronized with `setupMu.Lock()`, returning HTTP 409 `setup_already_done` if multiple setup requests race.
  - In `AddUserHandler` ([`users.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/users.go#L193-L200)), duplicate username collisions return HTTP 400 `user_exists`.
- **Regression Test**: `TestSetupRace_UniqueAdminConstraint` in [`auth_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/auth_test.go#L424-L472) fires 10 parallel goroutines against setup; exactly 1 succeeds (200 OK) and 9 return 409 Conflict, resulting in exactly 1 admin user in the database.

### 4.3 Stateless Session Revocation & Inactive User Invalidation (HIGH #3) — VERIFIED
- **Audit Findings**:
  - In `middleware/session.go` ([`session.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/middleware/session.go#L20-L47)), introduced `UserLookupFunc` and `SetUserLookup`.
  - Wired live DB user lookup in `NewRouterWithOptions` ([`router.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/router/router.go#L98-L103)).
  - `RequireAuth`, `RequireAdmin`, and `RequireAdminOrSupport` check user existence and `Enabled == true` via `checkUserActive`. If the user has been disabled or deleted, `ClearSessionCookie` is called, and the request is rejected with HTTP 401 `unauthorized` (or redirected to `/login` for HTML pages).
- **Regression Test**: `TestDisabledUser_SessionRejected` in [`auth_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/auth_test.go#L474-L547) asserts that valid session cookies are immediately rejected with 401 once a user is disabled or deleted in the DB.

### 4.4 Mutating Handler Error Propagation (MEDIUM #1) — VERIFIED
- **Audit Findings**:
  - `RebootServerHandler` ([`servers.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L194-L197)): Checks error from SSH `nohup reboot` and returns HTTP 500 `operation_failed`.
  - `ClearServerHandler` ([`servers.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L241-L253)): Checks error from `rm -rf /opt/amnezia`, `DeleteConnectionsByServer`, and `UpdateServerProtocols`.
  - `ToggleContainerHandler` ([`servers.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L532-L542)): Checks `client.RunSudoCommand` error and returns actual requested action `req.Action` instead of fabricating `"running"`.
  - `SetClientSpeedLimitHandler` ([`servers.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L712-L719)): Propagates `awgMgr.EditClient` error.
  - `SetAWGSpeedLimitConfigHandler` ([`servers.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/servers.go#L824-L835)): Propagates `UpdateServerProtocols` and `tc.SetGlobalLimit` errors.
  - `RemoveServerConnectionHandler` & `EditServerConnectionHandler` ([`server_connections.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/server_connections.go#L403-L410)): Propagate remote protocol manager removal and DB update errors.
  - `DeleteUserHandler` & `ToggleUserHandler` ([`users.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/users.go#L388-L427)): Propagate DB deletion and update errors.
- **Regression Tests**: Verified across `TestServerHandlers`, `TestHandlers_EdgeCasesAndErrorBranches`.

### 4.5 Connection Limit TOCTOU Protection (MEDIUM #2) — VERIFIED
- **Audit Findings**:
  - Implemented per-user mutex serialization via `lockUser(userID string) func()` in `internal/handlers/handlers.go` ([`handlers.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/handlers.go#L61-L77)).
  - Synchronized `UserAddConnectionHandler` across existence checks, limit checks, client provisioning, and connection creation ([`connections.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections.go#L131-L133)).
- **Regression Test**: `TestConnectionLimit_ConcurrentAdds` in [`connections_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/connections_test.go#L639-L727) simulates 10 concurrent connection add requests against a user limit of 3. Exactly 3 connections are provisioned, preventing connection limit bypass.

### 4.6 Backup Settings Allowlist (MEDIUM #3) — VERIFIED
- **Audit Findings**:
  - `restoreBackupSettings` in `internal/handlers/settings.go` ([`settings.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/settings.go#L379-L401)) enforces an allowlist (`appearance`, `sync`, `captcha`, `telegram`, `ssl`, `limits`, `vpn_config`, `schema_version`). Arbitrary or malicious keys from uploaded backup JSONs are skipped.
- **Regression Test**: Verified in `TestSettingsSave_PreservesSSLCertAndSecrets` ([`settings_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/settings_test.go#L404-L419)).

### 4.7 Empty SecretKey Fast-Fail (MEDIUM #4) — VERIFIED
- **Audit Findings**:
  - `CaptchaHandler` and `APILoginHandler` in [`auth.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/auth.go#L81-L84) fail fast with HTTP 500 `internal_error` and detail `"Session signing key not configured"` when `cfg.SecretKey == ""` instead of returning misleading 200 responses with unpersisted session state.
- **Regression Test**: `TestEmptySecretKey_ReturnsError` in [`auth_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/auth_test.go#L600-L633).

### 4.8 Language Parameter Validation (LOW) — VERIFIED
- **Audit Findings**:
  - `SetLangHandler` in [`auth.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/auth.go#L48-L56) normalizes and validates `lang` against allowed set (`en`, `ru`), falling back to `en` on path traversal attacks, XSS payloads, or arbitrary strings.
- **Regression Test**: `TestSetLang_Validation` in [`auth_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/handlers/auth_test.go#L549-L598).

---

## 5. QA Checklist

- [x] All findings from `CODE_REVIEW.md` (HIGH #1-#4, MEDIUM #1-#4, LOW) audited and confirmed resolved
- [x] Statement coverage meets or exceeds requirements: `handlers` (85.4% $\ge 85.0\%$), `router` (91.5% $\ge 85.0\%$)
- [x] 0 data races detected under `go test -race` across the entire project
- [x] `golangci-lint` clean with 0 issues
- [x] `gosec` clean with 0 issues
- [x] `govulncheck` clean with 0 called application vulnerabilities
- [x] Full Python regression test suite passes (`1130 passed, 0 failed`)
- [x] Python formatting and lint clean (`black` and `flake8`)
- [x] No git commits or pushes performed

---

## 6. Audit Verdict

**APPROVED**

The code review remediation for Issue #379 is complete, robust, fully tested, and ready for production deployment.
