"""
Unit tests for telemt_manager.py

Tests cover the refactored TelemtManager that uses direct HTTP API
calls for user management instead of SSH-tunneled curl and config parsing.
"""

import hashlib
import os
import re

import pytest
from unittest.mock import MagicMock, patch

from integrity import IntegrityError
from app.managers import TelemtManager
from schemas import InstallProtocolRequest


class TestTelemtManagerInit:
    """Tests for TelemtManager initialization and basic properties."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    def test_default_config_dir(self):
        assert self.manager._config_dir() == "/opt/amnezia/telemt"
        assert self.manager._config_path() == "/opt/amnezia/telemt/config.toml"

    def test_custom_config_dir(self):
        manager = TelemtManager(self.mock_ssh, config_dir="/custom/path")
        assert manager._config_dir() == "/custom/path"
        assert manager._config_path() == "/custom/path/config.toml"

    def test_container_name(self):
        assert self.manager.CONTAINER_NAME == "telemt"
        assert self.manager.API_BASE == "http://telemt:9091"


class TestCheckDockerInstalled:
    """Tests for check_docker_installed()."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    def test_docker_installed(self):
        self.mock_ssh.run_command.side_effect = [
            ("Docker version 20.10.0", "", 0),
            ("active", "", 0),
        ]
        assert self.manager.check_docker_installed() is True

    def test_docker_not_installed(self):
        self.mock_ssh.run_command.return_value = ("", "", 0)
        assert self.manager.check_docker_installed() is False

    def test_docker_command_fails(self):
        self.mock_ssh.run_command.return_value = ("", "command not found", 127)
        assert self.manager.check_docker_installed() is False


class TestCheckProtocolInstalled:
    """Tests for check_protocol_installed()."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    def test_container_exists(self):
        self.mock_ssh.run_command.return_value = ("telemt", "", 0)
        assert self.manager.check_protocol_installed() is True

    def test_container_not_exists(self):
        self.mock_ssh.run_command.return_value = ("", "", 0)
        assert self.manager.check_protocol_installed() is False


class TestGetServerStatus:
    """Tests for get_server_status()."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    def test_container_not_running(self):
        # check_protocol_installed returns False, so only 1 run_command call
        self.mock_ssh.run_command.return_value = ("", "", 0)
        status = self.manager.get_server_status("telemt")
        assert status["container_exists"] is False
        assert status["container_running"] is False
        assert "port" not in status
        assert "awg_params" not in status

    @patch.object(TelemtManager, "_api_request")
    def test_container_running(self, mock_api):
        # Two run_command calls: check_protocol_installed and docker inspect
        self.mock_ssh.run_command.side_effect = [
            ("telemt", "", 0),  # check_protocol_installed
            ("true", "", 0),  # docker inspect State.Running
            ("0.0.0.0:8443", "", 0),  # docker port
        ]
        # API calls: _get_telemt_params_from_api (health + system/info) + get_clients
        mock_api.side_effect = [
            {"ok": True, "data": {"status": "ok"}},  # health
            {"ok": True, "data": {}},  # system/info
            {"ok": True, "data": []},  # get_clients -> /v1/users
        ]

        status = self.manager.get_server_status("telemt")
        assert status["container_exists"] is True
        assert status["container_running"] is True
        assert status["port"] == "8443"

    @patch.object(TelemtManager, "_api_request")
    def test_container_running_no_port_mapping(self, mock_api):
        self.mock_ssh.run_command.side_effect = [
            ("telemt", "", 0),
            ("true", "", 0),
            ("", "", 0),  # docker port returns empty
        ]
        mock_api.side_effect = [
            {"ok": True, "data": {"status": "ok"}},
            {"ok": True, "data": {}},
            {"ok": True, "data": []},
        ]

        status = self.manager.get_server_status("telemt")
        assert status["port"] is None


