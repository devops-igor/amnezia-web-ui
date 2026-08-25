# API Contract Specification (`05-api-contract.md`)

> **Target Packages:** `internal/router/*`, `internal/middleware/*`  
> **Source Python Files:** `app/routers/*`  
> **Router:** `github.com/go-chi/chi/v5`  
> **Status:** Ground Truth Specification for Go Rewrite

---

## 1. Authentication & Security Middleware Rules

### 1.1 Auth Protection Levels
1. **Public:** No authentication required (`/login`, `/setup`, `/api/auth/*`, `/set_lang/*`, `/share/*`, static assets).
2. **Session (Any Authenticated User):** Valid session cookie (`session`) required (`/my`, `/api/connections/*`, `/logout`, `/api/vpn/my-*`).
3. **Admin Only:** Session cookie must have `role == "admin"` (`/`, `/users`, `/server/*`, `/settings`, `/api/servers/*`, `/api/users/*`, `/api/settings/*`, `/api/vpn/backends/*`).
4. **Share Token:** Valid token in URL path and matching share session if password protected (`/share/{token}`, `/api/share/{token}/*`).

### 1.2 CSRF Protection Rules
- **Enforced:** All state-mutating requests (`POST`, `PUT`, `PATCH`, `DELETE`) submitted from browser forms or AJAX require double-submit cookie validation:
  - Cookie: `csrftoken`
  - Header: `X-CSRF-Token` or form field `csrf_token`
- **Exemptions:**
  - `POST /api/auth/login` (Uses credentials + optional CAPTCHA)
  - `POST /api/auth/setup` (Initial setup wizard)
  - `POST /api/share/{token}/auth` (Public share password submission)

### 1.3 Standard Error Responses

```json
// Standard 400/422 Validation Error
{
  "error": "validation_failed",
  "detail": "Username must be between 3 and 255 characters"
}

// Standard 401 Unauthorized
{
  "error": "unauthorized",
  "detail": "Authentication required"
}

// Standard 403 Forbidden
{
  "error": "forbidden",
  "detail": "Admin privileges required"
}

// Standard 404 Not Found
{
  "error": "not_found",
  "detail": "Resource not found"
}

// Standard 500 Internal Error (Sanitized)
{
  "error": "internal_error",
  "detail": "An unexpected error occurred"
}
```

---

## 2. Complete Endpoint Catalog (64 Endpoints)

### 2.1 Auth Router (`internal/router/auth`)

| Method | Path | Auth | CSRF | Request Payload | Success Status | Response Body |
|--------|------|------|------|-----------------|----------------|---------------|
| `GET` | `/login` | Public | No | None | 200 | HTML (`login.html`) or 302 Redirect to `/` |
| `GET` | `/logout` | Public | No | None | 302 | Clears cookie; redirect to `/login` |
| `GET` | `/set_lang/{lang}` | Public | No | Path: `lang` | 302 | Sets `panel_lang` cookie; redirect to Referer |
| `GET` | `/api/auth/captcha` | Public | No | None | 200 | `{"captcha_id": "...", "image": "data:image/png;base64,..."}` |
| `POST` | `/api/auth/login` | Public | Exempt | `models.LoginRequest` | 200 | `{"status": "ok", "redirect": "/"}` + Sets `session` |
| `POST` | `/api/auth/setup` | Public | Exempt | `models.SetupRequest` | 200 | `{"status": "ok", "redirect": "/"}` + Creates Admin |
| `POST` | `/api/auth/change-password` | Session | Required | `models.ChangePasswordRequest`| 200 | `{"status": "ok", "message": "Password updated"}` |

---

### 2.2 Server Management Router (`internal/router/servers`)

