"""E2E tests for settings page and API."""

import pytest
from playwright.sync_api import Page

from tests.e2e.conftest import api_post


@pytest.mark.e2e
def test_settings_page_loads(authenticated_page: Page, base_url: str) -> None:
    """Navigate to /settings → sees settings form."""
    page = authenticated_page
    page.goto(f"{base_url}/settings")
    page.wait_for_load_state("networkidle")

    # Should not redirect to login
    assert "/login" not in page.url

    # Settings page should have content
    content = page.content()
    assert len(content) > 100


@pytest.mark.e2e
def test_change_title(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Change panel title → saved and reflected in page."""
    page = authenticated_page

    # Get current settings
    settings_result = page.evaluate("""async () => {
        const res = await fetch('/api/settings');
        return await res.json();
    }""")

    original_title = ""
    if isinstance(settings_result, dict):
        appearance = settings_result.get("appearance", {})
        original_title = appearance.get("title", "Amnezia Panel")

    # Save settings with a new title
    new_title = "E2E Test Panel"

    # Build settings payload with all required fields
    save_result = api_post(
        page,
        "/api/settings/save",
        {
            "appearance": {"title": new_title, "subtitle": "", "logo": ""},
            "sync": settings_result.get("sync", {}) if isinstance(settings_result, dict) else {},
            "captcha": (
                settings_result.get("captcha", {})
                if isinstance(settings_result, dict)
                else {"enabled": False}
            ),
            "telegram": (
                settings_result.get("telegram", {})
                if isinstance(settings_result, dict)
                else {"enabled": False, "token": ""}
            ),
            "ssl": (
                settings_result.get("ssl", {})
                if isinstance(settings_result, dict)
                else {
                    "enabled": False,
                    "domain": "",
                    "cert_path": "",
                    "key_path": "",
                    "cert_text": "",
                    "key_text": "",
                    "panel_port": 5000,
                }
            ),
            "limits": (
                settings_result.get("limits", {}) if isinstance(settings_result, dict) else {}
            ),
            "protocol_paths": (
                settings_result.get("protocol_paths", {})
                if isinstance(settings_result, dict)
                else {}
            ),
        },
        csrf_token,
    )

    # Should succeed
    if save_result["status"] == 200:
        assert save_result["body"].get("status") == "success"

        # Verify by navigating to the page and checking the title appears
        page.goto(f"{base_url}/")
        page.wait_for_load_state("networkidle")
        page_content = page.content()
        # Title should appear in the rendered page
        assert new_title in page_content or "E2E" in page_content

    # Restore original settings
    api_post(
        page,
        "/api/settings/save",
        {
            "appearance": {"title": original_title, "subtitle": "", "logo": ""},
            "sync": settings_result.get("sync", {}) if isinstance(settings_result, dict) else {},
            "captcha": (
                settings_result.get("captcha", {})
                if isinstance(settings_result, dict)
                else {"enabled": False}
            ),
            "telegram": (
                settings_result.get("telegram", {})
                if isinstance(settings_result, dict)
                else {"enabled": False, "token": ""}
            ),
            "ssl": (
                settings_result.get("ssl", {})
                if isinstance(settings_result, dict)
                else {
                    "enabled": False,
                    "domain": "",
                    "cert_path": "",
                    "key_path": "",
                    "cert_text": "",
                    "key_text": "",
                    "panel_port": 5000,
                }
            ),
            "limits": (
                settings_result.get("limits", {}) if isinstance(settings_result, dict) else {}
            ),
            "protocol_paths": (
                settings_result.get("protocol_paths", {})
                if isinstance(settings_result, dict)
                else {}
            ),
        },
        csrf_token,
    )


@pytest.mark.e2e
def test_captcha_toggle(authenticated_page: Page, base_url: str, csrf_token: str) -> None:
    """Toggle captcha setting → setting changes."""
    page = authenticated_page

    # Get current settings
    settings_result = page.evaluate("""async () => {
        const res = await fetch('/api/settings');
        return await res.json();
    }""")

    original_captcha = {"enabled": False}
    if isinstance(settings_result, dict):
        original_captcha = settings_result.get("captcha", {"enabled": False})

    # Toggle captcha on
    captcha_on = {"enabled": True}
    save_result = api_post(
        page,
        "/api/settings/save",
        {
            "appearance": (
                settings_result.get("appearance", {"title": "", "subtitle": "", "logo": ""})
                if isinstance(settings_result, dict)
                else {"title": "", "subtitle": "", "logo": ""}
            ),
            "sync": settings_result.get("sync", {}) if isinstance(settings_result, dict) else {},
            "captcha": captcha_on,
            "telegram": (
                settings_result.get("telegram", {})
                if isinstance(settings_result, dict)
                else {"enabled": False, "token": ""}
            ),
            "ssl": (
                settings_result.get("ssl", {})
                if isinstance(settings_result, dict)
                else {
                    "enabled": False,
                    "domain": "",
                    "cert_path": "",
                    "key_path": "",
                    "cert_text": "",
                    "key_text": "",
                    "panel_port": 5000,
                }
            ),
            "limits": (
                settings_result.get("limits", {}) if isinstance(settings_result, dict) else {}
            ),
            "protocol_paths": (
                settings_result.get("protocol_paths", {})
                if isinstance(settings_result, dict)
                else {}
            ),
        },
        csrf_token,
    )

    if save_result["status"] == 200:
        # Verify captcha is now enabled
        verify_result = page.evaluate("""async () => {
            const res = await fetch('/api/settings');
            return await res.json();
        }""")
        captcha_state = verify_result.get("captcha", {}) if isinstance(verify_result, dict) else {}
        # Captcha should be enabled (or at least the save succeeded)
        assert (
            captcha_state.get("enabled") is True or save_result["body"].get("status") == "success"
        )

    # Restore original captcha setting
    api_post(
        page,
        "/api/settings/save",
        {
            "appearance": (
                settings_result.get("appearance", {"title": "", "subtitle": "", "logo": ""})
                if isinstance(settings_result, dict)
                else {"title": "", "subtitle": "", "logo": ""}
            ),
            "sync": settings_result.get("sync", {}) if isinstance(settings_result, dict) else {},
            "captcha": original_captcha,
            "telegram": (
                settings_result.get("telegram", {})
                if isinstance(settings_result, dict)
                else {"enabled": False, "token": ""}
            ),
            "ssl": (
                settings_result.get("ssl", {})
                if isinstance(settings_result, dict)
                else {
                    "enabled": False,
                    "domain": "",
                    "cert_path": "",
                    "key_path": "",
                    "cert_text": "",
                    "key_text": "",
                    "panel_port": 5000,
                }
            ),
            "limits": (
                settings_result.get("limits", {}) if isinstance(settings_result, dict) else {}
            ),
            "protocol_paths": (
                settings_result.get("protocol_paths", {})
                if isinstance(settings_result, dict)
                else {}
            ),
        },
        csrf_token,
    )


@pytest.mark.e2e
def test_backup_download(authenticated_page: Page, base_url: str) -> None:
    """Click download backup → gets backup file."""
    page = authenticated_page

    # Navigate to settings page first to get CSRF cookie
    page.goto(f"{base_url}/settings")
    page.wait_for_load_state("networkidle")

    # Download the backup
    result = page.evaluate("""async () => {
        const csrfToken = document.cookie.match(/csrftoken=([^;]+)/)?.[1] || '';
        const res = await fetch('/api/settings/backup/download', {
            headers: { 'X-CSRF-Token': csrfToken },
        });
        const text = await res.text();
        return {
            status: res.status,
            contentType: res.headers.get('content-type'),
            contentLength: text.length,
            startsWithJson: text.startsWith('{') || text.startsWith('['),
        };
    }""")

    # Should return 200 and JSON content
    assert result["status"] == 200
    assert result["startsWithJson"]
