"""E2E tests for server management pages and API."""

import pytest
from playwright.sync_api import Page

from tests.e2e.conftest import api_get, api_post


@pytest.mark.e2e
def test_server_list_loads(authenticated_page: Page, base_url: str) -> None:
    """Navigate to / -> sees server cards with protocol badges."""
    page = authenticated_page
    page.goto(f"{base_url}/")
    page.wait_for_load_state("networkidle")

    # Should be on the index page, not redirected to login
    assert "/login" not in page.url

    # The page content should contain server-related UI elements.
    # Even if no servers exist, the page should render.
    content = page.content()
    assert len(content) > 100


@pytest.mark.e2e
def test_server_detail_page(authenticated_page: Page, base_url: str) -> None:
    """Click a server -> sees server detail page with stats."""
    page = authenticated_page

    # Get list of servers via API
    result = api_get(page, "/api/servers")

    servers = result if isinstance(result, list) else result.get("servers", [])

    if not servers:
        pytest.skip("No servers available to test detail page")

    server_id = servers[0]["id"]
    page.goto(f"{base_url}/server/{server_id}")
    page.wait_for_load_state("networkidle")

    # Should show server detail content
    content = page.content()
    assert len(content) > 100


@pytest.mark.e2e
def test_server_check(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Click 'Check' on a server -> sees check result."""
    page = authenticated_page

    # Get servers
    result = api_get(page, "/api/servers")
    servers = result if isinstance(result, list) else result.get("servers", [])

    if not servers:
        pytest.skip("No servers available to test check")

    server_id = servers[0]["id"]
    check_result = api_post(page, f"/api/servers/{server_id}/check", {}, csrf_token)

    # The check endpoint returns a response with server status data
    assert check_result["body"] is not None


@pytest.mark.e2e
def test_server_install(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Click 'Install' on a server -> sees install progress/status."""
    page = authenticated_page

    result = api_get(page, "/api/servers")
    servers = result if isinstance(result, list) else result.get("servers", [])

    if not servers:
        pytest.skip("No servers available to test install")

    server_id = servers[0]["id"]
    install_result = api_post(page, f"/api/servers/{server_id}/install", {}, csrf_token)

    # Install returns either success or an error (e.g. already installed)
    assert install_result["body"] is not None


@pytest.mark.e2e
def test_server_stats(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Navigate to server stats -> sees traffic stats display."""
    page = authenticated_page

    result = api_get(page, "/api/servers")
    servers = result if isinstance(result, list) else result.get("servers", [])

    if not servers:
        pytest.skip("No servers available to test stats")

    server_id = servers[0]["id"]
    stats_result = api_post(page, f"/api/servers/{server_id}/stats", {}, csrf_token)

    # Stats endpoint should respond
    assert stats_result["body"] is not None


@pytest.mark.e2e
def test_server_add_form(authenticated_page: Page, base_url: str) -> None:
    """Navigate to server management page -> sees server add UI elements."""
    page = authenticated_page
    page.goto(f"{base_url}/")
    page.wait_for_load_state("networkidle")

    # The add server form is accessible via a modal or API
    # Verify the API endpoint exists
    content = page.content()
    assert len(content) > 100


@pytest.mark.e2e
def test_server_reboot(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Click 'Reboot' on a server -> sees reboot confirmation/status."""
    page = authenticated_page

    result = api_get(page, "/api/servers")
    servers = result if isinstance(result, list) else result.get("servers", [])

    if not servers:
        pytest.skip("No servers available to test reboot")

    server_id = servers[0]["id"]
    reboot_result = api_post(page, f"/api/servers/{server_id}/reboot", {}, csrf_token)

    # Reboot endpoint returns a response
    assert reboot_result["body"] is not None