| Method | Path | Auth | CSRF | Request Payload | Success Status | Response Body |
|--------|------|------|------|-----------------|----------------|---------------|
| `GET` | `/` | Admin | No | None | 200 | HTML (`index.html`) |
| `POST` | `/add` | Admin | Required | `models.AddServerRequest` | 200 | `{"status": "ok", "server_id": 1}` or `{"fingerprint_required": true, "fingerprint": "..."}` |
| `POST` | `/confirm-fingerprint` | Admin | Required | `models.ConfirmFingerprintRequest` | 200 | `{"status": "ok", "server_id": 1}` |
| `POST` | `/{server_id}/delete` | Admin | Required | None | 200 | `{"status": "ok"}` |
| `POST` | `/{server_id}/reboot` | Admin | Required | None | 200 | `{"status": "ok"}` |
| `POST` | `/{server_id}/clear` | Admin | Required | None | 200 | `{"status": "ok"}` |
| `POST` | `/{server_id}/stats` | Admin | Required | None | 200 | `models.ServerStatsResponse` |
| `POST` | `/{server_id}/check` | Admin | Required | None | 200 | `models.ServerCheckResponse` |
| `POST` | `/{server_id}/install` | Admin | Required | `models.InstallProtocolRequest` | 200 | `{"status": "ok", "protocol": "awg"}` |
| `POST` | `/{server_id}/uninstall` | Admin | Required | `models.ProtocolRequest` | 200 | `{"status": "ok"}` |
| `POST` | `/{server_id}/container/toggle` | Admin | Required | `models.ProtocolRequest` + Form/JSON `action` | 200 | `{"status": "ok", "state": "running"}` |
| `POST` | `/{server_id}/server_config` | Admin | Required | `models.ProtocolRequest` | 200 | `{"status": "ok", "config": "..."}` |
| `POST` | `/{server_id}/server_config/save` | Admin | Required | `models.ServerConfigSaveRequest` | 200 | `{"status": "ok"}` |
| `GET` | `/{server_id}/connections` | Admin | No | None | 200 | `{"status": "ok", "connections": [...]}` |
| `POST` | `/{server_id}/connections/add` | Admin | Required | `models.AddConnectionRequest` | 200 | `{"status": "ok", "connection": {...}}` |
| `POST` | `/{server_id}/connections/{client_id}/rotate-mimicry` | Admin | Required | `models.ProtocolRequest` | 200 | `{"status": "ok", "mimicry": "quic"}` |
| `GET` | `/{server_id}/reachability` | Admin | No | Query: `protocol` | 200 | `{"reachable": true, "latency_ms": 42}` |
| `POST` | `/{server_id}/connections/auto-trial` | Admin | Required | `models.AutoTrialRequest` | 200 | `{"status": "ok", "results": {...}}` |
| `POST` | `/{server_id}/connections/kit` | Admin | Required | Form/JSON `client_id`, `protocol` | 200 | Binary ZIP file attachment (`connection-kit.zip`) |
| `POST` | `/{server_id}/connections/remove` | Admin | Required | `models.ConnectionActionRequest` | 200 | `{"status": "ok"}` |
| `POST` | `/{server_id}/connections/edit` | Admin | Required | `models.EditConnectionRequest` | 200 | `{"status": "ok"}` |
| `POST` | `/{server_id}/connections/config` | Admin | Required | `models.ConnectionActionRequest` | 200 | `{"status": "ok", "config": "...", "filename": "client.conf"}` |
| `POST` | `/{server_id}/connections/toggle` | Admin | Required | `models.ToggleConnectionRequest` | 200 | `{"status": "ok", "enabled": true}` |
| `GET` | `/{server_id}/{protocol}/clients` | Admin | No | Path params | 200 | `{"status": "ok", "clients": [...]}` |
| `PATCH`| `/{server_id}/connections/speed-limit` | Admin | Required | `models.SpeedLimitRequest` | 200 | `{"status": "ok"}` |
| `GET` | `/{server_id}/awg/speed-limit-config` | Admin | No | None | 200 | `models.AwgSpeedLimitConfigRequest` |
| `PATCH`| `/{server_id}/awg/speed-limit-config` | Admin | Required | `models.AwgSpeedLimitConfigRequest` | 200 | `{"status": "ok"}` |
| `POST` | `/{server_id}/awg/apply-default-speed-limits` | Admin | Required | None | 200 | `{"status": "ok", "updated": 5}` |

---

### 2.3 User Connections Router (`internal/router/connections`)

| Method | Path | Auth | CSRF | Request Payload | Success Status | Response Body |
|--------|------|------|------|-----------------|----------------|---------------|
| `POST` | `/add` | Session | Required | `models.MyAddConnectionRequest` | 200 | `{"status": "ok", "connection": {...}}` |
| `POST` | `/{connection_id}/config` | Session | Required | None | 200 | `{"status": "ok", "config": "...", "filename": "..."}` |
| `POST` | `/{connection_id}/kit` | Session | Required | None | 200 | Binary ZIP file attachment |
| `POST` | `/{connection_id}/rename` | Session | Required | `models.RenameConnectionRequest` | 200 | `{"status": "ok", "name": "New Name"}` |
| `POST` | `/{connection_id}/delete` | Session | Required | None | 200 | `{"status": "ok"}` |

---

### 2.4 User Management Router (`internal/router/users`)

| Method | Path | Auth | CSRF | Request Payload | Success Status | Response Body |
|--------|------|------|------|-----------------|----------------|---------------|
| `POST` | `/add` | Admin | Required | `models.AddUserRequest` | 200 | `{"status": "ok", "user_id": "uuid"}` |
| `POST` | `/{user_id}/update` | Admin | Required | `models.UpdateUserRequest` | 200 | `{"status": "ok"}` |
| `POST` | `/{user_id}/delete` | Admin | Required | None | 200 | `{"status": "ok"}` |
| `POST` | `/{user_id}/toggle` | Admin | Required | `models.ToggleUserRequest` | 200 | `{"status": "ok", "enabled": false}` |
| `POST` | `/{user_id}/connections/add` | Admin | Required | `models.AddUserConnectionRequest` | 200 | `{"status": "ok", "connection": {...}}` |
| `GET` | `/{user_id}/connections` | Admin | No | None | 200 | `{"status": "ok", "connections": [...]}` |

