# DEV_HANDOVER — Telemt Task 4

## Files Changed

| File | Description | LOC Changed |
|------|-------------|-------------|
| `telemt_manager.py` | Complete refactor: replaced SSH-tunneled curl with direct httpx calls; replaced all config.toml manipulation with REST API calls; removed 5 deprecated methods | ~445 lines (net -28 lines) |
| `tests/test_telemt_manager.py` | New test file: 51 tests covering all refactored methods, API client, error paths, and verification that deprecated methods are removed | +680 lines (new) |
| `telemt_task_4/WORKLOG.md` | Updated progress tracking | Updated |

## Coverage

```
Name                Stmts   Miss  Cover   Missing
-------------------------------------------------
telemt_manager.py     215     44    80%   149-233, 383, 408-411
-------------------------------------------------
TOTAL                 215     44    80%
```

Missing coverage is in:
- `install_protocol()` (lines 149-233) — unchanged, still uses SSH (out of scope)
- Error logging branches in `remove_client()` and `toggle_client()` — only hit when API returns errors

## Test Results

```
============================== 51 passed in 0.86s ==============================
```

All 51 tests pass. See `tests/test_telemt_manager.py` for full test suite.

## Linter Output

```
black: reformatted 2 files (telemt_manager.py, tests/test_telemt_manager.py)
flake8: no errors
py_compile: Syntax OK
```

## Security Audit (pip-audit)

No vulnerabilities found in project dependencies (httpx, paramiko, etc.). System package CVEs (pip, setuptools, etc.) are unrelated to this project.

## Summary of Changes

### What Changed

1. **`_api_request()`** — Replaced SSH-tunneled `docker exec telemt curl` with direct `httpx.Client` call to `http://telemt:9091/v1/...`

2. **`get_clients()`** — Now uses only `GET /v1/users` API response. No more config.toml parsing or regex extraction.

3. **`add_client()`** — Uses `POST /v1/users` with JSON body. No more config line insertion.

4. **`edit_client()`** — Uses `PATCH /v1/users/{username}` with JSON body. No more regex replace in config.

5. **`remove_client()`** — Uses `DELETE /v1/users/{username}`. No more config file manipulation.

6. **`toggle_client()`** — Uses `PATCH /v1/users/{username}` with `{"secret": ""}` to disable or `{"secret": "<32-hex>"}` to enable.

7. **`get_client_config()`** — Uses `GET /v1/users/{username}` to get links directly from API response.

8. **`get_server_status()`** — Added `_get_telemt_params_from_api()` using `GET /v1/health` and `GET /v1/system/info`.

### What Was Removed

- `_get_server_config()` — SSH `cat config.toml`
- `_parse_users_from_config()` — regex TOML parsing
- `_insert_into_section()` — config line insertion
- `_update_line_in_section()` — config line update
- `save_server_config()` — SSH upload + SIGHUP/restart

### What Stayed the Same

- `install_protocol()` — Still uses SSH for file upload and docker-compose
- `remove_container()` — Still uses SSH for cleanup
- `check_docker_installed()` — Still uses SSH for version check
- `check_protocol_installed()` — Still uses SSH for container check
- All public method signatures — Same inputs/outputs
- Return value shapes — Backward compatible

## Notes for QA

### Edge Cases Covered

1. **Empty API response** — `get_clients()` returns `[]`
2. **API connection error** — `_api_request()` returns `None`, callers handle gracefully
3. **Invalid JSON** — `_api_request()` logs error and returns `None`
4. **Disabled user** — User with empty secret shows `enabled=False`
5. **Quota exceeded** — Auto-disables user via API PATCH
6. **Link priority** — TLS > Secure > Classic
7. **Username sanitization** — Non-alphanumeric chars stripped
8. **Empty username** — Falls back to `user_<random>`

### Design Decisions

1. **Enable/disable via secret** — API has no explicit enable/disable flag. Empty secret disables, non-empty enables.
2. **restart parameter ignored** — `toggle_client(protocol_type, client_id, enable, restart=False)` accepts `restart` for backward compatibility but it has no effect since the API applies changes atomically.
3. **Health endpoint fallback** — `_get_telemt_params_from_api()` returns default params (tls_emulation=True, tls_domain="", max_connections=0) when API is unavailable.

### Known Limitations

1. **Docker network requirement** — The `amnezia_panel` container must be on the same Docker network as `telemt` for `http://telemt:9091` to resolve.
2. **API must be enabled** — The telemt service must have `[server.api] enabled = true` in its config.
3. **No If-Match concurrency** — The implementation does not use `If-Match` headers for optimistic concurrency. This is acceptable for user management but may cause issues under high concurrent mutation.