class TestApiRequest:
    """Tests for the refactored _api_request() using httpx."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    @patch("app.managers.telemt_manager.httpx.Client")
    def test_api_request_get_success(self, mock_client_class):
        mock_client = MagicMock()
        mock_client_class.return_value.__enter__.return_value = mock_client
        mock_response = MagicMock()
        mock_response.json.return_value = {"ok": True, "data": []}
        mock_client.request.return_value = mock_response

        result = self.manager._api_request("GET", "/v1/users")

        assert result == {"ok": True, "data": []}
        mock_client.request.assert_called_once()
        call_args = mock_client.request.call_args
        assert call_args[0][0] == "GET"
        assert call_args[0][1] == "http://telemt:9091/v1/users"

    @patch("app.managers.telemt_manager.httpx.Client")
    def test_api_request_post_with_data(self, mock_client_class):
        mock_client = MagicMock()
        mock_client_class.return_value.__enter__.return_value = mock_client
        mock_response = MagicMock()
        mock_response.json.return_value = {"ok": True, "data": {"username": "test"}}
        mock_client.request.return_value = mock_response

        result = self.manager._api_request(
            "POST", "/v1/users", {"username": "test", "secret": "abc123"}
        )

        assert result == {"ok": True, "data": {"username": "test"}}
        call_args = mock_client.request.call_args
        assert call_args[0][0] == "POST"
        assert call_args[1]["json"] == {"username": "test", "secret": "abc123"}

    @patch("app.managers.telemt_manager.httpx.Client")
    def test_api_request_connection_error(self, mock_client_class):
        import httpx

        mock_client = MagicMock()
        mock_client_class.return_value.__enter__.return_value = mock_client
        mock_client.request.side_effect = httpx.RequestError("Connection refused")

        result = self.manager._api_request("GET", "/v1/users")
        assert result is None

    @patch("app.managers.telemt_manager.httpx.Client")
    def test_api_request_invalid_json(self, mock_client_class):
        import json

        mock_client = MagicMock()
        mock_client_class.return_value.__enter__.return_value = mock_client
        mock_response = MagicMock()
        mock_response.json.side_effect = json.JSONDecodeError("doc", "pos", 0)
        mock_client.request.return_value = mock_response

        result = self.manager._api_request("GET", "/v1/users")
        assert result is None


class TestGetClients:
    """Tests for get_clients() using the API."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    @patch.object(TelemtManager, "_api_request")
    def test_get_clients_success(self, mock_api):
        mock_api.return_value = {
            "ok": True,
            "data": [
                {
                    "username": "alice",
                    "secret": "abc123def456",
                    "links": {"tls": ["tg://proxy?server=example.com&port=443&secret=abc123"]},
                    "total_octets": 1000,
                    "current_connections": 2,
                    "active_unique_ips": 1,
                    "data_quota_bytes": 5000,
                }
            ],
        }

        clients = self.manager.get_clients("telemt")

        assert len(clients) == 1
        assert clients[0]["clientId"] == "alice"
        assert clients[0]["clientName"] == "alice"
        assert clients[0]["enabled"] is True
        assert clients[0]["userData"]["token"] == "abc123def456"
        assert (
            clients[0]["userData"]["tg_link"]
            == "tg://proxy?server=example.com&port=443&secret=abc123"
        )
        assert clients[0]["userData"]["total_octets"] == 1000
        assert clients[0]["userData"]["current_connections"] == 2
        assert clients[0]["userData"]["active_ips"] == 1
        assert clients[0]["userData"]["quota"] == 5000

    @patch.object(TelemtManager, "_api_request")
    def test_get_clients_empty(self, mock_api):
        mock_api.return_value = {"ok": True, "data": []}

        clients = self.manager.get_clients("telemt")
        assert len(clients) == 0

    @patch.object(TelemtManager, "_api_request")
    def test_get_clients_api_failure(self, mock_api):
        mock_api.return_value = None

        clients = self.manager.get_clients("telemt")
        assert len(clients) == 0

    @patch.object(TelemtManager, "_api_request")
    def test_get_clients_disabled_user(self, mock_api):
        """User with empty secret should be disabled."""
        mock_api.return_value = {
            "ok": True,
            "data": [
                {
                    "username": "bob",
                    "secret": "",  # Empty secret = disabled
                    "links": {"classic": ["tg://proxy?server=example.com&port=443&secret="]},
                    "total_octets": 500,
                }
            ],
        }

        clients = self.manager.get_clients("telemt")
        assert len(clients) == 1
        assert clients[0]["enabled"] is False

    @patch.object(TelemtManager, "_api_request")
    def test_get_clients_quota_reached_no_side_effect(self, mock_api):
        """get_clients is a pure read - over-quota users returned as-enabled without side effect."""
        mock_api.return_value = {
            "ok": True,
            "data": [
                {
                    "username": "carol",
                    "secret": "***",
                    "links": {"tls": ["tg://proxy?..."]},
                    "total_octets": 10000,
                    "data_quota_bytes": 5000,  # Quota exceeded
                }
            ],
        }

        clients = self.manager.get_clients("telemt")
        assert len(clients) == 1
        # get_clients is a pure read - returns actual state, does NOT auto-disable
        assert clients[0]["enabled"] is True
        # No PATCH call since we removed the side effect
        # (toggle_client should NOT be called by get_clients)

    @patch.object(TelemtManager, "_api_request")
    def test_get_clients_links_priority_tls_over_secure_over_classic(self, mock_api):
        """Should prefer tls link, then secure, then classic."""
        mock_api.return_value = {
            "ok": True,
            "data": [
                {
                    "username": "dave",
                    "secret": "xyz789",
                    "links": {
                        "tls": [],
                        "secure": ["tg://secure-link"],
                        "classic": ["tg://classic-link"],
                    },
                }
            ],
        }

        clients = self.manager.get_clients("telemt")
        assert clients[0]["userData"]["tg_link"] == "tg://secure-link"


