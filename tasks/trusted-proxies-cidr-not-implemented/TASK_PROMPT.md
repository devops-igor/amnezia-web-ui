# Task: Implement CIDR Support in TRUSTED_PROXIES

## Your Identity
You are py_bot. Before starting:
- Read your SOUL.md at /home/igor/.hermes/profiles/py_bot/SOUL.md
- Read /home/igor/hermes-team/shared/PYTHON_STANDARDS.md
- Read /home/igor/hermes-team/shared/WORKFLOW.md

## Task Specification

The `TRUSTED_PROXIES` env var in `app/utils/helpers.py` claims to support CIDR notation but only does exact string matching. This breaks rate limiting behind Docker reverse proxies because proxy container IPs are dynamic.

### Files to Modify

1. **`app/utils/helpers.py`** — lines 33-58:
   - Replace `TRUSTED_PROXIES: set[str]` with two parsed collections:
     - `_trusted_proxy_hosts: set[ipaddress.IPv4Address | ipaddress.IPv6Address]` — exact IP matches
     - `_trusted_proxy_networks: list[ipaddress.IPv4Network | ipaddress.IPv6Network]` — CIDR matches
   - Add parsing function `_parse_trusted_proxies(env_value: str)` that:
     - Splits on `,`, strips whitespace
     - For each entry, tries `ipaddress.ip_network(entry, strict=False)` first
     - If it has a host bits set (e.g., `172.16.0.1/32`), also try `ipaddress.ip_address(entry)`
     - Stores CIDRs (with netmask) in `_trusted_proxy_networks`
     - Stores plain IPs in `_trusted_proxy_hosts`
     - Logs a warning for invalid entries and skips them (never crashes)
   - Update `_get_client_ip()` to check both collections:
     ```python
     def _get_client_ip(request: Request) -> str:
         peer = get_remote_address(request)
         peer_ip = ipaddress.ip_address(peer)
         if (peer_ip in _trusted_proxy_hosts or
             any(peer_ip in net for net in _trusted_proxy_networks)):
             forwarded = request.headers.get("X-Forwarded-For")
             if forwarded:
                 return forwarded.split(",")[0].strip()
         return peer
     ```
   - Keep the module-level logging (info for configured proxies, info for none configured)
   - Add `import ipaddress` at the top

2. **`tests/test_rate_limiting.py`** — add a new test class `TestTrustedProxiesCidr`:
   - `test_cidr_match` — peer `172.18.0.5` matches network `172.18.0.0/24`
   - `test_cidr_no_match` — peer `10.0.0.1` does NOT match network `192.168.0.0/24`
   - `test_mixed_ip_and_cidr` — TRUSTED_PROXIES with both `10.0.0.1` and `172.18.0.0/24` works
   - `test_invalid_entry_skipped_with_warning` — invalid entry like `not-an-ip` logs warning, doesn't crash
   - `test_ipv6_cidr` — `fd00::/64` matches `fd00::1`
   - Use `patch.object(helpers, "_trusted_proxy_hosts", ...)` and `patch.object(helpers, "_trusted_proxy_networks", ...)` for patching, same pattern as existing tests

3. **`docker-compose.yml`** — update the TRUSTED_PROXIES example:
   - Change the commented example from `172.16.0.1,172.16.0.2` to `TRUSTED_PROXIES=172.18.0.0/24` with a comment explaining CIDR support
   - Add a note: `# Supports CIDR notation (e.g. 172.18.0.0/24) and plain IPs. Use CIDR for Docker networks to survive container IP changes.`

### What NOT to Change

- Do NOT change the rate limiter configuration in `app.py`
- Do NOT change `slowapi` integration or `get_remote_address` usage
- Do NOT change the `_rate_limit_exceeded_handler` function
- Do NOT change any other endpoint or route logic
- Do NOT remove backward compatibility — plain IPs must still work
- Do NOT change the `_sanitize_error`, `hash_password`, `verify_password`, or any other helper functions

### Compilation Gate

Before creating DEV_HANDOVER.md, ALL must pass:
```bash
cd /home/igor/Amnezia-Web-Panel
black --check .
flake8 .
python3 -m py_compile app/utils/helpers.py
python3 -m pytest tests/test_rate_limiting.py -v --tb=short
python3 -m pytest tests/ -v --ignore=tests/e2e --tb=short
```

## Project Root
/home/igor/Amnezia-Web-Panel

## Artifacts
- Write DEV_HANDOVER.md in tasks/trusted-proxies-cidr-not-implemented/
- Append to WORKLOG.md at project root with IMPLEMENTATION_COMPLETE