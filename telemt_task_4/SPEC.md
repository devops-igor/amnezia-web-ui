# Telemt Task 4: Replace SSH/API Tunnel with Direct HTTP API

## Problem Statement

The current `TelemtManager` uses a brittle approach:
1. `_api_request()` SSH-tunnels into the remote server and runs `docker exec telemt curl ...` — unnecessary since both containers are on the same Docker network
2. All user mutations (add/edit/remove/toggle) manipulate `config.toml` via regex, upload via SSH, then `docker kill -s HUP` to reload — error-prone and fragile

The telemt service already exposes a full REST API (`http://telemt:9091/v1/...`) that handles all of this atomically.

---

## What Changes

### Communication: SSH Tunnel → Direct HTTP

**Before:** `docker exec telemt curl -s -X METHOD http://127.0.0.1:9091/v1/...` via SSH
**After:** Python `httpx`/`requests` call to `http://telemt:9091/v1/...` directly (containers on same Docker network)

### User Management: config.toml manipulation → API calls

| Operation | Before (brittle) | After (robust) |
|---|---|---|
| `get_clients` | `GET /v1/users` (API) + `_parse_users_from_config` (config) | `GET /v1/users` (API only) |
| `add_client` | Insert line into `config.toml`, save + SIGHUP | `POST /v1/users` |
| `edit_client` | Regex replace in `config.toml`, save + SIGHUP | `PATCH /v1/users/{username}` |
| `remove_client` | Remove lines from `config.toml`, save + SIGHUP | `DELETE /v1/users/{username}` |
| `toggle_client` | Comment/uncomment line in `config.toml`, save + SIGHUP | `PATCH /v1/users/{username}` (empty vs non-empty secret) |
| `get_client_config` | Read config + construct link manually | `GET /v1/users/{username}` (API provides `links`) |

### What Stays the Same

- SSH for `install_protocol()` — still needs to upload files and run docker-compose
- SSH for `remove_container()` — cleanup via SSH
- Container management (docker commands) via SSH
- The `config.toml` template for initial deployment

### What Gets Removed

- `_api_request()` — shell-based SSH tunnel
- `_get_server_config()` — raw config read via SSH
- `_parse_users_from_config()` — regex TOML parsing
- `_insert_into_section()` — config line insertion
- `_update_line_in_section()` — config line update
- `save_server_config()` — upload + SIGHUP/restart pattern
- All config.toml manipulation for user operations

---

## API Mapping

### Endpoints Used

| Method | Path | Used For |
|---|---|---|
| `GET` | `/v1/health` | Health check |
| `GET` | `/v1/users` | List all users with stats/links |
| `POST` | `/v1/users` | Create user |
| `GET` | `/v1/users/{username}` | Get single user (for link) |
| `PATCH` | `/v1/users/{username}` | Update user (quota, max_ips, expiry, enable/disable) |
| `DELETE` | `/v1/users/{username}` | Delete user |

### Request/Response Contracts

**CreateUserRequest:**
```json
{
  "username": "string",        // required, [A-Za-z0-9_.-], 1..64
  "secret": "string",           // optional, 32 hex chars, auto-generated if missing
  "data_quota_bytes": 123456,   // optional, u64
  "max_unique_ips": 3,          // optional, usize
  "expiration_rfc3339": "..."   // optional, RFC3339
}
```

**PatchUserRequest:**
```json
{
  "secret": "string",           // optional, 32 hex chars
  "data_quota_bytes": 123456,   // optional, u64
  "max_unique_ips": 3,          // optional, usize
  "expiration_rfc3339": "..."   // optional, RFC3339
}
```

**Success Envelope:**
```json
{
  "ok": true,
  "data": { ... },
  "revision": "sha256-hex"
}
```

**Error Envelope:**
```json
{
  "ok": false,
  "error": { "code": "...", "message": "..." },
  "request_id": 1
}
```

### Enable/Disable Logic

