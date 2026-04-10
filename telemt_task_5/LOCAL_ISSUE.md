# Local Issue — Telemt `tg://` Link is Built Manually Instead of Using the API-Provided Link

## Problem

The `tg://proxy?server=...&port=...&secret=...` link is **constructed manually** from the panel's host/port/secret values instead of being taken from the telemt API response.

The telemt API already returns the correct, ready-to-use link in `resp["data"]["links"]`:

```
GET /v1/users → .data[].links.tls[]
```

Example (from API):
```
tg://proxy?server=64.112.127.200&port=18443&secret=ee9f812de32ecd4755aa538a22f4a55686686f737475702e7365
```

Example (what our backend builds):
```
tg://proxy?server=64.112.127.200&port=18443&secret=9f812de32ecd4755aa538a22f4a55686
```

### Symptoms of building the link ourselves

- **Truncated/padded secret** — our manually-constructed secret differs from what the API provides
- **Any future API changes to link format will be missed** — our hardcoded construction won't track upstream changes

### Root cause

Two locations in `telemt_manager.py` manually build `tg://` links:

**1. `add_client()` (line 343):**
```python
link = f"tg://proxy?server={host}&port={port}&secret={secret}"
return {"client_id": username, "config": link, "vpn_link": link}
```

The API response already contains the correct link in `resp["data"]["links"]`, but it is **ignored** and the link is built from scratch.

**2. `get_client_config()` (line 442):**
```python
return f"tg://proxy?server={host}&port={port}&secret={secret}"
```

This is the fallback when `GET /v1/users/{username}` fails — it falls back to `get_clients()` and builds the link manually from the `token` field. This must also be removed.

### Contrast with `get_clients()`

`get_clients()` already does the right thing — it reads the link from the API:
```python
if links.get("tls"):
    tg_link = links["tls"][0]
```

## Fix

**Rule: Never manually construct `tg://` links. Always use the API-provided link. If the API doesn't return one, return empty string.**

### 1. `add_client()` — use API link, no manual fallback

```python
resp = self._api_request("POST", "/v1/users", body)
if not resp or not resp.get("ok"):
    ...
    return {"client_id": username, "config": "", "vpn_link": ""}

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

### 2. `get_client_config()` — remove manual fallback construction

Current code at line 436-442:
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

The `get_clients()` already returns the API-provided `tg_link` in `userData`. Use it directly instead of building from secret:

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

## Files

- `telemt_manager.py` — `add_client()` and `get_client_config()` methods
- `tests/test_telemt_manager.py` — tests for both methods need updating