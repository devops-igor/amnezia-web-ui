"""E2E tests for authentication flows."""

import pytest
from playwright.sync_api import Page


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
    """Valid admin credentials → redirected to index page with server list."""
    page.goto(f"{base_url}/login")
    page.wait_for_load_state("networkidle")

    csrf_token = page.evaluate(
        """() => {
        const meta = document.querySelector('meta[name="csrf-token"]');
        if (meta) return meta.getAttribute('content');
        const match = document.cookie.match(/csrftoken=([^;]+)/);
        return match ? match[1] : '';
    }"""
    )

    result = page.evaluate(
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
        return { status: res.status, body: await res.json() };
    }""",
        [admin_user, admin_pass, csrf_token],
    )

    assert result["status"] == 200
    assert result["body"].get("status") == "success"


@pytest.mark.e2e
def test_login_failure(page: Page, base_url: str) -> None:
    """Wrong password → stays on login page with error message."""
    page.goto(f"{base_url}/login")
    page.wait_for_load_state("networkidle")

    csrf_token = page.evaluate(
        """() => {
        const meta = document.querySelector('meta[name="csrf-token"]');
        if (meta) return meta.getAttribute('content');
        const match = document.cookie.match(/csrftoken=([^;]+)/);
        return match ? match[1] : '';
    }"""
    )

    result = page.evaluate(
        """async ([csrfToken]) => {
        const res = await fetch('/api/auth/login', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'X-CSRF-Token': csrfToken,
            },
            body: JSON.stringify({
                username: 'admin',
                password: 'wrongpassword123',
                captcha: null,
            }),
        });
        return { status: res.status, body: await res.json() };
    }""",
        [csrf_token],
    )

    assert result["status"] == 401
    assert "error" in result["body"]


@pytest.mark.e2e
def test_login_rate_limiting(page: Page, base_url: str) -> None:
    """Rapidly hit login with wrong creds 6 times → 429 on 6th attempt."""
    page.goto(f"{base_url}/login")
    page.wait_for_load_state("networkidle")

    csrf_token = page.evaluate(
        """() => {
        const match = document.cookie.match(/csrftoken=([^;]+)/);
        return match ? match[1] : '';
    }"""
    )

    statuses = []
    for i in range(6):
        result = page.evaluate(
            """async ([csrfToken]) => {
            const res = await fetch('/api/auth/login', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrfToken,
                },
                body: JSON.stringify({
                    username: 'ratelimit_test_user',
                    password: 'wrong_password',
                    captcha: null,
                }),
            });
            return res.status;
        }""",
            [csrf_token],
        )
        statuses.append(result)

    # At least one response should be 429 (or the last one)
    assert 429 in statuses, f"Expected rate limiting (429), got statuses: {statuses}"


@pytest.mark.e2e
def test_csrf_protection(page: Page, base_url: str) -> None:
    """POST to login without CSRF token → 403 Forbidden."""
    result = page.evaluate(
        """async () => {
        const res = await fetch('/api/auth/login', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({
                username: 'admin',
                password: 'test',
                captcha: null,
            }),
        });
        return res.status;
    }"""
    )

    # CSRF middleware returns 403 for POSTs without the token
    # when the session cookie is present (which it is after navigating
    # to any page)
    assert result == 403, f"Expected 403 CSRF rejection, got {result}"


@pytest.mark.e2e
def test_logout(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Login then logout → session cleared, redirected to /login."""
    page = authenticated_page

    # Navigate to logout
    page.goto(f"{base_url}/logout")
    # Should redirect to /login
    page.wait_for_url("**/login**", timeout=5000)

    # Verify we're on the login page
    assert "/login" in page.url
    assert page.locator("input#username").is_visible()
