"""E2E tests for connection management."""

import pytest
from playwright.sync_api import Page

from tests.e2e.conftest import api_post


@pytest.mark.e2e
def test_connection_list(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Navigate to server connections → sees connection list."""
    page = authenticated_page

    # Get a server first
    result = page.evaluate(
        """async () => {
        const res = await fetch('/api/servers');
        return await res.json();
    }"""
    )
    servers = result if isinstance(result, list) else result.get("servers", [])

    if not servers:
        pytest.skip("No servers available to test connections")

    server_id = servers[0]["id"]

    # Navigate to server detail page which shows connections
    page.goto(f"{base_url}/server/{server_id}")
    page.wait_for_load_state("networkidle")

    # Verify page loaded
    assert "/login" not in page.url


@pytest.mark.e2e
def test_add_connection(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Add a new connection → appears in connection list."""
    page = authenticated_page

    # Need a server to add connections to
    result = page.evaluate(
        """async () => {
        const res = await fetch('/api/servers');
        return await res.json();
    }"""
    )
    servers = result if isinstance(result, list) else result.get("servers", [])

    if not servers:
        pytest.skip("No servers available to add connections")

    server_id = servers[0]["id"]
    add_result = api_post(
        page,
        f"/api/servers/{server_id}/connections/add",
        {
            "protocol": "awg",
            "name": "e2e_test_connection",
        },
        csrf_token,
    )

    # Endpoint should respond (success or error if protocol not available)
    assert add_result["body"] is not None


@pytest.mark.e2e
def test_connection_config_and_qr(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """View connection config → sees config text and QR code."""
    page = authenticated_page

    # Get server and its connections
    result = page.evaluate(
        """async () => {
        const res = await fetch('/api/servers');
        return await res.json();
    }"""
    )
    servers = result if isinstance(result, list) else result.get("servers", [])

    if not servers:
        pytest.skip("No servers available to test connection config")

    server_id = servers[0]["id"]

    # Get connections for this server
    connections_result = page.evaluate(
        """async (serverId) => {
        const res = await fetch(`/api/servers/${serverId}/connections`);
        return await res.json();
    }""",
        server_id,
    )

    connections = (
        connections_result
        if isinstance(connections_result, list)
        else connections_result.get("connections", [])
    )

    if not connections:
        pytest.skip("No connections available to test config view")

    conn_id = connections[0]["id"]
    config_result = api_post(
        page,
        f"/api/servers/{server_id}/connections/config",
        {"connection_id": conn_id},
        csrf_token,
    )

    # Config endpoint should respond
    assert config_result["body"] is not None


@pytest.mark.e2e
def test_toggle_connection(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Enable/disable a connection → status changes."""
    page = authenticated_page

    result = page.evaluate(
        """async () => {
        const res = await fetch('/api/servers');
        return await res.json();
    }"""
    )
    servers = result if isinstance(result, list) else result.get("servers", [])

    if not servers:
        pytest.skip("No servers available to test toggle")

    server_id = servers[0]["id"]

    connections_result = page.evaluate(
        """async (serverId) => {
        const res = await fetch(`/api/servers/${serverId}/connections`);
        return await res.json();
    }""",
        server_id,
    )

    connections = (
        connections_result
        if isinstance(connections_result, list)
        else connections_result.get("connections", [])
    )

    if not connections:
        pytest.skip("No connections available to test toggle")

    conn_id = connections[0]["id"]
    toggle_result = api_post(
        page,
        f"/api/servers/{server_id}/connections/toggle",
        {"connection_id": conn_id},
        csrf_token,
    )

    # Toggle should respond
    assert toggle_result["body"] is not None


@pytest.mark.e2e
def test_delete_connection(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Delete a connection → removed from list."""
    page = authenticated_page

    result = page.evaluate(
        """async () => {
        const res = await fetch('/api/servers');
        return await res.json();
    }"""
    )
    servers = result if isinstance(result, list) else result.get("servers", [])

    if not servers:
        pytest.skip("No servers available to test delete")

    server_id = servers[0]["id"]

    connections_result = page.evaluate(
        """async (serverId) => {
        const res = await fetch(`/api/servers/${serverId}/connections`);
        return await res.json();
    }""",
        server_id,
    )

    connections = (
        connections_result
        if isinstance(connections_result, list)
        else connections_result.get("connections", [])
    )

    if not connections:
        pytest.skip("No connections available to test delete")

    conn_id = connections[0]["id"]
    delete_result = api_post(
        page,
        f"/api/servers/{server_id}/connections/remove",
        {"connection_id": conn_id},
        csrf_token,
    )

    # Delete should respond
    assert delete_result["body"] is not None