The API doesn't have an explicit enable/disable flag. Based on API semantics:
- **Disable:** `PATCH` with empty secret (`""`) or remove secret field
- **Enable:** `PATCH` with a valid 32-char hex secret

Need to verify: does `secret: ""` disable, or does omitting the secret field keep current value? API spec says PATCH "updates only provided fields". So to disable, we need to explicitly set secret to empty string.

**Alternative approach:** The issue mentions "comment/uncomment" — in TOML config, users starting with `#` are disabled. The API likely handles this by treating empty secret as disabled.

---

## Docker Network Requirement

The `amnezia_panel` container must be on the same Docker network as `telemt`. Since they're in the same `docker-compose` setup, they should share a network by default. However:

1. Check if `amnezia_panel` and `telemt` share a network in the deployed setup
2. If not, the `docker-compose.yml` for the deployment (not this repo's dev setup) needs updating
3. The telemt API listens on `0.0.0.0:9091` inside its container — `http://telemt:9091` from another container on the same network

**Note:** The included `docker-compose.yml` is for the panel itself — telemt runs on the VPN server (SSH target), not locally. The Docker network consideration applies to the deployed server's infrastructure.

---

## Implementation Details

### HTTP Client

Use `httpx` (preferred for async support) or `requests`. Add to `requirements.txt` if not present.

```python
import httpx

class TelemtManager:
    API_BASE = "http://telemt:9091"
    
    def _api_request(self, method, path, data=None):
        with httpx.Client(timeout=10.0) as client:
            url = f"{self.API_BASE}{path}"
            resp = client.request(method, url, json=data)
            return resp.json()
```

### Error Handling

The API returns structured errors. Map to appropriate exceptions:

| HTTP | error.code | Action |
|---|---|---|
| 400 | `bad_request` | Log and raise ValueError |
| 401 | `unauthorized` | Log and raise AuthError |
| 403 | `forbidden` | Log and raise PermissionError |
| 404 | `not_found` | Return None or raise KeyError |
| 409 | `user_exists` | Catch and return friendly error |
| 409 | `revision_conflict` | Retry with fresh revision |
| 500 | `internal_error` | Log and raise RuntimeError |
| 503 | `api_disabled` | Log and raise ServiceUnavailableError |

### If-Match / Optimistic Concurrency

The API supports `If-Match: <revision>` header for atomic updates. For v1, we can ignore revision checking (not strictly needed for user management). The revision is returned in every response envelope as `revision`.

### Health Check

`GET /v1/health` returns `{"ok": true, "data": {"status": "ok", "read_only": bool}, "revision": "..."}`. Use this for `check_protocol_installed()` and status checks.

---

## Files to Modify

1. **`telemt_manager.py`** — Replace all SSH-based API calls with direct HTTP; remove config.toml manipulation methods
2. **`requirements.txt`** — Add `httpx` if not present

---

## Backward Compatibility

The public API of `TelemtManager` stays the same:
- `__init__(ssh_manager, config_dir)`
- `check_docker_installed()`
- `check_protocol_installed()`
- `get_server_status(protocol_type)`
- `install_protocol(...)`
- `remove_container(protocol_type)`
- `get_clients(protocol_type)` — returns same shape
- `add_client(...)` — returns same shape
- `edit_client(...)` — same params
- `remove_client(...)`
- `toggle_client(...)`
- `get_client_config(...)`

Only internal implementation changes.

---

## Testing Approach

1. **Unit tests** for API client methods (mock httpx)
2. **Integration tests** if telemt container is available (docker-compose with telemt)
3. **Smoke test:** `GET /v1/health` returns `{"ok": true}`
4. **CRUD test:** create → get → edit → delete a test user

---

## Migration Path

1. Add `httpx` to requirements
2. Implement new `_api_request()` using httpx
3. Replace each user management method one-by-one
4. Remove deprecated methods only after all callers are updated
5. Test thoroughly before removing SSH dependency for user operations
