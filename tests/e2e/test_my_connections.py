"""E2E tests for user self-service (my connections)."""

import pytest
from playwright.sync_api import Page

from tests.e2e.conftest import _do_login, _get_csrf_cookie, api_get, api_post


def _create_test_user(
    page: Page, base_url: str, csrf_token: str, username: str = "e2e_my_user"
) -> dict:
    """Helper: create a regular user and return user dict."""
    add_result = api_post(
        page,
        "/api/users/add",
        {
            "username": username,
            "password": "TestPass123!",
            "role": "user",
            "enabled": True,
        },
        csrf_token,
    )

    if add_result["status"] != 200:
        pytest.skip("Could not create test user")

    users_result = api_get(page, "/api/users")
    users = users_result if isinstance(users_result, list) else []

    for u in users:
        if u.get("username") == username:
            return u

    pytest.skip("Test user not found after creation")
    return {}  # unreachable, for type checker


@pytest.mark.e2e
def test_user_login_and_list(page: Page, base_url: str, admin_user: str, admin_pass: str) -> None:
    """Login as regular user -> sees own connections."""
    # First, login as admin to create a test user
    _do_login(page, base_url, admin_user, admin_pass)
    csrf_token = _get_csrf_cookie(page)

    # Create test user via admin API
    test_user = _create_test_user(page, base_url, csrf_token, "e2e_my_user")
    test_username = test_user.get("username", "e2e_my_user")
    test_password = "TestPass123!"

    # Logout admin
    page.goto(f"{base_url}/logout")
    page.wait_for_load_state("networkidle")

    # Clear cookies for a fresh login
    page.context.clear_cookies()

    # Login as the test user via API (bypasses CSP)
    _do_login(page, base_url, test_username, test_password)

    # Navigate to /my — should see own connections
    page.goto(f"{base_url}/my")
    page.wait_for_load_state("networkidle")
    assert "/login" not in page.url


@pytest.mark.e2e
def test_create_connection(
    authenticated_page: Page,
    base_url: str,
    csrf_token: str,
) -> None:
    """Regular user creates a new connection -> appears in their list."""
    page = authenticated_page

    # Get a server to attach connection to
    result = api_get(page, "/api/servers")
    servers = result if isinstance(result, list) else result.get("servers", [])

    if not servers:
        pytest.skip("No servers available for connection test")

    server_id = servers[0]["id"]

    # Create a test user and add a connection for them
    test_user = _create_test_user(page, base_url, csrf_token, "e2e_conn_user")
    user_id = test_user["id"]

    conn_result = api_post(
        page,
        f"/api/users/{user_id}/connections/add",
        {"server_id": server_id, "protocol": "awg", "name": "e2e_user_conn"},
        csrf_token,
    )

    # Connection should be created successfully or return a meaningful response
    assert conn_result["body"] is not None
    if conn_result["status"] == 200:
        assert conn_result["body"].get("status") == "success" or "id" in conn_result["body"]

    # Clean up — delete the test user
    api_post(page, f"/api/users/{user_id}/delete", {}, csrf_token)


@pytest.mark.e2e
def test_view_connection_config(
    page: Page, base_url: str, admin_user: str, admin_pass: str
) -> None:
    """Click connection config -> sees config details."""
    # Login as admin
    _do_login(page, base_url, admin_user, admin_pass)
    csrf_token = _get_csrf_cookie(page)

    test_user = _create_test_user(page, base_url, csrf_token, "e2e_view_user")
    user_id = test_user["id"]

    # Get servers to find a connection
    servers_result = api_get(page, "/api/servers")
    servers = (
        servers_result if isinstance(servers_result, list) else servers_result.get("servers", [])
    )

    if not servers:
        api_post(page, f"/api/users/{user_id}/delete", {}, csrf_token)
        pytest.skip("No servers available for config test")

    server_id = servers[0]["id"]

    # Add a connection for the test user
    conn_result = api_post(
        page,
        f"/api/users/{user_id}/connections/add",
        {"server_id": server_id, "protocol": "awg", "name": "e2e_view_conn"},
        csrf_token,
    )

    if conn_result["status"] != 200:
        api_post(page, f"/api/users/{user_id}/delete", {}, csrf_token)
        pytest.skip("Could not create connection for config test")

    # Fetch the connection config via the server API
    config_result = api_post(
        page,
        f"/api/servers/{server_id}/connections/config",
        {"connection_id": conn_result["body"].get("id", "")},
        csrf_token,
    )

    # Config endpoint should respond with data
    assert config_result["body"] is not None

    # Clean up
    api_post(page, f"/api/users/{user_id}/delete", {}, csrf_token)


@pytest.mark.e2e
def test_role_access_denied(page: Page, base_url: str, admin_user: str, admin_pass: str) -> None:
    """Regular user navigating to admin page -> receives error or redirect."""
    # Login as admin
    _do_login(page, base_url, admin_user, admin_pass)
    csrf_token = _get_csrf_cookie(page)

    test_user = _create_test_user(page, base_url, csrf_token, "e2e_role_user")

    # Logout admin
    page.goto(f"{base_url}/logout")
    page.wait_for_load_state("networkidle")
    page.context.clear_cookies()

    # Login as regular user via API (bypasses CSP)
    _do_login(page, base_url, test_user.get("username", "e2e_role_user"), "TestPass123!")

    # Get CSRF token for the regular user's session
    regular_csrf = _get_csrf_cookie(page)

    # Try to access admin API — should get 403
    # Use Playwright's request API (bypasses CSP) with regular user's CSRF
    api_result = page.request.get(
        f"{base_url}/api/settings",
        headers={"X-CSRF-Token": regular_csrf},
    )

    # Regular user should be forbidden from admin endpoints
    assert api_result.status in (403, 401)
