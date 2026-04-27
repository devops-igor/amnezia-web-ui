# E2E Playwright Tests — Running Guide

## Prerequisites

```bash
cd /home/igor/Amnezia-Web-Panel

# Install dependencies
pip install playwright==1.52.0 pytest-playwright==0.7.2 pytest-asyncio==1.3.0 --break-system-packages

# Install Chromium browser (one-time, ~100MB)
python3 -m playwright install chromium
```

---

## Running Tests

### Run all 36 E2E tests against dev server

```bash
E2E_BASE_URL=https://vpn.dev.drochi.games \
E2E_ADMIN_USER=admin \
E2E_ADMIN_PASS="$ADMIN_PASSWORD" \
python3 -m pytest tests/e2e/ -m e2e -v
```

> **Note:** Set `ADMIN_PASSWORD` in your shell environment before running. Never hardcode credentials in commands or scripts.

### Run a single test file

```bash
E2E_BASE_URL=https://vpn.dev.drochi.games \
python3 -m pytest tests/e2e/test_auth.py -m e2e -v
```

### Run a single test by name

```bash
E2E_BASE_URL=https://vpn.dev.drochi.games \
python3 -m pytest tests/e2e/test_auth.py::test_login_page_loads -m e2e -v
```

### Run with visible browser (not headless)

```bash
E2E_HEADLESS=0 \
python3 -m pytest tests/e2e/test_auth.py -m e2e -v
```

> Note: `E2E_HEADLESS=0` opens a real Chromium window. Only works on machines with a display (not headless servers).

### Run against localhost (for local development)

```bash
# Default: targets http://localhost:8000
python3 -m pytest tests/e2e/ -m e2e -v
```

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `E2E_BASE_URL` | `http://localhost:8000` | Target server URL |
| `E2E_HEADLESS` | `1` | Set to `0` for visible browser |
| `E2E_ADMIN_USER` | `admin` | Admin username for login |
| `E2E_ADMIN_PASS` | (empty) | Admin password — **set via env var, never hardcode** |

---

## Test Files & Scenarios

| File | Tests | What it covers |
|------|-------|---------------|
| `test_auth.py` | 6 | Login page, success, failure, rate limiting, CSRF, logout |
| `test_servers.py` | 7 | Server list, detail, check, install, stats, add form, reboot |
| `test_connections.py` | 5 | Connection list, add, config/QR, toggle, delete |
| `test_users.py` | 7 | User list, add, edit, toggle, add connection, delete, XSS |
| `test_my_connections.py` | 4 | User login+list, create, view config, role access denied |
| `test_settings.py` | 4 | Page load, change title, captcha toggle, backup download |
| `test_share.py` | 3 | Enable sharing, access share link, download config |

**Total: 36 test scenarios**

---

## Seeing What's Happening (Progress & Debugging)

### 1. Verbose output (`-v` flag)

The `-v` flag shows each test name and PASS/FAIL status in real-time:

```
tests/e2e/test_auth.py::test_login_page_loads PASSED
tests/e2e/test_auth.py::test_login_success FAILED
...
```

### 2. Extra verbose (`-vv` flag)

Shows full assertion details:

```bash
python3 -m pytest tests/e2e/test_auth.py -m e2e -vv
```

### 3. Printstatements (`-s` flag)

Disables output capture so `print()` statements in tests show in real-time:

```bash
python3 -m pytest tests/e2e/test_auth.py -m e2e -v -s
```

### 4. Detailed traceback (`--tb=long`)

Shows full traceback on failures:

```bash
python3 -m pytest tests/e2e/test_auth.py -m e2e -v --tb=long
```

### 5. Short traceback (`--tb=short`)

More compact, shows just the assertion line:

```bash
python3 -m pytest tests/e2e/test_auth.py -m e2e -v --tb=short
```

### 6. Screenshots on failure

Automatically saved to `tests/e2e/screenshots/` when a test fails. This directory is gitignored — screenshots are runtime artifacts, not committed to the repo.

```bash
ls tests/e2e/screenshots/
# e.g., test_login_success[chromium].png
```

### 7. Slow motion for debugging

Add a delay between Playwright actions to watch what happens:

```python
# In your test (temporary debug):
page.wait_for_timeout(2000)  # 2 second pause
```

### 8. Run with visible browser + slow motion

Best for debugging — you can watch the browser in real time:

```bash
E2E_HEADLESS=0 python3 -m pytest tests/e2e/test_auth.py::test_login_success -m e2e -v -s
```

### 9. Playwright trace viewer

Record a trace and inspect it afterward:

```bash
E2E_BASE_URL=https://vpn.dev.drochi.games \
python3 -m pytest tests/e2e/test_auth.py -m e2e --tracing on -v
# Then view:
python3 -m playwright show-trace trace.zip
```

---

## Common Issues

### "ModuleNotFoundError: No module named 'playwright'"

```bash
pip install playwright==1.52.0 pytest-playwright==0.7.2 --break-system-packages
python3 -m playwright install chromium
```

### "BrowserType.launch: Executable doesn't exist"

```bash
python3 -m playwright install chromium
```

### Tests timeout / hang

- The dev server may be slow to respond
- Increase wait timeouts in test files or conftest.py (e.g., `timeout=10000` → `timeout=60000`)
- Or add `--timeout=120` to pytest: `python3 -m pytest tests/e2e/ -m e2e -v --timeout=120`

### Login tests fail with 400/422

- Check `E2E_ADMIN_PASS` is set correctly for the target server
- Check the server is up: `curl -sk https://vpn.dev.drochi.games/login`
- Check if captcha is enabled (test fixtures try to handle it but may fail)

### Fixture errors / test hangs

Tests use pytest-playwright's built-in sync fixtures. If you see hangs or async errors, make sure conftest.py does NOT define custom async `browser` or `page` fixtures — those conflict with pytest-asyncio.

---

## Quick Reference

```bash
# Full suite, dev server, verbose
E2E_BASE_URL=https://vpn.dev.drochi.games E2E_ADMIN_PASS="$ADMIN_PASSWORD" python3 -m pytest tests/e2e/ -m e2e -v

# Single test, detailed output  
E2E_BASE_URL=https://vpn.dev.drochi.games E2E_ADMIN_PASS="$ADMIN_PASSWORD" python3 -m pytest tests/e2e/test_auth.py::test_login_page_loads -m e2e -vv -s

# Just collect, don't run
python3 -m pytest tests/e2e/ -m e2e --collect-only -q
```