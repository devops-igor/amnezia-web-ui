# Verification Plan: trusted-proxies-cidr-not-implemented

## L1 — Smoke Test (pm_bot runs)

1. `python3 -m py_compile app/utils/helpers.py` — compiles without error
2. `black --check .` — formatting clean
3. `flake8 .` — lint clean
4. `python3 -m pytest tests/test_rate_limiting.py -v --tb=short` — all rate limiting tests pass
5. `python3 -m pytest tests/ -v --ignore=tests/e2e` — full test suite passes
6. Verify `ipaddress` import is present in helpers.py
7. Verify `TRUSTED_PROXIES` is no longer a plain `set[str]` — grep for `set[str]` near TRUSTED_PROXIES should return nothing

## L2 — Automated Tests (py_bot writes)

- `test_cidr_match` — `172.18.0.5` in `172.18.0.0/24` → trusted
- `test_cidr_no_match` — `10.0.0.1` NOT in `192.168.0.0/24` → not trusted
- `test_mixed_ip_and_cidr` — plain IP + CIDR both work together
- `test_invalid_entry_skipped` — bad input like `"not-an-ip"` logs warning, doesn't crash
- `test_ipv6_cidr` — `fd00::1` in `fd00::/64` → trusted
- Existing tests continue to pass unchanged

## L3 — Live Deployment Verification (pm_bot runs)

1. Deploy updated image to dev server
2. Set `TRUSTED_PROXIES=172.18.0.0/24` in docker-compose.yaml
3. `docker compose up -d`, verify container starts
4. Check logs: `docker logs amnezia-panel 2>&1 | head -5` should show "Trusted proxies for X-Forwarded-For: {IPv4Network('172.18.0.0/24')}"
5. Browser login test at https://vpn.dev.drochi.games/

## L4 — QA Review (qa_bot, mandatory)

- Code review of CIDR parsing logic
- Security review: no bypass vectors, invalid input handled safely
- Verify docker-compose.yml example is correct