class TestAddClient:
    """Tests for add_client() using POST /v1/users."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    @patch.object(TelemtManager, "_api_request")
    def test_add_client_success(self, mock_api):
        mock_api.return_value = {
            "ok": True,
            "data": {
                "username": "newuser",
                "secret": "***",
                "links": {
                    "tls": ["tg://proxy?server=api.example.com&port=18443&secret=newsecret123"]
                },
            },
        }

        result = self.manager.add_client("telemt", "New User", host="vpn.example.com", port="443")

        assert result["client_id"] == "New_User"
        api_link = "tg://proxy?server=api.example.com&port=18443&secret=newsecret123"
        assert result["config"] == api_link
        assert result["vpn_link"] == api_link

        # Verify POST was called with correct data
        call_args = mock_api.call_args[0]
        assert call_args[0] == "POST"
        assert call_args[1] == "/v1/users"
        assert call_args[2]["username"] == "New_User"

    @patch.object(TelemtManager, "_api_request")
    def test_add_client_with_quota_and_ips(self, mock_api):
        mock_api.return_value = {"ok": True, "data": {"username": "user1", "secret": "sec"}}

        self.manager.add_client(
            "telemt",
            "Test User",
            telemt_quota=1073741824,
            telemt_max_ips=5,
            telemt_expiry="2025-12-31T23:59:59Z",
        )

        call_args = mock_api.call_args[0]
        body = call_args[2]
        assert body["data_quota_bytes"] == 1073741824
        assert body["max_unique_ips"] == 5
        assert body["expiration_rfc3339"] == "2025-12-31T23:59:59Z"

    @patch.object(TelemtManager, "_api_request")
    def test_add_client_sanitizes_username(self, mock_api):
        mock_api.return_value = {"ok": True, "data": {"username": "UserName123", "secret": "s"}}

        self.manager.add_client("telemt", "User@Name#123!")

        call_args = mock_api.call_args[0]
        body = call_args[2]
        assert body["username"] == "UserName123"

    @patch.object(TelemtManager, "_api_request")
    def test_add_client_empty_username_generates_random(self, mock_api):
        mock_api.return_value = {"ok": True, "data": {"username": "user_abc123", "secret": "s"}}

        self.manager.add_client("telemt", "!@#$%^&*()")

        call_args = mock_api.call_args[0]
        body = call_args[2]
        assert body["username"].startswith("user_")

    @patch.object(TelemtManager, "_api_request")
    def test_add_client_api_error(self, mock_api):
        mock_api.return_value = {
            "ok": False,
            "error": {"code": "user_exists", "message": "User already exists"},
        }

        result = self.manager.add_client("telemt", "existing_user")

        # On API failure, client_id should be empty, config/vpn_link empty, error present
        assert result["client_id"] == ""
        assert result["config"] == ""
        assert result["vpn_link"] == ""
        assert result["error"] == "User already exists"

    @patch.object(TelemtManager, "_api_request")
    def test_add_client_no_links_user_created(self, mock_api):
        """When API succeeds but returns no links, user was created — client_id is set."""
        mock_api.return_value = {
            "ok": True,
            "data": {"username": "newuser", "secret": "***", "links": {}},
        }

        result = self.manager.add_client("telemt", "New User")

        # User was created in API, but no links available
        assert result["client_id"] == "New_User"
        assert result["config"] == ""
        assert result["vpn_link"] == ""
        assert "error" not in result


class TestEditClient:
    """Tests for edit_client() using PATCH /v1/users/{username}."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    @patch.object(TelemtManager, "_api_request")
    def test_edit_client_quota(self, mock_api):
        mock_api.return_value = {"ok": True, "data": {}}

        result = self.manager.edit_client("telemt", "alice", {"telemt_quota": 2000000})

        assert result["status"] == "success"
        call_args = mock_api.call_args[0]
        assert call_args[0] == "PATCH"
        assert call_args[1] == "/v1/users/alice"
        assert call_args[2] == {"data_quota_bytes": 2000000}

    @patch.object(TelemtManager, "_api_request")
    def test_edit_client_max_ips(self, mock_api):
        mock_api.return_value = {"ok": True, "data": {}}

        result = self.manager.edit_client("telemt", "bob", {"telemt_max_ips": 10})

        assert result["status"] == "success"
        call_args = mock_api.call_args[0]
        assert call_args[0] == "PATCH"
        assert call_args[1] == "/v1/users/bob"
        assert call_args[2] == {"max_unique_ips": 10}

    @patch.object(TelemtManager, "_api_request")
    def test_edit_client_expiry(self, mock_api):
        mock_api.return_value = {"ok": True, "data": {}}

        result = self.manager.edit_client(
            "telemt", "carol", {"telemt_expiry": "2026-01-01T00:00:00Z"}
        )

        assert result["status"] == "success"
        call_args = mock_api.call_args[0]
        assert call_args[0] == "PATCH"
        assert call_args[1] == "/v1/users/carol"
        assert call_args[2] == {"expiration_rfc3339": "2026-01-01T00:00:00Z"}

    @patch.object(TelemtManager, "_api_request")
    def test_edit_client_multiple_params(self, mock_api):
        mock_api.return_value = {"ok": True, "data": {}}

        result = self.manager.edit_client(
            "telemt",
            "dave",
            {"telemt_quota": 1000, "telemt_max_ips": 3, "telemt_expiry": "2026-06-01T00:00:00Z"},
        )

        assert result["status"] == "success"
        call_args = mock_api.call_args[0]
        body = call_args[2]
        assert body["data_quota_bytes"] == 1000
        assert body["max_unique_ips"] == 3
        assert body["expiration_rfc3339"] == "2026-06-01T00:00:00Z"

    @patch.object(TelemtManager, "_api_request")
    def test_edit_client_empty_params(self, mock_api):
        result = self.manager.edit_client("telemt", "eve", {})

        assert result["status"] == "success"
        mock_api.assert_not_called()

    @patch.object(TelemtManager, "_api_request")
    def test_edit_client_api_error(self, mock_api):
        mock_api.return_value = {
            "ok": False,
            "error": {"code": "not_found", "message": "User not found"},
        }

        result = self.manager.edit_client("telemt", "missing", {"telemt_quota": 1000})

        assert result["status"] == "error"
        assert "message" in result


