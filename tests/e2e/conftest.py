"""Playwright E2E test fixtures for Amnezia Web Panel.

Provides:
- base_url: configurable target server URL
- browser: Playwright chromium browser instance
- page: fresh browser context + page, navigated to base_url
- authenticated_page: logged-in admin page with session cookie
- csrf_token: CSRF token extracted from browser cookies
- api_post: helper for making CSRF-aware POST requests from browser
"""

import os
import logging
import types
from typing import Any, Dict, Optional

import pytest
from playwright.async_api import async_playwright, Browser, Page

logger = logging.getLogger(__name__)

# ── Marker registration ──────────────────────────────────────────────


def pytest_configure(config: pytest.Config) -> None:
    """Register the e2e marker to avoid unknown-marker warnings."""
    config.addinivalue_line("markers", "e2e: end-to-end Playwright tests")


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
        # Async fixtures appear as async_generator in funcargs; skip screenshot
        # for unresolved async fixtures. Only screenshot if we have a real Page.
        if page_obj and not isinstance(page_obj, types.AsyncGeneratorType):
            if not page_obj.is_closed():
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


@pytest.fixture(scope="session")
def headless() -> bool:
    """Run browser headless unless E2E_HEADLESS=0."""
    return os.environ.get("E2E_HEADLESS", "1") != "0"


@pytest.fixture(scope="session")
async def browser(headless: bool):
    """Provide a Playwright chromium browser instance (session-scoped)."""
    async with async_playwright() as pw:
        browser = await pw.chromium.launch(headless=headless)
        yield browser
        await browser.close()


@pytest.fixture
async def page(browser: Browser, base_url: str) -> Page:
    """Fresh browser context + page, navigated to base_url."""
    context = await browser.new_context()
    p = await context.new_page()
    p.set_default_timeout(10000)
    await p.goto(base_url)
    yield p
    await context.close()


async def _do_login(page: Page, base_url: str, admin_user: str, admin_pass: str) -> None:
    """Perform the login flow on the given page.

    Handles captcha by hitting /api/auth/captcha first (which stores
    the answer in the session). We then extract the captcha text from
    the session cookie via a small JS trick — because the captcha
    answer is server-side (session), we instead bypass captcha for E2E
    by first checking if the app has captcha enabled, and if so,
    disabling it or using the direct captcha fetch approach.

    Strategy: Navigate to the login page so we get a session, then
    call /api/auth/captcha (which stores captcha_answer in session).
    We cannot read session data from browser JS, so we use the
    following approach:
      1. Navigate to /login to get a session cookie.
      2. Evaluate fetch('/api/auth/captcha') — this generates a new
         captcha image AND stores the answer in the session.
      3. We then submit the form BUT we include the captcha text.
         Since we can't read it from the session, we rely on the
         E2E test instance having captcha disabled (via settings).

    If captcha is enabled, the test framework would need OCR or a
    test-mode bypass. For now, we assume the E2E environment has
    captcha disabled (which is the default for a fresh install).
    """
    await page.goto(f"{base_url}/login")
    await page.wait_for_load_state("networkidle")

    # Fill in credentials
    await page.fill("input#username", admin_user)
    await page.fill("input#password", admin_pass)

    # If captcha field is present, fill it (only works if captcha
    # is disabled on the server, or if we can determine the answer).
    captcha_input = page.locator("input#captcha")
    if await captcha_input.count() > 0 and await captcha_input.is_visible():
        # Captcha is enabled — attempt to extract text via API.
        # The /api/auth/captcha endpoint generates a new captcha and
        # stores the answer in session. We can't read session from JS,
        # but in a test environment the captcha might be simple or
        # we can reload it. For now, fill a placeholder; the test
        # config should have captcha disabled for E2E.
        await captcha_input.fill("e2e_bypass")

    # Get CSRF token from meta tag or cookie
    csrf_token = await page.evaluate(
        """() => {
        const meta = document.querySelector('meta[name="csrf-token"]');
        if (meta) return meta.getAttribute('content');
        const match = document.cookie.match(/csrftoken=([^;]+)/);
        return match ? match[1] : '';
    }"""
    )

    # Submit via the JS function on the page
    await page.evaluate(
        """async ([adminUser, adminPass, csrfToken]) => {
        const res = await fetch('/api/auth/login', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': csrfToken,
            },
            body: JSON.stringify({
                username: adminUser,
                password: adminPass,
                captcha: null,
            }),
        });
        const data = await res.json();
        if (!res.ok) {
            throw new Error(data.error || 'Login failed: ' + res.status);
        }
        return data;
    }""",
        [admin_user, admin_pass, csrf_token],
    )

    # Wait for redirect to complete
    await page.wait_for_url(f"{base_url}/", timeout=5000)


@pytest.fixture
async def authenticated_page(page: Page, base_url: str, admin_user: str, admin_pass: str) -> Page:
    """Return a page that is logged in as admin."""
    await _do_login(page, base_url, admin_user, admin_pass)
    return page


@pytest.fixture
async def csrf_token(authenticated_page: Page, base_url: str) -> str:
    """Extract CSRF token from the authenticated browser session."""
    token = await authenticated_page.evaluate(
        """() => {
        const match = document.cookie.match(/csrftoken=([^;]+)/);
        return match ? match[1] : '';
    }"""
    )
    return token


async def api_post(page: Page, url: str, data: Dict[str, Any], token: str) -> Dict[str, Any]:
    """Make a POST request from the browser context with CSRF header + session.

    Args:
        page: Authenticated Playwright page.
        url: Relative URL path (e.g., '/api/users/add').
        data: JSON-serializable body dict.
        token: CSRF token string.

    Returns:
        Parsed JSON response body.
    """
    result = await page.evaluate(
        """async ([url, data, csrfToken]) => {
        const res = await fetch(url, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': csrfToken,
            },
            body: JSON.stringify(data),
        });
        return { status: res.status, body: await res.json() };
    }""",
        [url, data, token],
    )
    return result
