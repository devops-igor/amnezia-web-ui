# TASK-05 — Use API-Provided `tg://` Links Instead of Manual Construction

## Objective

Fix two places in `telemt_manager.py` that manually construct `tg://` proxy links instead of using the links already returned by the API.

## Problem Statement

The telemt API returns complete, ready-to-use `tg://` links in `resp["data"]["links"]`. However, two methods ignore this and build links manually from raw host/port/secret values — resulting in truncated/incorrect secrets.

## Files to Modify

- `/home/igor/Amnezia-Web-Panel/telemt_manager.py`

## Specific Changes

### 1. `add_client()` — Use API link from `resp["data"]["links"]`

**Current code (lines 341-344):**
```python
data = resp.get("data", {})
secret = data.get("secret", "")
link = f"tg://proxy?server={host}&port={port}&secret={secret}"
return {"client_id": username, "config": link, "vpn_link": link}
```

**Replace with:**
```python
data = resp.get("data", {})
links = data.get("links", {})

# Use the link from the API (it has correct server IP, port, and secret)
if links.get("tls"):
    return {"client_id": username, "config": links["tls"][0], "vpn_link": links["tls"][0]}
elif links.get("secure"):
    return {"client_id": username, "config": links["secure"][0], "vpn_link": links["secure"][0]}
elif links.get("classic"):
    return {"client_id": username, "config": links["classic"][0], "vpn_link": links["classic"][0]}

# No link from API — return empty
return {"client_id": username, "config": "", "vpn_link": ""}
```

### 2. `get_client_config()` — Remove manual fallback construction

**Current fallback code (lines 436-442):**
```python
# Fallback: search in all clients
clients = self.get_clients(protocol_type)
client = next((c for c in clients if c["clientId"] == client_id), None)
if client:
    secret = client.get("userData", {}).get("token", "")
    if secret:
        return f"tg://proxy?server={host}&port={port}&secret={secret}"
return "Not found"
```

**Replace with:**
```python
# Fallback: search in all clients
clients = self.get_clients(protocol_type)
client = next((c for c in clients if c["clientId"] == client_id), None)
if client:
    tg_link = client.get("userData", {}).get("tg_link", "")
    if tg_link:
        return tg_link
return ""
```

Note: The direct API call path (`_api_request("GET", f"/v1/users/{client_id}")`) already extracts links correctly — only the fallback path needs fixing.

## Tests to Update

`/home/igor/Amnezia-Web-Panel/tests/test_telemt_manager.py`

### `test_add_client_success`
Mock API response must include `links` structure:
```python
{
    "ok": True,
    "data": {
        "username": "alice",
        "secret": "abc123secret",
        "links": {
            "tls": ["tg://proxy?server=api.example.com&port=18443&secret=abc123secret"]
        }
    }
}
```
Verify the returned `config` and `vpn_link` equal `links["tls"][0]`, NOT a manually constructed link.

### `test_add_client_api_error`
No changes needed — already tests the error path.

### `test_get_client_config_not_found_uses_fallback`
Current mock returns `"links": {}` (empty) and asserts `"secret123" in result`. Update to:
1. Provide a `tg_link` in the mock client data
2. Assert the result equals that `tg_link`, not a manually constructed one

### `test_get_client_config_user_not_found`
No changes needed — asserts `result == "Not found"` which remains correct (user doesn't exist at all).

## Rule

**Never manually construct `tg://` links. Always use the API-provided link. If the API doesn't return one, return empty string (or `"Not found"` for "user missing" case).**

## Acceptance Criteria

1. `add_client()` returns the API-provided `tg_link` in both `config` and `vpn_link`
2. `get_client_config()` fallback returns the `tg_link` from `userData`, not a manually built link
3. `"Not found"` sentinel is preserved for "user does not exist" case
4. All existing tests pass (after updating mocks to include `links` structure)
5. No manual `tg://proxy?` string construction remains in `telemt_manager.py`