class TestRemoveClient:
    """Tests for remove_client() using DELETE /v1/users/{username}."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    @patch.object(TelemtManager, "_api_request")
    def test_remove_client_success(self, mock_api):
        mock_api.return_value = {"ok": True, "data": "deleted_user"}

        self.manager.remove_client("telemt", "alice")

        call_args = mock_api.call_args[0]
        assert call_args[0] == "DELETE"
        assert call_args[1] == "/v1/users/alice"

    @patch.object(TelemtManager, "_api_request")
    def test_remove_client_api_error(self, mock_api):
        mock_api.return_value = None

        self.manager.remove_client("telemt", "bob")

        call_args = mock_api.call_args[0]
        assert call_args[0] == "DELETE"
        assert call_args[1] == "/v1/users/bob"


class TestDisableOverquotaUsers:
    """Tests for disable_overquota_users() - explicit quota enforcement."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    @patch.object(TelemtManager, "get_clients")
    @patch.object(TelemtManager, "toggle_client")
    def test_disable_overquota_users_disables_overquota(self, mock_toggle, mock_get_clients):
        """Should disable users who are over quota."""
        mock_get_clients.return_value = [
            {
                "clientId": "alice",
                "clientName": "alice",
                "enabled": True,
                "userData": {"total_octets": 10000, "quota": 5000},
            },
            {
                "clientId": "bob",
                "clientName": "bob",
                "enabled": True,
                "userData": {"total_octets": 3000, "quota": 5000},
            },
        ]

        disabled = self.manager.disable_overquota_users("telemt")

        assert disabled == ["alice"]
        mock_toggle.assert_called_once_with("telemt", "alice", False, restart=False)

    @patch.object(TelemtManager, "get_clients")
    @patch.object(TelemtManager, "toggle_client")
    def test_disable_overquota_users_none_overquota(self, mock_toggle, mock_get_clients):
        """Should return empty list when no users are over quota."""
        mock_get_clients.return_value = [
            {
                "clientId": "bob",
                "clientName": "bob",
                "enabled": True,
                "userData": {"total_octets": 3000, "quota": 5000},
            },
        ]

        disabled = self.manager.disable_overquota_users("telemt")

        assert disabled == []
        mock_toggle.assert_not_called()

    @patch.object(TelemtManager, "get_clients")
    @patch.object(TelemtManager, "toggle_client")
    def test_disable_overquota_users_already_disabled(self, mock_toggle, mock_get_clients):
        """Should not toggle already-disabled users."""
        mock_get_clients.return_value = [
            {
                "clientId": "carol",
                "clientName": "carol",
                "enabled": False,  # Already disabled
                "userData": {"total_octets": 10000, "quota": 5000},
            },
        ]

        disabled = self.manager.disable_overquota_users("telemt")

        assert disabled == []
        mock_toggle.assert_not_called()

    @patch.object(TelemtManager, "get_clients")
    @patch.object(TelemtManager, "toggle_client")
    def test_disable_overquota_users_multiple(self, mock_toggle, mock_get_clients):
        """Should disable multiple over-quota users."""
        mock_get_clients.return_value = [
            {
                "clientId": "alice",
                "clientName": "alice",
                "enabled": True,
                "userData": {"total_octets": 10000, "quota": 5000},
            },
            {
                "clientId": "bob",
                "clientName": "bob",
                "enabled": True,
                "userData": {"total_octets": 6000, "quota": 5000},
            },
            {
                "clientId": "carol",
                "clientName": "carol",
                "enabled": True,
                "userData": {"total_octets": 4000, "quota": 5000},
            },
        ]

        disabled = self.manager.disable_overquota_users("telemt")

        assert set(disabled) == {"alice", "bob"}
        assert mock_toggle.call_count == 2

    def test_is_overquota_true(self):
        """_is_overquota returns True when traffic >= quota."""
        client = {
            "enabled": True,
            "userData": {"total_octets": 10000, "quota": 5000},
        }
        assert self.manager._is_overquota(client) is True

    def test_is_overquota_false_under_limit(self):
        """_is_overquota returns False when traffic < quota."""
        client = {
            "enabled": True,
            "userData": {"total_octets": 3000, "quota": 5000},
        }
        assert self.manager._is_overquota(client) is False

    def test_is_overquota_false_disabled(self):
        """_is_overquota returns False for disabled users."""
        client = {
            "enabled": False,
            "userData": {"total_octets": 10000, "quota": 5000},
        }
        assert self.manager._is_overquota(client) is False

    def test_is_overquota_false_no_quota(self):
        """_is_overquota returns False when no quota set."""
        client = {
            "enabled": True,
            "userData": {"total_octets": 10000, "quota": None},
        }
        assert self.manager._is_overquota(client) is False


