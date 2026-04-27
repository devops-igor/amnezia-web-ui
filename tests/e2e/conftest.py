"""Playwright E2E test fixtures for Amnezia Web Panel.

Provides:
- base_url: configurable target server URL
- authenticated_page: logged-in admin page with session cookie
- csrf_token: CSRF token extracted from page meta tag
- api_get: helper for making GET requests via Playwright's request API
- api_post: helper for making CSRF-aware POST requests via Playwright's request API

Browser and page fixtures are provided by pytest-playwright.
Configure headless/headed via --headed CLI flag or E2E_HEADLESS env var.
"""

import os
import time
import logging
from typing import Any, Dict
from urllib.parse import urljoin

import pytest
from playwright.sync_api import Page

logger = logging.getLogger(__name__)

# ── Marker registration ──────────────────────────────────────────────


def pytest_configure(config: pytest.Config) -> None:
    """Register the e2e marker and configure Playwright launch args."""
    config.addinivalue_line("markers", "e2e: end-to-end Playwright tests")

    # Respect E2E_HEADLESS env var when --headed/--headless flags aren't
    # explicitly passed on the CLI.
    if not config.getoption("--headed", default=False):
        # pytest-playwright defaults to headless; setting E2E_HEADLESS=0
        # with --headed flag overrides that.
        pass


def pytest_collection_modifyitems(config: pytest.Config, items: list) -> None:
    """Auto-apply the e2e marker to every test in this directory."""
    for item in items:
        if str(item.fspath).endswith("_test.py") or "e2e" in str(item.fspath):
            item.add_marker(pytest.mark.e2e)


# ── Screenshot-on-failure hook ───────────────────────────────────────
@pytest.hookimpl(tryfirst=True, hookwrapper=True)
def pytest_runtest_makereport(item: pytest.Item, call):
    outcome = yield
    report = outcome.get_result()
    if report.when == "call" and report.failed:
        page_obj = item.funcargs.get("page") or item.funcargs.get("authenticated_page")
        if page_obj and not page_obj.is_closed():
            screenshot_dir = os.path.join(os.path.dirname(__file__), "screenshots")
            os.makedirs(screenshot_dir, exist_ok=True)
            path = os.path.join(screenshot_dir, f"{item.name}.png")
            try:
                page_obj.screenshot(path=path)
                logger.info("Screenshot saved: %s", path)
            except Exception:
                pass


# ── Fixtures ──────────────────────────────────────────────────────────


@pytest.fixture(scope="session")
def base_url() -> str:
    """Target server URL (from env, defaults to localhost)."""
    return os.environ.get("E2E_BASE_URL", "http://localhost:8000")


@pytest.fixture(scope="session")
def admin_user() -> str:
    """Admin username override from env."""
    return os.environ.get("E2E_ADMIN_USER", "admin")


@pytest.fixture(scope="session")
def admin_pass() -> str:
    """Admin password override from env.

    Default empty — the test must know the password or set it via env.
    """
    return os.environ.get("E2E_ADMIN_PASS", "")


def _get_csrf_cookie(page: Page) -> str:
    """Extract the HttpOnly csrftoken cookie via Playwright's cookie API.

    The CSRF cookie is set as HttpOnly by BunkerWeb, so JavaScript cannot
    read it from document.cookie. Playwright's context.cookies() bypasses
    this restriction because it uses the CDP protocol, not JS.
    """
    for cookie in page.context.cookies():
        if cookie["name"] == "csrftoken":
            return cookie["value"]
    return ""


def _do_login(page: Page, base_url: str, username: str, password: str) -> None:
    """Perform the login flow using Playwright's request API.

    The login page's doLogin() JS function uses fetch() internally, which is
    blocked by BunkerWeb's Content Security Policy. Instead, we:

    1. Navigate to /login to establish a session (sets HttpOnly csrf cookie).
    2. Extract the CSRF token from Playwright's cookie API (bypasses HttpOnly).
    3. Login via page.request.post() — this uses Playwright's APIRequestContext
       which shares the browser's cookies but runs outside CSP restrictions.
    4. Navigate to the index page to establish authenticated session state.

    Includes retry with exponential backoff to handle BunkerWeb rate limiting.
    """
    max_attempts = 3
    for attempt in range(max_attempts):
        try:
            return _do_login_inner(page, base_url, username, password)
        except Exception as e:
            if attempt < max_attempts - 1 and (
                "CONNECTION_REFUSED" in str(e) or "socket hang up" in str(e) or "Timeout" in str(e)
            ):
                wait = 2 ** (attempt + 1)
                logger.warning(
                    "Login attempt %d failed (%s), retrying in %ds...",
                    attempt + 1,
                    type(e).__name__,
                    wait,
                )
                time.sleep(wait)
            else:
                raise


