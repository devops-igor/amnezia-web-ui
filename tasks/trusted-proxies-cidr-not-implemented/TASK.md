# Bug: TRUSTED_PROXIES CIDR Support Not Implemented

**Issue:** #152
**Priority:** P1 — Bug (directly impacts rate limiting behind reverse proxy)
**Category:** Security
**Status:** Open

## Problem

The `TRUSTED_PROXIES` code in `app/utils/helpers.py` claims to support CIDR notation in its comment (line 34):

```python
# Comma-separated CIDRs or IPs from the TRUSTED_PROXIES env var.
```

But the implementation (line 54) uses exact string matching against a `set[str]`:

```python
if peer in TRUSTED_PROXIES:
```

This means `TRUSTED_PROXIES=172.18.0.0/24` stores the literal string `"172.18.0.0/24"` in the set, and `"172.18.0.5" in {"172.18.0.0/24"}` returns `False`. CIDRs are documented but never matched.

### Impact

1. **Rate limiting is broken behind reverse proxies** — without a correct TRUSTED_PROXIES value, all client IPs resolve to the proxy IP (e.g., `172.18.0.5`), creating a single rate-limit bucket for all users
2. **Docker IPs are dynamic** — hardcoding a specific IP like `172.18.0.5` breaks on `docker compose down && up` when the proxy container gets a different IP
3. **Users must hardcode fragile IPs** — instead of resilient CIDR ranges like `172.18.0.0/24`

## Current Code

`app/utils/helpers.py` lines 33-58:
```python
# Only trust X-Forwarded-For when the actual TCP peer is in this set.
# Comma-separated CIDRs or IPs from the TRUSTED_PROXIES env var.
# Empty = trust no proxy (X-Forwarded-For always ignored).
TRUSTED_PROXIES: set[str] = {
    ip.strip() for ip in os.environ.get("TRUSTED_PROXIES", "").split(",") if ip.strip()
}

if TRUSTED_PROXIES:
    logger.info("Trusted proxies for X-Forwarded-For: %s", TRUSTED_PROXIES)
else:
    logger.info("No trusted proxies configured. X-Forwarded-For headers will be ignored.")

def _get_client_ip(request: Request) -> str:
    peer = get_remote_address(request)
    if peer in TRUSTED_PROXIES:  # <— EXACT STRING MATCH, not CIDR
        forwarded = request.headers.get("X-Forwarded-For")
        if forwarded:
            return forwarded.split(",")[0].strip()
    return peer
```

## Required Fix

1. **Parse CIDRs into `ipaddress` objects** at module load time:
   - Plain IPs like `172.16.0.1` → `ipaddress.ip_address("172.16.0.1")`
   - CIDRs like `172.18.0.0/24` → `ipaddress.ip_network("172.18.0.0/24")`
   - Store as two lists: `_trusted_proxy_hosts: list[IPv4Address|IPv6Address]` and `_trusted_proxy_networks: list[IPv4Network|IPv6Network]`

2. **Match using `in` operator for networks** — `ip_address in ip_network` is natively supported by Python's `ipaddress` module

3. **Update `_get_client_ip()`** to use the new matching logic

4. **Add tests for CIDR matching** — at minimum:
   - Peer IP within a CIDR range is trusted
   - Peer IP outside a CIDR range is not trusted
   - Mixed IPs and CIDRs work together
   - Invalid CIDR strings are logged as warnings and skipped (not crash)

5. **Update `docker-compose.yml` example** — uncomment and set `TRUSTED_PROXIES` with a CIDR example

6. **Backward compatible** — plain IPs still work exactly as before

## Acceptance Criteria

- [ ] `TRUSTED_PROXIES=172.18.0.0/24` correctly matches `172.18.0.5` as a trusted proxy
- [ ] `TRUSTED_PROXIES=172.16.0.1` still works as exact match (backward compat)
- [ ] `TRUSTED_PROXIES=172.18.0.0/24,10.0.0.1` works with mixed CIDRs and IPs
- [ ] Invalid CIDR strings produce a warning log and are skipped (not crash)
- [ ] Existing rate limiting tests pass unchanged
- [ ] New tests cover CIDR matching, mixed inputs, and invalid input
- [ ] `docker-compose.yml` example updated with CIDR documentation
- [ ] `black --check .` and `flake8 .` pass