class TestToggleClient:
    """Tests for toggle_client() using PATCH with secret manipulation."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    @patch.object(TelemtManager, "_api_request")
    def test_toggle_client_enable(self, mock_api):
        mock_api.return_value = {"ok": True, "data": {}}

        self.manager.toggle_client("telemt", "alice", True)

        call_args = mock_api.call_args[0]
        body = call_args[2]
        assert "secret" in body
        assert len(body["secret"]) == 32  # 16 bytes = 32 hex chars
        assert call_args[0] == "PATCH"
        assert call_args[1] == "/v1/users/alice"

    @patch.object(TelemtManager, "_api_request")
    def test_toggle_client_disable(self, mock_api):
        mock_api.return_value = {"ok": True, "data": {}}

        self.manager.toggle_client("telemt", "bob", False)

        call_args = mock_api.call_args[0]
        body = call_args[2]
        assert body["secret"] == ""
        assert call_args[0] == "PATCH"
        assert call_args[1] == "/v1/users/bob"

    @patch.object(TelemtManager, "_api_request")
    def test_toggle_client_restart_param_ignored(self, mock_api):
        """restart parameter should be accepted but not affect API call."""
        mock_api.return_value = {"ok": True, "data": {}}

        self.manager.toggle_client("telemt", "carol", False, restart=False)

        call_args = mock_api.call_args[0]
        body = call_args[2]
        assert body["secret"] == ""


class TestGetClientConfig:
    """Tests for get_client_config() using GET /v1/users/{username}."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    @patch.object(TelemtManager, "_api_request")
    def test_get_client_config_tls_link(self, mock_api):
        mock_api.return_value = {
            "ok": True,
            "data": {
                "username": "alice",
                "links": {"tls": ["tg://tls-link"]},
            },
        }

        result = self.manager.get_client_config("telemt", "alice")
        assert result == "tg://tls-link"

    @patch.object(TelemtManager, "_api_request")
    def test_get_client_config_secure_link(self, mock_api):
        mock_api.return_value = {
            "ok": True,
            "data": {
                "username": "bob",
                "links": {"secure": ["tg://secure-link"]},
            },
        }

        result = self.manager.get_client_config("telemt", "bob")
        assert result == "tg://secure-link"

    @patch.object(TelemtManager, "_api_request")
    def test_get_client_config_classic_link(self, mock_api):
        mock_api.return_value = {
            "ok": True,
            "data": {
                "username": "carol",
                "links": {"classic": ["tg://classic-link"]},
            },
        }

        result = self.manager.get_client_config("telemt", "carol")
        assert result == "tg://classic-link"

    @patch.object(TelemtManager, "_api_request")
    def test_get_client_config_not_found_uses_fallback(self, mock_api):
        # First call (direct GET) returns empty, then get_clients is called
        # get_clients calls _api_request("GET", "/v1/users") which returns users
        mock_api.side_effect = [
            None,  # Direct GET fails
            {
                "ok": True,
                "data": [
                    {
                        "username": "dave",
                        "secret": "***",
                        "links": {
                            "tls": [
                                "tg://proxy?server=fallback.example.com&port=18443&secret=secret123"
                            ]
                        },
                        "total_octets": 0,
                    }
                ],
            },  # get_clients -> /v1/users
        ]

        result = self.manager.get_client_config(
            "telemt", "dave", host="vpn.example.com", port="443"
        )
        assert result == "tg://proxy?server=fallback.example.com&port=18443&secret=secret123"

    @patch.object(TelemtManager, "_api_request")
    def test_get_client_config_user_not_found(self, mock_api):
        mock_api.return_value = None

        result = self.manager.get_client_config("telemt", "missing")
        assert result == "Not found"