---

### 2.5 Settings & Sync Router (`internal/router/settings`)

| Method | Path | Auth | CSRF | Request Payload | Success Status | Response Body |
|--------|------|------|------|-----------------|----------------|---------------|
| `GET` | `/settings` | Admin | No | None | 200 | HTML (`settings.html`) |
| `GET` | `/api/settings` | Admin | No | None | 200 | `models.SaveSettingsRequest` (secrets stripped) |
| `POST` | `/api/settings/save` | Admin | Required | `models.SaveSettingsRequest` | 200 | `{"status": "ok"}` |
| `POST` | `/api/settings/sync_now` | Admin | Required | None | 200 | `{"status": "ok", "synced_users": 12}` |
| `POST` | `/api/settings/sync_delete` | Admin | Required | None | 200 | `{"status": "ok", "deleted": 3}` |
| `GET` | `/api/settings/backup/download` | Admin | No | None | 200 | JSON file download (`amnezia_backup.json`) |
| `POST` | `/api/settings/backup/restore` | Admin | Required | Multipart file upload | 200 | `{"status": "ok", "message": "Restored"}` |

---

### 2.6 Public Share Router (`internal/router/share`)

| Method | Path | Auth | CSRF | Request Payload | Success Status | Response Body |
|--------|------|------|------|-----------------|----------------|---------------|
| `POST` | `/api/users/{user_id}/share/setup` | Admin | Required | `models.ShareSetupRequest` | 200 | `{"status": "ok", "share_token": "..."}` |
| `GET` | `/share/{token}` | Public | No | Path: `token` | 200 | HTML (`user_share.html`) |
| `POST` | `/api/share/{token}/auth` | Public | Exempt | `models.ShareAuthRequest` | 200 | `{"status": "ok"}` + Sets share session cookie |
| `GET` | `/api/share/{token}/connections` | Share Token | No | Path: `token` | 200 | `{"status": "ok", "connections": [...]}` |
| `POST` | `/api/share/{token}/config/{connection_id}` | Share Token | Required | None | 200 | `{"status": "ok", "config": "...", "filename": "..."}` |

---

### 2.7 Pages & Leaderboard Routers (`internal/router/pages` & `leaderboard`)

| Method | Path | Auth | CSRF | Request Payload | Success Status | Response Body |
|--------|------|------|------|-----------------|----------------|---------------|
| `GET` | `/setup` | Public | No | None | 200 | HTML (`setup.html`) |
| `GET` | `/change-password` | Session | No | None | 200 | HTML (`change_password.html`) |
| `GET` | `/server/{server_id}`| Admin | No | Path: `server_id` | 200 | HTML (`server.html`) |
| `GET` | `/users` | Admin | No | None | 200 | HTML (`users.html`) |
| `GET` | `/my` | Session | No | None | 200 | HTML (`my_connections.html`) |
| `GET` | `/leaderboard` | Public | No | None | 200 | HTML (`leaderboard.html`) |
| `GET` | `/api/leaderboard` | Public | No | Query: `period` (`all-time` \| `monthly`) | 200 | `models.LeaderboardResponse` |

---

### 2.8 VPN Subsystem Router (`internal/router/vpn`) — NEW (Phase 4E & 5.8)

| Method | Path | Auth | CSRF | Request Payload | Success Status | Response Body |
|--------|------|------|------|-----------------|----------------|---------------|
| `GET` | `/api/vpn/status` | Admin | No | None | 200 | `{"listener_running": true, "active_tunnels": 3, "connected_sessions": 45, "rx_bytes": 1024, "tx_bytes": 2048}` |
| `GET` | `/api/vpn/backends` | Admin | No | None | 200 | `{"backends": [models.BackendTunnel]}` |
| `POST` | `/api/vpn/backends/{server_id}/enable` | Admin | Required | Path: `server_id` | 200 | `{"status": "ok"}` |
| `POST` | `/api/vpn/backends/{server_id}/disable`| Admin | Required | Path: `server_id` | 200 | `{"status": "ok", "draining": true}` |
| `GET` | `/api/vpn/tunnels` | Admin | No | None | 200 | `{"tunnels": [models.BackendTunnel]}` |
| `GET` | `/api/vpn/config` | Admin | No | None | 200 | `models.VPNConfig` |
| `POST` | `/api/vpn/config` | Admin | Required | `models.VPNConfig` | 200 | `{"status": "ok"}` |
| `GET` | `/api/vpn/my-connection` | Session | No | None | 200 | `models.VPNSession` |
| `GET` | `/api/vpn/my-config` | Session | No | None | 200 | `{"status": "ok", "config": "...", "filename": "portal-awg.conf"}` |
| `POST` | `/api/vpn/disconnect` | Admin | Required | Body: `{"session_id": "..."}` | 200 | `{"status": "ok"}` |
