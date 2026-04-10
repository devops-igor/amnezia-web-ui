**Current approach (brittle):**
- `_api_request()` shells into the remote server, runs `docker exec telemt curl -s -X METHOD http://127.0.0.1:9091/v1/...` through SSH
- For config changes (add/toggle/remove/edit client): reads `config.toml` via SSH (`cat`), parses with regex, modifies lines manually, uploads the file back, then sends `docker kill -s HUP` to reload

**What the Telemt API already provides (robust):**
- `POST /v1/users` — create user (with secret, quota, max_ips, expiry)
- `GET /v1/users` — list users (with links, stats, octets)
- `GET /v1/users/{username}` — get single user
- `PATCH /v1/users/{username}` — update user (quota, max_ips, expiry)
- `DELETE /v1/users/{username}` — delete user
- `If-Match` header for optimistic concurrency
- All mutations atomically update config.toml + hot-reload (no SIGHUP needed, no file manipulation required)
- Links auto-generated from config (public_host, public_port already in config.toml)

**What should change:**

1. **Replace `_api_request` SSH tunnel** — Our app and telemt are in the same Docker network. Instead of `ssh → docker exec telemt curl`, the app should call `http://telemt:9091/v1/...` directly via Python `httpx`/`requests` — no SSH, no curl, no shell.

2. **Replace all config.toml manipulation** — Remove `_get_server_config`, `_parse_users_from_config`, `_insert_into_section`, `_update_line_in_section`, `save_server_config`, and the SIGHUP/restart pattern. The API handles all of this:
   - `add_client` → `POST /v1/users` (API generates secret if not provided, handles quotas/expiry)
   - `edit_client` → `PATCH /v1/users/{username}`
   - `remove_client` → `DELETE /v1/users/{username}`
   - `toggle_client` → `PATCH /v1/users/{username}` with empty/non-empty secret, or the API's comment/uncomment
   - `get_clients` → `GET /v1/users` (already partially used, but mixed with config parsing)

3. **Docker network** — Our `amnezia_panel` container needs to be on the same Docker network as `telemt`. The telemt API listens on `0.0.0.0:9091` inside its container, so `http://telemt:9091` works from another container on the same network.

4. **Config changes still need SSH for install** — `install_protocol()` still needs SSH to upload files and run docker-compose. But all runtime user management goes through the API.

**What stays:**
- SSH for `install_protocol`, `remove_container`, and initial `config.toml` setup
- The telemt `config.toml` template (for initial deployment)
- Container management via SSH

**What gets removed:**
- All raw config.toml read/write/parse methods
- `save_server_config` + SIGHUP/restart pattern for user operations
- The fragile regex-based TOML manipulation
- Shell-based `_api_request` via `docker exec curl`

This is a significant refactor. Want me to create a detailed task spec and kick off py_bot to implement it?