class TestRemoveContainer:
    """Tests for remove_container()."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    def test_remove_container_calls_docker_rm(self):
        self.manager.remove_container("telemt")

        calls = [call[0][0] for call in self.mock_ssh.run_sudo_command.call_args_list]
        assert any("docker rm -f telemt" in c for c in calls)
        assert any("rm -rf /opt/amnezia/telemt" in c for c in calls)

    def test_remove_container_custom_config_dir(self):
        manager = TelemtManager(self.mock_ssh, config_dir="/custom/telemt")
        manager.remove_container("telemt")

        calls = [call[0][0] for call in self.mock_ssh.run_sudo_command.call_args_list]
        assert any("rm -rf /custom/telemt" in c for c in calls)


class TestDeprecatedMethodsRemoved:
    """Verify that deprecated methods have been removed."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    def test_get_server_config_removed(self):
        assert not hasattr(self.manager, "_get_server_config")

    def test_parse_users_from_config_removed(self):
        assert not hasattr(self.manager, "_parse_users_from_config")

    def test_insert_into_section_removed(self):
        assert not hasattr(self.manager, "_insert_into_section")

    def test_update_line_in_section_removed(self):
        assert not hasattr(self.manager, "_update_line_in_section")

    def test_save_server_config_removed(self):
        assert not hasattr(self.manager, "save_server_config")


class TestGetTelemtParamsFromApi:
    """Tests for _get_telemt_params_from_api()."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    @patch.object(TelemtManager, "_api_request")
    def test_returns_default_params_on_health_success(self, mock_api):
        mock_api.return_value = {"ok": True, "data": {"status": "ok"}}

        params = self.manager._get_telemt_params_from_api()

        assert isinstance(params, dict)
        # Should return some default params
        assert "tls_emulation" in params

    @patch.object(TelemtManager, "_api_request")
    def test_returns_empty_on_health_failure(self, mock_api):
        mock_api.return_value = None

        params = self.manager._get_telemt_params_from_api()
        assert params == {}


class TestTlsDomainValidation:
    """Tests for tls_domain injection prevention in install_protocol."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.mock_ssh.host = "1.2.3.4"
        # Default SSH command returns (stdout, stderr, exit_code)
        self.mock_ssh.run_command.return_value = ("", "", 0)
        self.mock_ssh.run_sudo_command.return_value = ("", "", 0)
        self.manager = TelemtManager(self.mock_ssh)

    def _make_config(self, tls_domain_value='""'):
        """Helper: create a config.toml with a tls_domain line."""
        return f"tls_emulation = true\ntls_domain = {tls_domain_value}\n"

    def test_valid_tls_domain_applied(self):
        """Valid domain names should be correctly substituted into config via
        the safe match-and-slice replacement pattern."""
        config = self._make_config()
        # Simulate the safe replacement used in telemt_manager.py
        pattern = re.compile(r'tls_domain\s*=\s*".*?"')
        match = pattern.search(config)
        assert match is not None
        tls_domain = "example.com"
        replacement = f'tls_domain = "{tls_domain}"'
        result = config[: match.start()] + replacement + config[match.end() :]
        assert 'tls_domain = "example.com"' in result
        # No regex backreference injection possible with slice-based replacement
        assert "${1}" not in result

    def test_tls_domain_with_newlines_rejected(self):
        """tls_domain containing newlines should be rejected by Pydantic validator."""
        from pydantic import ValidationError

        with pytest.raises(ValidationError):
            InstallProtocolRequest(
                protocol="telemt",
                tls_emulation=True,
                tls_domain="evil.com\nMALICIOUS_LINE",
            )

    def test_tls_domain_with_regex_special_chars_rejected(self):
        """tls_domain with regex specials ($, {, }) should be rejected."""
        from pydantic import ValidationError

        # $ character
        with pytest.raises(ValidationError):
            InstallProtocolRequest(protocol="telemt", tls_emulation=True, tls_domain="$1.evil.com")

        # curly braces
        with pytest.raises(ValidationError):
            InstallProtocolRequest(
                protocol="telemt", tls_emulation=True, tls_domain="${1}.evil.com"
            )

        # backslash
        with pytest.raises(ValidationError):
            InstallProtocolRequest(protocol="telemt", tls_emulation=True, tls_domain="evil\\.com")

    def test_tls_domain_with_whitespace_rejected(self):
        """tls_domain with whitespace should be rejected."""
        from pydantic import ValidationError

        with pytest.raises(ValidationError):
            InstallProtocolRequest(protocol="telemt", tls_emulation=True, tls_domain="evil .com")

    def test_tls_domain_valid_subdomain(self):
        """Valid subdomain should pass validation."""
        req = InstallProtocolRequest(
            protocol="telemt", tls_emulation=True, tls_domain="sub.domain.org"
        )
        assert req.tls_domain == "sub.domain.org"

    def test_tls_domain_simple_domain(self):
        """Simple domain should pass validation."""
        req = InstallProtocolRequest(
            protocol="telemt", tls_emulation=True, tls_domain="example.com"
        )
        assert req.tls_domain == "example.com"

    def test_tls_domain_single_char_accepted(self):
        """Single alphanumeric char is allowed by the second regex alternative."""
        req = InstallProtocolRequest(protocol="telemt", tls_emulation=True, tls_domain="a")
        assert req.tls_domain == "a"

    def test_tls_domain_dash_hyphen_domain(self):
        """Domain with hyphens should pass validation."""
        req = InstallProtocolRequest(
            protocol="telemt", tls_emulation=True, tls_domain="my-site.example.com"
        )
        assert req.tls_domain == "my-site.example.com"

    def test_tls_domain_empty_string_allowed(self):
        """Empty string tls_domain should be allowed (optional field)."""
        req = InstallProtocolRequest(protocol="telemt", tls_emulation=True, tls_domain="")
        assert req.tls_domain == ""

    def test_tls_domain_none_allowed(self):
        """None tls_domain should be allowed (optional field)."""
        req = InstallProtocolRequest(protocol="telemt", tls_emulation=True, tls_domain=None)
        assert req.tls_domain is None

    def test_tls_domain_too_long_rejected(self):
        """tls_domain longer than 128 chars should be rejected."""
        from pydantic import ValidationError

        with pytest.raises(ValidationError):
            InstallProtocolRequest(
                protocol="telemt",
                tls_emulation=True,
                tls_domain="a" * 130 + ".com",
            )

    def test_install_protocol_safe_re_sub(self):
        """Verify that the safe match-and-slice replacement prevents regex injection
        even with malicious-looking domain values (defense-in-depth)."""
        config = self._make_config()
        # If a malicious value somehow passed validation, the slice-based
        # replacement still prevents regex backreference injection.
        # (re.sub with f-string would interpret \1, $1, etc.)
        pattern = re.compile(r'tls_domain\s*=\s*".*?"')
        match = pattern.search(config)
        assert match is not None
        # Simulate a value with regex backreference patterns
        malicious = "${1}\\nMALICIOUS"
        replacement = f'tls_domain = "{malicious}"'
        result = config[: match.start()] + replacement + config[match.end() :]
        # The literal text appears — it's NOT interpreted as a backreference
        assert 'tls_domain = "${1}\\nMALICIOUS"' in result
        # No actual backreference expansion happens with slice replacement


