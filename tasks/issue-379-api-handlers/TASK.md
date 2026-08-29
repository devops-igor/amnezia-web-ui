# TASK: Phase 5 — API Handlers & Business Logic (Issue #379)

**Issue**: [#379](https://github.com/devops-igor/amnezia-web-ui/issues/379)  
**Target Package**: `amnezia-web-ui-go/internal/handlers/` and `amnezia-web-ui-go/internal/router/`  
**Primary Specifications**: 
- `docs/specs/01-domain-model.md`
- `docs/specs/02-configuration.md`
- `docs/specs/03-database.md`
- `docs/specs/04-external-services.md`
- `docs/specs/05-api-contract.md`
- `docs/plans/2026-08-25-go-rewrite.md` (Phase 5, lines 567–580)

---

## 1. Objective & Scope

Implement all HTTP API handlers and business logic for `amnezia-web-ui-go`, satisfying the full 64-endpoint API catalog defined in `docs/specs/05-api-contract.md`, and wire them to `internal/router/router.go`.

### Modules to Implement:

1. **`internal/handlers/handlers.go`**:
   - `HandlerContext` struct or dependency container holding `DB`, `Config`, `SSHManager`, `AWGManager`, `MTProxyLManager`, `DNSManager`, `VPNService`, and helper functions (JSON response writers, validation helpers, error formatters per §1.3 of `05-api-contract.md`).
2. **`internal/handlers/auth.go` (Phase 5.1)**:
   - `GET /login`, `GET /logout`, `GET /set_lang/{lang}`, `GET /api/auth/captcha`, `POST /api/auth/login`, `POST /api/auth/setup`, `POST /api/auth/change-password`.
   - Cookie management (`session`, `panel_lang`, `lang`), session store verification, brute force tracking, CAPTCHA generation/validation, initial admin setup guard.
3. **`internal/handlers/servers.go` (Phase 5.2)**:
   - `POST /api/servers/add` (and legacy `/add`), `POST /api/servers/confirm-fingerprint` (and `/confirm-fingerprint`), `POST /api/servers/{server_id}/delete` (and legacy), `POST /api/servers/{server_id}/reboot`, `POST /api/servers/{server_id}/clear`, `POST /api/servers/{server_id}/stats`, `POST /api/servers/{server_id}/check`, `POST /api/servers/{server_id}/install`, `POST /api/servers/{server_id}/uninstall`, `POST /api/servers/{server_id}/container/toggle`, `POST /api/servers/{server_id}/server_config`, `POST /api/servers/{server_id}/server_config/save`, `GET /api/servers/{server_id}/reachability`.
   - AWG speed limit endpoints: `PATCH /api/servers/{server_id}/connections/speed-limit`, `GET /api/servers/{server_id}/awg/speed-limit-config`, `PATCH /api/servers/{server_id}/awg/speed-limit-config`, `POST /api/servers/{server_id}/awg/apply-default-speed-limits`.
4. **`internal/handlers/server_connections.go` (Phase 5.2 - Connections on Server)**:
   - `GET /api/servers/{server_id}/connections`, `POST /api/servers/{server_id}/connections/add`, `POST /api/servers/{server_id}/connections/{client_id}/rotate-mimicry`, `POST /api/servers/{server_id}/connections/auto-trial`, `POST /api/servers/{server_id}/connections/kit` (generates ZIP archive with client `.conf`, `.vpn`, QR PNG), `POST /api/servers/{server_id}/connections/remove`, `POST /api/servers/{server_id}/connections/edit`, `POST /api/servers/{server_id}/connections/config`, `POST /api/servers/{server_id}/connections/toggle`, `GET /api/servers/{server_id}/{protocol}/clients`.
5. **`internal/handlers/connections.go` (Phase 5.3 - User Self-Service Connections)**:
   - `POST /api/connections/add`, `POST /api/connections/{connection_id}/config`, `POST /api/connections/{connection_id}/kit` (ZIP archive download), `POST /api/connections/{connection_id}/rename`, `POST /api/connections/{connection_id}/delete`.
6. **`internal/handlers/users.go` (Phase 5.4 - User Management)**:
   - `POST /api/users/add`, `POST /api/users/{user_id}/update`, `POST /api/users/{user_id}/delete`, `POST /api/users/{user_id}/toggle`, `POST /api/users/{user_id}/connections/add`, `GET /api/users/{user_id}/connections`, `POST /api/users/{user_id}/share/setup`.
7. **`internal/handlers/settings.go` (Phase 5.5 - Settings & Sync)**:
   - `GET /api/settings` (masks secret keys), `POST /api/settings/save`, `POST /api/settings/sync_now` (RemnaWave sync trigger), `POST /api/settings/sync_delete`, `GET /api/settings/backup/download` (JSON backup download), `POST /api/settings/backup/restore` (JSON backup restore).
8. **`internal/handlers/share.go` (Phase 5.6 - Public Share Links)**:
   - `GET /share/{token}`, `POST /api/share/{token}/auth`, `GET /api/share/{token}/connections`, `POST /api/share/{token}/config/{connection_id}`.
9. **`internal/handlers/leaderboard.go` & `pages.go` (Phase 5.7 - Leaderboard & Page Views)**:
   - `GET /api/leaderboard?period=all-time|monthly`, template page handlers (`GET /`, `GET /users`, `GET /server/{server_id}`, `GET /settings`, `GET /my`, `GET /setup`, `GET /change-password`, `GET /leaderboard`).
10. **`internal/handlers/vpn.go` (Phase 5.8 - VPN Subsystem)**:
    - `GET /api/vpn/status`, `GET /api/vpn/backends`, `POST /api/vpn/backends/{server_id}/enable`, `POST /api/vpn/backends/{server_id}/disable`, `GET /api/vpn/tunnels`, `GET /api/vpn/config`, `POST /api/vpn/config`, `GET /api/vpn/my-connection`, `GET /api/vpn/my-config`, `POST /api/vpn/disconnect`.
11. **`internal/handlers/system.go` (Phase 5.10 - System APIs)**:
    - `GET /api/health`, `GET /api/version`.
12. **`internal/router/router.go` Wiring**:
    - Update `NewRouter` to accept handler context / dependencies and mount all endpoints with appropriate middlewares (`RequireAuth`, `RequireAdminOrSupport`, `CSRF`, `RateLimit`).

---

## 2. Technical Constraints & Design Principles

- **Error Format**: Strictly adhere to `05-api-contract.md §1.3` (JSON with `{"error": "<code_snake_case>", "detail": "<human description>"}`).
- **CSRF & Auth**: Respect CSRF exemptions (`/api/auth/login`, `/api/auth/setup`, `/api/share/{token}/auth`) and enforcement on all mutating routes.
- **Audit Logging**: Use `middleware.LogAuditEvent` or database audit helper on state-changing actions (server add/delete, user toggle/delete, config edit, etc.).
- **Concurrency & Thread Safety**: All handlers must be thread-safe. Avoid global mutable state.
- **Zip / Attachment Generation**: Connection kit ZIP files must contain valid `.conf` text files and metadata.

---

## 3. Mandatory Quality Gate & Acceptance Criteria

1. **Compilation Gate**:
   ```bash
   go fmt ./...
   go vet ./...
   go build ./...
   go test -race ./...
   golangci-lint run ./...
   gosec ./...
   govulncheck ./...
   ```
2. **Statement Coverage**: Minimum $\ge 85\%$ statement coverage across all new handler packages (`internal/handlers`, `internal/router`).
3. **Regression Safety**: All 1,130 Python tests in root test suite must pass without regressions.
4. **DEV_HANDOVER.md**: Complete handoff output written to `tasks/issue-379-api-handlers/DEV_HANDOVER.md`.
5. **No git commit/push**: Handoff directly to orchestrator `pm_bot`.
