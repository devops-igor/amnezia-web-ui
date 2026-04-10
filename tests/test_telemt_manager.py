"""
Unit tests for telemt_manager.py

Tests cover the refactored TelemtManager that uses direct HTTP API
calls for user management instead of SSH-tunneled curl and config parsing.
"""

from unittest.mock import MagicMock, patch

from telemt_manager import TelemtManager


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
        self.mock_ssh.run_command.return_value = ("Docker version 20.10.0", "", 0)
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

    @patch("telemt_manager.httpx.Client")
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

    @patch("telemt_manager.httpx.Client")
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

    @patch("telemt_manager.httpx.Client")
    def test_api_request_connection_error(self, mock_client_class):
        import httpx

        mock_client = MagicMock()
        mock_client_class.return_value.__enter__.return_value = mock_client
        mock_client.request.side_effect = httpx.RequestError("Connection refused")

        result = self.manager._api_request("GET", "/v1/users")
        assert result is None

    @patch("telemt_manager.httpx.Client")
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
    def test_get_clients_quota_reached_auto_disable(self, mock_api):
        """Users who exceed quota should be auto-disabled."""
        mock_api.return_value = {
            "ok": True,
            "data": [
                {
                    "username": "carol",
                    "secret": "abc123",
                    "links": {"tls": ["tg://proxy?..."]},
                    "total_octets": 10000,
                    "data_quota_bytes": 5000,  # Quota exceeded
                }
            ],
        }

        clients = self.manager.get_clients("telemt")
        assert len(clients) == 1
        assert clients[0]["enabled"] is False
        # toggle_client should have been called via API (not SSH)
        # The toggle_client now uses _api_request, not SSH
        mock_api.assert_called_with("PATCH", "/v1/users/carol", {"secret": ""})

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

        assert result["client_id"] == "existing_user"
        assert result["config"] == ""
        assert result["vpn_link"] == ""


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