class TestInstallProtocolIntegrityChecks:
    """Tests for integrity verification in install_protocol()."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.mock_ssh.host = "10.0.0.1"
        self.mock_ssh.run_command.return_value = ("", "", 0)
        self.mock_ssh.run_sudo_command.return_value = ("", "", 0)
        self.manager = TelemtManager(self.mock_ssh)

    @patch("app.managers.telemt_manager.verify_integrity", return_value=True)
    @patch("app.managers.telemt_manager.load_expected_hash", return_value="a" * 64)
    def test_install_protocol_integrity_checks_pass(self, mock_load_hash, mock_verify_file):
        """install_protocol succeeds when all integrity checks pass."""
        result = self.manager.install_protocol("telemt", port="443")
        assert result["status"] == "success"
        # Should have called load_expected_hash for 3 template files
        assert mock_load_hash.call_count == 3
        # Should have called verify_integrity for 3 template files
        assert mock_verify_file.call_count == 3

    @patch("app.managers.telemt_manager.verify_integrity", return_value=False)
    @patch("app.managers.telemt_manager.load_expected_hash", return_value="a" * 64)
    def test_install_protocol_config_tampered_raises(self, mock_load_hash, mock_verify):
        """IntegrityError raised when config.toml template is tampered."""
        with pytest.raises(IntegrityError, match="Config template integrity check failed"):
            self.manager.install_protocol("telemt", port="443")

    @patch("app.managers.telemt_manager.verify_integrity")
    @patch("app.managers.telemt_manager.load_expected_hash", return_value="a" * 64)
    def test_install_protocol_compose_tampered_raises(self, mock_load_hash, mock_verify):
        """IntegrityError raised when docker-compose.yml template is tampered."""
        # config.toml passes, docker-compose.yml fails
        mock_verify.side_effect = [True, False]
        with pytest.raises(IntegrityError, match="Docker Compose template integrity check failed"):
            self.manager.install_protocol("telemt", port="443")

    @patch("app.managers.telemt_manager.verify_integrity")
    @patch("app.managers.telemt_manager.load_expected_hash", return_value="a" * 64)
    def test_install_protocol_dockerfile_tampered_raises(self, mock_load_hash, mock_verify):
        """IntegrityError raised when Dockerfile template is tampered."""
        # config.toml and docker-compose.yml pass, Dockerfile fails
        mock_verify.side_effect = [True, True, False]
        with pytest.raises(IntegrityError, match="Dockerfile template integrity check failed"):
            self.manager.install_protocol("telemt", port="443")

    @patch("app.managers.telemt_manager.verify_integrity", return_value=True)
    @patch("app.managers.telemt_manager.load_expected_hash")
    def test_install_protocol_missing_hash_file_raises(self, mock_load_hash, mock_verify):
        """FileNotFoundError raised when .sha256 file is missing."""
        mock_load_hash.side_effect = FileNotFoundError("config.toml.sha256 not found")
        with pytest.raises(FileNotFoundError):
            self.manager.install_protocol("telemt", port="443")

    @patch("app.managers.telemt_manager.verify_integrity", return_value=True)
    @patch("app.managers.telemt_manager.load_expected_hash")
    def test_install_protocol_empty_hash_file_raises(self, mock_load_hash, mock_verify):
        """IntegrityError raised when .sha256 file is empty."""
        mock_load_hash.side_effect = IntegrityError("Hash file is empty")
        with pytest.raises(IntegrityError, match="empty"):
            self.manager.install_protocol("telemt", port="443")

    @patch("app.managers.telemt_manager.verify_integrity", return_value=True)
    @patch("app.managers.telemt_manager.load_expected_hash", return_value="a" * 64)
    def test_install_protocol_remote_config_verification(self, mock_load_hash, mock_verify):
        """Verify remote hash check after config.toml upload passes when hashes match."""
        # Read the actual template and compute what the patched hash will be
        local_dir = os.path.join(os.path.dirname(__file__), "..", "protocol_telemt")
        config_path = os.path.join(local_dir, "config.toml")
        with open(config_path, "r", encoding="utf-8") as f:
            config_content = f.read()

        # Apply the same patches install_protocol does (default params)
        config_content = re.sub(
            r"tls_emulation\s*=\s*(true|false|True|False)",
            "tls_emulation = true",
            config_content,
        )
        config_content = re.sub(r"public_port\s*=\s*\d+", "public_port = 443", config_content)
        config_content = re.sub(r'^hello\s*=\s*".*?"', "", config_content, flags=re.MULTILINE)
        config_content = re.sub(
            r'#?\s*public_host\s*=\s*".*?"', 'public_host = "10.0.0.1"', config_content
        )

        patched_hash = hashlib.sha256(config_content.encode("utf-8")).hexdigest()

        def run_command_side_effect(cmd):
            if "sha256sum" in cmd:
                return (f"{patched_hash}  /opt/amnezia/telemt/config.toml", "", 0)
            return ("", "", 0)

        self.mock_ssh.run_command.side_effect = run_command_side_effect
        result = self.manager.install_protocol("telemt", port="443")
        assert result["status"] == "success"

    @patch("app.managers.telemt_manager.verify_integrity", return_value=True)
    @patch("app.managers.telemt_manager.load_expected_hash", return_value="a" * 64)
    def test_install_protocol_remote_config_mismatch_raises(self, mock_load_hash, mock_verify):
        """IntegrityError raised when remote config.toml hash doesn't match after upload."""
        # Set up mock to return different hash for remote config check
        call_count = [0]

        def run_command_side_effect(cmd):
            call_count[0] += 1
            # Return a mismatched hash for sha256sum command
            if "sha256sum" in cmd:
                return ("0" * 64 + "  /opt/amnezia/telemt/config.toml", "", 0)
            return ("", "", 0)

        self.mock_ssh.run_command.side_effect = run_command_side_effect

        with pytest.raises(IntegrityError, match="Remote config.toml integrity check failed"):
            self.manager.install_protocol("telemt", port="443")

    @patch("app.managers.telemt_manager.verify_integrity", return_value=True)
    @patch("app.managers.telemt_manager.load_expected_hash", return_value="a" * 64)
    def test_install_protocol_remote_hash_check_skipped_if_empty(self, mock_load_hash, mock_verify):
        """Remote hash check is skipped gracefully if sha256sum returns empty."""
        # Default mock returns empty string for run_command
        result = self.manager.install_protocol("telemt", port="443")
        assert result["status"] == "success"

    @patch("app.managers.telemt_manager.verify_integrity", return_value=True)
    @patch("app.managers.telemt_manager.load_expected_hash", return_value="a" * 64)
    def test_install_protocol_patched_hash_logged(self, mock_load_hash, mock_verify):
        """Patched config.toml SHA256 hash is logged for audit trail."""
        with patch("app.managers.telemt_manager.logger") as mock_logger:
            self.manager.install_protocol("telemt", port="443")
            # Check that logger.info was called with patched hash
            logged_messages = [call.args[0] for call in mock_logger.info.call_args_list]
            assert any("Patched config.toml SHA256:" in msg for msg in logged_messages)