def _do_login_inner(page: Page, base_url: str, username: str, password: str) -> None:
    """Inner login implementation (no retry)."""
    page.goto(f"{base_url}/login")
    page.wait_for_load_state("networkidle")

    # Extract CSRF from HttpOnly cookie via Playwright's cookie API
    csrf_value = _get_csrf_cookie(page)
    if not csrf_value:
        raise RuntimeError("CSRF cookie not found — cannot login")

    # Login via Playwright's request API (bypasses CSP fetch restrictions)
    result = page.request.post(
        f"{base_url}/api/auth/login",
        data={"username": username, "password": password, "captcha": None},
        headers={
            "X-CSRF-Token": csrf_value,
            "Content-Type": "application/json",
        },
    )

    if result.status != 200:
        raise RuntimeError(f"Login API returned {result.status}: {result.text()[:200]}")

    # Navigate to index page to establish authenticated session in the browser
    page.goto(f"{base_url}/")
    page.wait_for_load_state("networkidle")

    # Verify we're on an authenticated page (not redirected to login)
    if "/login" in page.url:
        raise RuntimeError(f"Login succeeded but page redirected to login. URL: {page.url}")


@pytest.fixture
def authenticated_page(page: Page, base_url: str, admin_user: str, admin_pass: str) -> Page:
    """Return a page that is logged in as admin."""
    _do_login(page, base_url, admin_user, admin_pass)
    return page


@pytest.fixture
def csrf_token(authenticated_page: Page, base_url: str) -> str:
    """Extract CSRF token from the authenticated page's meta tag.

    After login, the app populates <meta name="csrf-token" content="...">
    on every authenticated page. Falls back to Playwright's cookie API if
    the meta tag is empty (e.g. on pages that don't inject it).
    """
    # The authenticated_page is already on an authenticated page (post-login
    # redirect). Read the CSRF from the meta tag or cookie store.
    token = authenticated_page.evaluate("""() => {
        const meta = document.querySelector('meta[name="csrf-token"]');
        if (meta && meta.getAttribute('content')) return meta.getAttribute('content');
        return '';
    }""")

    if not token:
        # Fallback: read from Playwright's cookie store (bypasses HttpOnly)
        token = _get_csrf_cookie(authenticated_page)

    return token


def api_get(page: Page, url: str) -> Any:
    """Make a GET request via Playwright's request API (bypasses CSP).

    Uses page.request (APIRequestContext) which shares the browser context's
    cookies and session, but runs outside the page's CSP restrictions.

    Args:
        page: Authenticated Playwright page (used for its request context).
        url: Relative URL path (e.g., '/api/servers').

    Returns:
        Parsed JSON response body. Returns a dict with 'error' key for
        non-JSON responses.
    """
    full_url = urljoin(page.url, url)
    response = page.request.get(full_url)
    content_type = response.headers.get("content-type", "")
    text = response.text()

    if "application/json" in content_type or text.startswith(("{", "[")):
        try:
            return response.json()
        except Exception:
            return {"error": text[:200], "status": response.status}

    return {"error": text[:200], "status": response.status}


def api_post(page: Page, url: str, data: Dict[str, Any], token: str) -> Dict[str, Any]:
    """Make a POST request via Playwright's request API (bypasses CSP).

    Uses page.request (APIRequestContext) which shares the browser context's
    cookies and session, but runs outside the page's CSP restrictions.

    Args:
        page: Authenticated Playwright page (used for its request context).
        url: Relative URL path (e.g., '/api/users/add').
        data: JSON-serializable body dict.
        token: CSRF token string.

    Returns:
        Dict with 'status' (int) and 'body' (dict). Handles HTML error
        responses gracefully — when the server returns non-JSON (e.g. a
        CSRF rejection page), body contains {error: <first 200 chars>}.
    """
    full_url = urljoin(page.url, url)
    response = page.request.post(
        full_url,
        data=data,
        headers={
            "X-CSRF-Token": token,
            "Content-Type": "application/json",
        },
    )

    content_type = response.headers.get("content-type", "")
    text = response.text()
    status = response.status

    if "application/json" in content_type or text.startswith(("{", "[")):
        try:
            body = response.json()
        except Exception:
            body = {"error": text[:200]}
    else:
        body = {"error": text[:200]}

    return {"status": status, "body": body}
