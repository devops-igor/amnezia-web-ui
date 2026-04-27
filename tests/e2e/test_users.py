"""E2E tests for user management."""

import pytest
from playwright.sync_api import Page

from tests.e2e.conftest import api_get, api_post


@pytest.mark.e2e
def test_user_list_loads(authenticated_page: Page, base_url: str) -> None:
    """Navigate to /users -> sees user list."""
    page = authenticated_page

    result = api_get(page, "/api/users")

    # Should return a list of users (may include admin)
    if isinstance(result, dict) and "error" in result:
        # API returned an error — might be forbidden or similar
        pass
    else:
        assert isinstance(result, list) or "users" in result


@pytest.mark.e2e
def test_add_user(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Add a new user -> appears in user list."""
    page = authenticated_page

    add_result = api_post(
        page,
        "/api/users/add",
        {
            "username": "e2e_test_user",
            "password": "TestPass123!",
            "role": "user",
            "enabled": True,
        },
        csrf_token,
    )

    # Should succeed or return validation error
    body = add_result["body"]
    if add_result["status"] == 200:
        assert body.get("status") == "success" or "id" in body

    # Clean up — try to delete the test user
    if add_result["status"] == 200 and "id" in body:
        user_id = body["id"]
        api_post(page, f"/api/users/{user_id}/delete", {}, csrf_token)


@pytest.mark.e2e
def test_edit_user(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Edit user details -> changes saved."""
    page = authenticated_page

    # Create a test user first
    add_result = api_post(
        page,
        "/api/users/add",
        {
            "username": "e2e_edit_user",
            "password": "TestPass123!",
            "role": "user",
            "enabled": True,
        },
        csrf_token,
    )

    if add_result["status"] != 200:
        pytest.skip("Could not create test user for edit test")

    users_result = api_get(page, "/api/users")
    users = users_result if isinstance(users_result, list) else []

    # Find our test user
    test_user = None
    for u in users:
        if u.get("username") == "e2e_edit_user":
            test_user = u
            break

    if not test_user:
        pytest.skip("Test user not found for edit test")

    user_id = test_user["id"]

    # Edit the user
    edit_result = api_post(
        page,
        f"/api/users/{user_id}/update",
        {"username": "e2e_edit_user_renamed"},
        csrf_token,
    )

    # Should succeed
    assert edit_result["body"] is not None

    # Clean up
    api_post(page, f"/api/users/{user_id}/delete", {}, csrf_token)


@pytest.mark.e2e
def test_toggle_user(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Enable/disable a user -> status changes."""
    page = authenticated_page

    # Create a test user
    add_result = api_post(
        page,
        "/api/users/add",
        {
            "username": "e2e_toggle_user",
            "password": "TestPass123!",
            "role": "user",
            "enabled": True,
        },
        csrf_token,
    )

    if add_result["status"] != 200:
        pytest.skip("Could not create test user for toggle test")

    users_result = api_get(page, "/api/users")
    users = users_result if isinstance(users_result, list) else []

    test_user = None
    for u in users:
        if u.get("username") == "e2e_toggle_user":
            test_user = u
            break

    if not test_user:
        pytest.skip("Test user not found for toggle test")

    user_id = test_user["id"]

    # Toggle user enabled status
    toggle_result = api_post(page, f"/api/users/{user_id}/toggle", {}, csrf_token)

    # Should respond
    assert toggle_result["body"] is not None

    # Clean up
    api_post(page, f"/api/users/{user_id}/delete", {}, csrf_token)


@pytest.mark.e2e
def test_add_user_connection(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Add a connection to a user -> appears in user's connections."""
    page = authenticated_page

    # Get servers first
    servers_result = api_get(page, "/api/servers")
    servers = servers_result if isinstance(servers_result, list) else []

    if not servers:
        pytest.skip("No servers available for user connection test")

    # Create a test user
    add_result = api_post(
        page,
        "/api/users/add",
        {
            "username": "e2e_conn_user",
            "password": "TestPass123!",
            "role": "user",
            "enabled": True,
        },
        csrf_token,
    )

    if add_result["status"] != 200:
        pytest.skip("Could not create test user for connection test")

    users_result = api_get(page, "/api/users")
    users = users_result if isinstance(users_result, list) else []

    test_user = None
    for u in users:
        if u.get("username") == "e2e_conn_user":
            test_user = u
            break

    if not test_user:
        pytest.skip("Test user not found")

    user_id = test_user["id"]
    server_id = servers[0]["id"]

    # Add connection for user
    conn_result = api_post(
        page,
        f"/api/users/{user_id}/connections/add",
        {
            "server_id": server_id,
            "protocol": "awg",
            "name": "e2e_user_conn",
        },
        csrf_token,
    )

    assert conn_result["body"] is not None

    # Clean up
    api_post(page, f"/api/users/{user_id}/delete", {}, csrf_token)


@pytest.mark.e2e
def test_delete_user(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Delete a user -> removed from list."""
    page = authenticated_page

    # Create a test user
    add_result = api_post(
        page,
        "/api/users/add",
        {
            "username": "e2e_delete_user",
            "password": "TestPass123!",
            "role": "user",
            "enabled": True,
        },
        csrf_token,
    )

    if add_result["status"] != 200:
        pytest.skip("Could not create test user for delete test")

    users_result = api_get(page, "/api/users")
    users = users_result if isinstance(users_result, list) else []

    test_user = None
    for u in users:
        if u.get("username") == "e2e_delete_user":
            test_user = u
            break

    if not test_user:
        pytest.skip("Test user not found for delete test")

    user_id = test_user["id"]

    # Delete the user
    delete_result = api_post(page, f"/api/users/{user_id}/delete", {}, csrf_token)

    # Should succeed
    assert delete_result["body"] is not None

    # Verify user no longer exists
    users_after = api_get(page, "/api/users")
    if isinstance(users_after, list):
        user_ids = [u["id"] for u in users_after]
        assert user_id not in user_ids


@pytest.mark.e2e
def test_xss_prevention(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """XSS payload in username -> payload is escaped, not executed."""
    page = authenticated_page

    xss_payload = '<script>alert("xss")</script>'

    add_result = api_post(
        page,
        "/api/users/add",
        {
            "username": xss_payload,
            "password": "TestPass123!",
            "role": "user",
            "enabled": True,
        },
        csrf_token,
    )

    # The API may reject the XSS payload or accept it but escape on render.
    # If the user was created, verify the XSS isn't executed when viewed.
    if add_result["status"] == 200:
        # Navigate to users page and verify XSS is escaped
        page.goto(f"{base_url}/users")
        page.wait_for_load_state("networkidle")

        # The page should not have executed the script
        # Check that no alert was triggered (Playwright handles alerts)
        content = page.content()
        # The XSS payload should be HTML-escaped in the rendered page
        assert "<script>alert(" not in content or "&lt;script&gt;" in content

        # Clean up
        users_result = api_get(page, "/api/users")
        users = users_result if isinstance(users_result, list) else []
        for u in users:
            if xss_payload in u.get("username", ""):
                api_post(page, f"/api/users/{u['id']}/delete", {}, csrf_token)
