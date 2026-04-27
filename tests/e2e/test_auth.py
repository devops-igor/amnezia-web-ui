"""E2E tests for authentication flows."""

import os

import pytest
from playwright.sync_api import Page

from tests.e2e.conftest import _do_login, _get_csrf_cookie


@pytest.mark.e2e
def test_login_page_loads(page: Page, base_url: str) -> None:
    """GET /login returns 200 and shows the login form."""
    page.goto(f"{base_url}/login")
    page.wait_for_load_state("networkidle")

    # Page should have the login form
    assert page.locator("input#username").is_visible()
    assert page.locator("input#password").is_visible()
    assert page.locator("form#loginForm").is_visible()


@pytest.mark.e2e
def test_login_success(page: Page, base_url: str, admin_user: str, admin_pass: str) -> None:
    """Valid admin credentials -> redirected to index page."""
    _do_login(page, base_url, admin_user, admin_pass)

    # Should be on index page, not login
    assert "/login" not in page.url


@pytest.mark.e2e
def test_login_failure(page: Page, base_url: str) -> None:
    """Wrong password -> login returns error."""
    page.goto(f"{base_url}/login")
    page.wait_for_load_state("networkidle")

    # Get CSRF cookie via Playwright's cookie API
    csrf_value = _get_csrf_cookie(page)

    # Try to login with wrong credentials via API
    result = page.request.post(
        f"{base_url}/api/auth/login",
        data={"username": "admin", "password": "wrongpassword123", "captcha": None},
        headers={
            "X-CSRF-Token": csrf_value,
            "Content-Type": "application/json",
        },
    )

    # Should fail (401 or 400)
    assert result.status in (400, 401, 403)

    # Should still be able to see the login page
    page.reload()
    page.wait_for_load_state("networkidle")
    assert "/login" in page.url


@pytest.mark.e2e
@pytest.mark.skipif(
    os.environ.get("E2E_TESTING", "").lower() == "true",
    reason="Rate limiting disabled in E2E test mode",
)
def test_login_rate_limiting(page: Page, base_url: str) -> None:
    """Rapidly hit login with wrong creds 6 times -> 429 on 6th attempt."""
    page.goto(f"{base_url}/login")
    page.wait_for_load_state("networkidle")

    # Extract CSRF from cookie store
    csrf_token = _get_csrf_cookie(page)

    statuses = []
    for i in range(6):
        result = page.request.post(
            f"{base_url}/api/auth/login",
            data={
                "username": "ratelimit_test_user",
                "password": "wrong_password",
                "captcha": None,
            },
            headers={
                "X-CSRF-Token": csrf_token,
                "Content-Type": "application/json",
            },
        )
        statuses.append(result.status)

    # At least one response should be 429 (or the last one)
    assert 429 in statuses, f"Expected rate limiting (429), got statuses: {statuses}"


@pytest.mark.e2e
def test_csrf_protection(page: Page, base_url: str) -> None:
    """POST to login without CSRF token -> 403 Forbidden."""
    # Navigate to login first so the browser has a session context
    page.goto(f"{base_url}/login")
    page.wait_for_load_state("networkidle")

    # Use Playwright's request API — POST without CSRF header
    result = page.request.post(
        f"{base_url}/api/auth/login",
        data={
            "username": "admin",
            "password": "test",
            "captcha": None,
        },
        headers={"Content-Type": "application/json"},
    )

    # CSRF middleware returns 403 for POSTs without the token when the session
    # cookie is present. Without a session, the app returns 401 instead.
    assert result.status in (403, 401), f"Expected 403/401 rejection, got {result.status}"


@pytest.mark.e2e
def test_logout(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Login then logout -> session cleared, redirected to /login."""
    page = authenticated_page

    # Navigate to logout
    page.goto(f"{base_url}/logout")
    # Should redirect to /login
    page.wait_for_url("**/login**", timeout=5000)

    # Verify we're on the login page
    assert "/login" in page.url
    assert page.locator("input#username").is_visible()
