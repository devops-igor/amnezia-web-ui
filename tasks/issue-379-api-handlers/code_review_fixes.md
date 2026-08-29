# Sub-Task: Code Review Security & Parity Remediation (Issue #379)

## Context & Objectives
Following the adversarial senior code review in `tasks/issue-379-api-handlers/CODE_REVIEW.md`, `dev_bot` must resolve the identified HIGH, MEDIUM, and LOW findings across `amnezia-web-ui-go`.

---

## Required Changes

### 1. Sensitive Secrets Masking & Round-Trip Preservation (HIGH #1 & HIGH #4)
- **`GetSettingsHandler` (`internal/handlers/settings.go`)**:
  - Mask `syncCfg.RemnawaveAPIKey` if non-empty (e.g. `"********"`).
  - Mask `telegramCfg["bot_token"]` / `token` if present.
- **`SaveSettingsHandler` (`internal/handlers/settings.go`)**:
  - When saving `ssl`: if `req.SSL.KeyText == ""` and `req.SSL.CertText == ""`, load existing `ssl` settings from DB and retain the stored `KeyText` and `CertText`.
  - When saving `sync`: if `req.Sync.RemnawaveAPIKey == ""` or equals `"********"`, load existing `sync` settings and retain the stored `RemnawaveAPIKey`.
  - When saving `telegram`: retain existing `bot_token` if empty or masked.
  - Propagate database errors from `SetSetting` instead of discarding with `_ =`.

### 2. Username Uniqueness & First-Run Setup Race (HIGH #2)
- **`internal/database/schema.sql` & `internal/database/`**:
  - Update `idx_users_username` to be a `UNIQUE` index:
    `CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username);`
  - In `internal/database/database.go` or migration, ensure the unique index is applied.
  - In `CreateUser`: if SQLite returns a UNIQUE constraint error on `username`, return an appropriate duplicate user error.
- **`internal/handlers/auth.go` (`APISetupHandler`)**:
  - Handle unique constraint / duplicate admin creation gracefully if racing requests occur.
- **`internal/handlers/users.go` (`AddUserHandler`)**:
  - Handle duplicate username error gracefully if concurrent adds race.

### 3. Session Revocation & Disabled/Deleted User Checks (HIGH #3)
- **`internal/middleware/session.go` / `internal/router/router.go`**:
  - `RequireAuth`, `RequireAdmin`, and `RequireAdminOrSupport` must verify that the authenticated user still exists in the database and `Enabled == true` (using a user lookup function or DB reference).
  - If the user does not exist or `Enabled == false`, reject the request with HTTP 401 `unauthorized` (or redirect to `/login` for HTML routes) and clear the session cookie.

### 4. Robust Error Propagation on Mutating Handlers (MEDIUM #1)
- In `internal/handlers/servers.go`, `server_connections.go`, `settings.go`, `users.go`:
  - Check errors from remote execution (`RunSudoCommand`), protocol manager calls (`awgMgr.EditClient`, `tc.SetGlobalLimit`), and database mutations.
  - Return HTTP 500 `operation_failed` (or appropriate status) when primary operations fail instead of silently returning `{"status":"ok"}`.

### 5. Connection Limit TOCTOU Race Protection (MEDIUM #2)
- In `internal/handlers/connections.go`:
  - Synchronize connection limit check and connection creation per user (e.g. using a mutex/sync map) to prevent concurrent requests from bypassing max connection limits.

### 6. Backup Settings Allowlist (MEDIUM #3)
- In `internal/handlers/settings.go` (`restoreBackupSettings`):
  - Allowlist only valid settings keys (`appearance`, `sync`, `captcha`, `telegram`, `ssl`, `limits`). Ignore unexpected or arbitrary keys.

### 7. Empty SecretKey Fast-Fail (MEDIUM #4)
- In `internal/handlers/auth.go` (`CaptchaHandler`, `APILoginHandler`):
  - If `cfg.SecretKey == ""`, return HTTP 500 `internal_error` with detail `"Session signing key not configured"`.

### 8. Language Parameter Validation (LOW)
- In `internal/handlers/auth.go` (`SetLangHandler`):
  - Validate `lang` against allowed languages (`en`, `ru`). Fall back to `en` or return 400 for invalid/malicious values.

### 9. Regression Test Suite Expansion
- Add tests in `settings_test.go`, `users_test.go`, `auth_test.go`, `connections_test.go`:
  - `TestSettingsSave_PreservesSSLCertAndSecrets`: verifies masking on GET and preservation on POST.
  - `TestSetupRace_UniqueAdminConstraint`: parallel setup requests result in exactly one admin.
  - `TestDisabledUser_SessionRejected`: session cookie for disabled user is rejected by middleware.
  - `TestConnectionLimit_ConcurrentAdds`: parallel adds cannot exceed max connection limit.
  - `TestSetLang_Validation`: valid and invalid language parameters.
  - `TestEmptySecretKey_ReturnsError`: fast-fail on unconfigured secret key.

---

## Compilation Gate (HARD MANDATORY GATE)
Run from `amnezia-web-ui-go/`:
```bash
go fmt ./...
go vet ./...
go build ./...
go test -race -cover ./internal/handlers/... ./internal/router/... ./internal/middleware/... ./internal/database/...
golangci-lint run ./...
gosec -quiet ./...
govulncheck ./...
```
- Test coverage across `internal/handlers` and `internal/router` MUST remain `>= 85.0%`.
- 0 data races.
- 0 lint issues.
- All Go tests (`go test -race ./...`) and Python non-e2e tests (`python3 -m pytest --tb=no -q --ignore=tests/e2e` from repo root) must pass.

---

## Deliverables & Handover
- Emit developer handover strictly to:
  `tasks/issue-379-api-handlers/code_review_fixes_dev_handover.md`
- Append `IMPLEMENTATION_COMPLETE` entry to `WORKLOG.md`.
- Hand back to `pm_bot` for QA delegation.
- **DO NOT commit or push git changes.**
