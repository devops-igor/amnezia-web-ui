"""
Unit tests for mtproxyl_manager.py (MTProxyLManager).

Tests cover the MTProxyLManager class that replaces TelemtManager,
using SSH + CLI commands to communicate with MTProxyL on remote servers.
All SSH operations are mocked.
"""

import pytest
from unittest.mock import MagicMock

from app.managers import MTProxyLManager

# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def mock_ssh():
    return MagicMock()


@pytest.fixture
def manager(mock_ssh):
    return MTProxyLManager(mock_ssh)


# ---------------------------------------------------------------------------
# TestMTProxyLManagerInit
# ---------------------------------------------------------------------------


class TestMTProxyLManagerInit:
    """Tests for MTProxyLManager initialization and constants."""

    def test_constructor(self, mock_ssh):
        mgr = MTProxyLManager(mock_ssh)
        assert mgr.ssh is mock_ssh

    def test_constants(self, mock_ssh):
        assert MTProxyLManager.CONTAINER_NAME == "mtproxyl"
        assert MTProxyLManager.CLI_PATH == "/usr/local/bin/mtproxyl"
        assert MTProxyLManager.SECRETS_FILE == "/opt/mtproxyl/secrets.conf"
        assert MTProxyLManager.SETTINGS_FILE == "/opt/mtproxyl/settings.conf"


# ---------------------------------------------------------------------------
# TestCheckProtocolInstalled
# ---------------------------------------------------------------------------


class TestCheckProtocolInstalled:
    """Tests for check_protocol_installed()."""

    def test_installed_running(self, manager, mock_ssh):
        """When status --json returns a running status, protocol is installed."""
        mock_ssh.run_command.return_value = (
            '{"status":"running","port":18443}',
            "",
            0,
        )
        assert manager.check_protocol_installed() is True

    def test_installed_stopped(self, manager, mock_ssh):
        """When status --json returns a stopped status, protocol is installed."""
        mock_ssh.run_command.return_value = (
            '{"status":"stopped","port":0}',
            "",
            0,
        )
        assert manager.check_protocol_installed() is True

    def test_not_installed(self, manager, mock_ssh):
        """When status --json fails, protocol is not installed."""
        mock_ssh.run_command.return_value = ("", "mtproxyl: command not found", 127)
        assert manager.check_protocol_installed() is False

    def test_installed_empty_output(self, manager, mock_ssh):
        """When status --json returns empty, protocol is not installed."""
        mock_ssh.run_command.return_value = ("", "", 1)
        assert manager.check_protocol_installed() is False


# ---------------------------------------------------------------------------
# TestParseStatusJson
# ---------------------------------------------------------------------------


class TestParseStatusJson:
    """Tests for _parse_status_json()."""

    def test_valid_json(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = (
            '{"status":"running","port":18443,"domain":"cloud.hostup.se","connections":40}',
            "",
            0,
        )
        result = manager._parse_status_json()
        assert result is not None
        assert result["status"] == "running"
        assert result["port"] == 18443
        assert result["domain"] == "cloud.hostup.se"

    def test_invalid_json(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("not json", "", 0)
        assert manager._parse_status_json() is None

    def test_command_fails(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("", "error", 1)
        assert manager._parse_status_json() is None


# ---------------------------------------------------------------------------
# TestGetServerStatus
# ---------------------------------------------------------------------------


class TestGetServerStatus:
    """Tests for get_server_status()."""

    def test_not_installed(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("", "not found", 1)
        status = manager.get_server_status("telemt")
        assert status["container_exists"] is False
        assert status["container_running"] is False
        assert "port" not in status

    def test_running_with_clients(self, manager, mock_ssh):
        """When running, returns port and client count from secrets.conf."""
        secrets_content = (
            "# MTProxyL secrets\n"
            "user1|ee161655fa13a99629c566dcc682dac3|1783512971|true|0|0|0|0|\n"
            "user2|ee161655fa13a99629c566dcc682dac4|1783512972|false|0|0|0|0|\n"
        )
        mock_ssh.run_command.side_effect = [
            # _parse_status_json
            ('{"status":"running","port":18443,"domain":"cloud.hostup.se"}', "", 0),
            # _parse_secrets
            (secrets_content, "", 0),
            # _parse_traffic
            ("", "", 0),
        ]
        status = manager.get_server_status("telemt")
        assert status["container_exists"] is True
        assert status["container_running"] is True
        assert status["port"] == "18443"
        assert status["clients_count"] == 2
        assert status["awg_params"]["tls_domain"] == "cloud.hostup.se"

    def test_stopped_status(self, manager, mock_ssh):
        mock_ssh.run_command.side_effect = [
            ('{"status":"stopped","port":0}', "", 0),
            ("", "", 0),
        ]
        status = manager.get_server_status("telemt")
        assert status["container_exists"] is True
        assert status["container_running"] is False
        assert "port" not in status


# ---------------------------------------------------------------------------
# TestParseSecrets
# ---------------------------------------------------------------------------


class TestParseSecrets:
    """Tests for _parse_secrets()."""

    def test_valid_secrets(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = (
            "# MTProxyL — база секретов v1.1.0\n"
            "tg_proxy|ee161655fa13a99629c566dcc682dac3|1783512971|true|0|0|0|0|\n"
            "test_user|ee161655fa13a99629c566dcc682dac4|1783512972|false|10|5|1073741824|0|notes\n",
            "",
            0,
        )
        clients = manager._parse_secrets()
        assert len(clients) == 2

        c1 = clients[0]
        assert c1["clientId"] == "tg_proxy"
        assert c1["clientName"] == "tg_proxy"
        assert c1["enabled"] is True
        assert c1["creationDate"] == "1783512971"
        assert c1["userData"]["token"] == "ee161655fa13a99629c566dcc682dac3"
        assert c1["userData"]["quota"] is None
        assert c1["userData"]["expiry"] is None

        c2 = clients[1]
        assert c2["clientId"] == "test_user"
        assert c2["enabled"] is False
        assert c2["userData"]["quota"] == 1073741824
        assert c2["userData"]["active_ips"] == 5
        # "0" expiry means "never" — returned as None for consistency with TelemtManager
        assert c2["userData"]["expiry"] is None

    def test_empty_secrets(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("# MTProxyL secrets\n", "", 0)
        assert manager._parse_secrets() == []

    def test_only_comments(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = (
            "# MTProxyL — база секретов v1.1.0\n# Comment line\n",
            "",
            0,
        )
        assert manager._parse_secrets() == []

    def test_malformed_lines_skipped(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = (
            "valid|secret|123|true|0|0|0|0|\n"
            "invalid|not|enough|fields\n"
            "also_valid|secret2|456|false|0|0|0|0|extra|\n",
            "",
            0,
        )
        clients = manager._parse_secrets()
        assert len(clients) == 2
        assert clients[0]["clientId"] == "valid"
        assert clients[1]["clientId"] == "also_valid"


# ---------------------------------------------------------------------------
# TestGetClients
# ---------------------------------------------------------------------------


class TestGetClients:
    """Tests for get_clients()."""

    def test_returns_list_of_clients(self, manager, mock_ssh):
        secrets_content = "user1|ee161655fa13a99629c566dcc682dac3|1783512971|true|0|0|0|0|\n"
        mock_ssh.run_command.side_effect = [
            (secrets_content, "", 0),
            ("", "", 0),
        ]
        clients = manager.get_clients("telemt")
        assert isinstance(clients, list)
        assert len(clients) == 1
        assert clients[0]["clientId"] == "user1"

    def test_enriches_with_traffic(self, manager, mock_ssh):
        secrets_content = "tg_proxy|ee161655|1783512971|true|0|0|0|0|\n"
        traffic_output = "● tg_proxy: ↓ 1.96 ГБ  ↑ 96.64 ГБ  соед: 41\n"
        mock_ssh.run_command.side_effect = [
            (secrets_content, "", 0),
            (traffic_output, "", 0),
        ]
        clients = manager.get_clients("telemt")
        assert len(clients) == 1
        # 1.96 GB + 96.64 GB in bytes
        assert clients[0]["userData"]["total_octets"] > 0
        assert clients[0]["userData"]["current_connections"] == 41


# ---------------------------------------------------------------------------
# TestAddClient
# ---------------------------------------------------------------------------


class TestAddClient:
    """Tests for add_client()."""

    def test_add_client_success(self, manager, mock_ssh):
        mock_ssh.run_command.side_effect = [
            # secret add
            ("Secret added: testuser", "", 0),
            # secret link
            ("Some header\ntg://proxy?server=1.2.3.4&port=18443&secret=ee1616\nMore info", "", 0),
        ]
        result = manager.add_client("telemt", "testuser", "1.2.3.4", "18443")
        assert result["client_id"] == "testuser"
        assert result["vpn_link"] == "tg://proxy?server=1.2.3.4&port=18443&secret=ee1616"
        assert "config" in result

    def test_add_client_with_limits(self, manager, mock_ssh):
        mock_ssh.run_command.side_effect = [
            ("Secret added: user_with_limits", "", 0),
            # setlimits called with formatted limits
            ("", "", 0),
            # secret link
            ("tg://proxy?secret=test123", "", 0),
        ]
        result = manager.add_client(
            "telemt",
            "user_with_limits",
            telemt_quota=1073741824,
            telemt_max_ips=5,
            telemt_expiry="0",
        )
        assert result["client_id"] == "user_with_limits"
        # Verify setlimits was called (second call)
        calls = mock_ssh.run_command.call_args_list
        assert len(calls) == 3
        setlimits_call = calls[1][0][0]
        assert "secret setlimits" in setlimits_call
        assert "1073741824" in setlimits_call
        assert "5" in setlimits_call

    def test_add_client_sanitizes_name(self, manager, mock_ssh):
        mock_ssh.run_command.side_effect = [
            ("Secret added: Test User 123", "", 0),
            ("tg://proxy?secret=test", "", 0),
        ]
        result = manager.add_client("telemt", "Test User 123!")
        # Name should be sanitized to alphanumeric + underscore/dash
        assert result["client_id"] == "Test_User_123"
        # Verify CLI was called with sanitized name
        calls = mock_ssh.run_command.call_args_list
        assert "Test_User_123" in calls[0][0][0]

    def test_add_client_generates_name_if_empty(self, manager, mock_ssh):
        mock_ssh.run_command.side_effect = [
            ("Secret added: user_abc12345", "", 0),
            ("tg://proxy?secret=test", "", 0),
        ]
        result = manager.add_client("telemt", "!!!")
        assert result["client_id"].startswith("user_")
        assert len(result["client_id"]) <= 32

    def test_add_client_failure(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("", "error: invalid name", 1)
        result = manager.add_client("telemt", "baduser")
        assert result["client_id"] == ""
        assert "error" in result


# ---------------------------------------------------------------------------
# TestEditClient
# ---------------------------------------------------------------------------


class TestEditClient:
    """Tests for edit_client()."""

    def test_edit_client_success(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("Limits updated", "", 0)
        result = manager.edit_client(
            "telemt",
            "testuser",
            {"telemt_quota": 1073741824, "telemt_max_ips": 3},
        )
        assert result == {"status": "success"}
        mock_ssh.run_command.assert_called_once()
        call_str = mock_ssh.run_command.call_args[0][0]
        assert "secret setlimits" in call_str
        assert "testuser" in call_str
        assert "1073741824" in call_str

    def test_edit_client_no_changes(self, manager, mock_ssh):
        result = manager.edit_client("telemt", "testuser", {})
        assert result == {"status": "success"}
        mock_ssh.run_command.assert_not_called()

    def test_edit_client_failure(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("", "error: client not found", 1)
        result = manager.edit_client("telemt", "ghostuser", {"telemt_quota": 100})
        assert result["status"] == "error"
        assert "error" in result["message"]


# ---------------------------------------------------------------------------
# TestRemoveClient
# ---------------------------------------------------------------------------


class TestRemoveClient:
    """Tests for remove_client()."""

    def test_remove_client_success(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("Client removed", "", 0)
        manager.remove_client("telemt", "testuser")
        mock_ssh.run_command.assert_called_once()
        assert "secret remove" in mock_ssh.run_command.call_args[0][0]
        assert "testuser" in mock_ssh.run_command.call_args[0][0]

    def test_remove_client_failure_logs(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("", "not found", 1)
        # Should not raise, just logs
        manager.remove_client("telemt", "ghost")


# ---------------------------------------------------------------------------
# TestToggleClient
# ---------------------------------------------------------------------------


class TestToggleClient:
    """Tests for toggle_client()."""

    def test_toggle_enable(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("Client enabled", "", 0)
        manager.toggle_client("telemt", "testuser", True)
        mock_ssh.run_command.assert_called_once()
        assert "secret enable" in mock_ssh.run_command.call_args[0][0]
        assert "testuser" in mock_ssh.run_command.call_args[0][0]

    def test_toggle_disable(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("Client disabled", "", 0)
        manager.toggle_client("telemt", "testuser", False)
        mock_ssh.run_command.assert_called_once()
        assert "secret disable" in mock_ssh.run_command.call_args[0][0]
        assert "testuser" in mock_ssh.run_command.call_args[0][0]


# ---------------------------------------------------------------------------
# TestGetClientConfig
# ---------------------------------------------------------------------------


class TestGetClientConfig:
    """Tests for get_client_config()."""

    def test_returns_tg_link(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = (
            "Header line\n"
            "tg://proxy?server=64.112.127.200&port=18443&secret=eeee1616\n"
            "Footer line\n",
            "",
            0,
        )
        link = manager.get_client_config("telemt", "testuser", "64.112.127.200", "18443")
        assert link == "tg://proxy?server=64.112.127.200&port=18443&secret=eeee1616"

    def test_not_found(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("No such client", "", 0)
        link = manager.get_client_config("telemt", "ghost", "", "")
        assert link == "Not found"

    def test_empty_output(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("", "", 0)
        link = manager.get_client_config("telemt", "test", "", "")
        assert link == "Not found"


# ---------------------------------------------------------------------------
# TestIsOverquota
# ---------------------------------------------------------------------------


class TestIsOverquota:
    """Tests for _is_overquota()."""

    def test_over_quota(self, manager):
        client = {
            "enabled": True,
            "userData": {"total_octets": 2000000000, "quota": 1000000000},
        }
        assert manager._is_overquota(client) is True

    def test_under_quota(self, manager):
        client = {
            "enabled": True,
            "userData": {"total_octets": 500000000, "quota": 1000000000},
        }
        assert manager._is_overquota(client) is False

    def test_disabled_not_overquota(self, manager):
        client = {
            "enabled": False,
            "userData": {"total_octets": 2000000000, "quota": 1000000000},
        }
        assert manager._is_overquota(client) is False

    def test_no_quota_set(self, manager):
        client = {
            "enabled": True,
            "userData": {"total_octets": 9999999999, "quota": None},
        }
        assert manager._is_overquota(client) is False


# ---------------------------------------------------------------------------
# TestDisableOverquotaUsers
# ---------------------------------------------------------------------------


class TestDisableOverquotaUsers:
    """Tests for disable_overquota_users()."""

    def test_disable_overquota(self, manager, mock_ssh):
        # Two clients: one over quota, one under
        secrets = (
            "overuser|ee161655|1783512971|true|0|0|1000000|0|\n"
            "underuser|ee161656|1783512972|true|0|0|100000000|0|\n"
        )
        traffic = "● overuser: ↓ 2 МБ  ↑ 0 КБ  соед: 1\n" "● underuser: ↓ 1 КБ  ↑ 0 КБ  соед: 0\n"
        mock_ssh.run_command.side_effect = [
            (secrets, "", 0),
            (traffic, "", 0),
            # toggle calls
            ("disabled", "", 0),
        ]

        disabled = manager.disable_overquota_users("telemt")
        assert "overuser" in disabled
        assert "underuser" not in disabled

    def test_no_overquota_users(self, manager, mock_ssh):
        secrets = "user1|ee161655|1783512971|true|0|0|0|0|\n"
        mock_ssh.run_command.side_effect = [
            (secrets, "", 0),
            ("", "", 0),
        ]
        disabled = manager.disable_overquota_users("telemt")
        assert disabled == []


# ---------------------------------------------------------------------------
# TestInstallProtocol
# ---------------------------------------------------------------------------


class TestInstallProtocol:
    """Tests for install_protocol()."""

    def test_install_already_installed(self, manager, mock_ssh):
        # _check_mtproxyl_installed returns True
        mock_ssh.run_command.return_value = ("found", "", 0)
        mock_ssh.run_sudo_command.return_value = ("", "", 0)

        result = manager.install_protocol("telemt", "443")
        assert result["status"] == "success"
        # No install script should be called
        mock_ssh.run_sudo_command.assert_not_called()

    def test_install_needs_installing(self, manager, mock_ssh):
        # _check_mtproxyl_installed: first call False, then True after install
        mock_ssh.run_command.side_effect = [
            ("not_found", "", 0),  # _check_mtproxyl_installed
            ("", "", 0),  # port command
            ("", "", 0),  # start command
        ]
        mock_ssh.run_sudo_command.return_value = ("Installed", "", 0)

        result = manager.install_protocol("telemt", "18443")
        assert result["status"] == "success"
        mock_ssh.run_sudo_command.assert_called_once()
        assert "mtproxyl-install.sh" in mock_ssh.run_sudo_command.call_args[0][0]

    def test_install_fails(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("not_found", "", 0)
        mock_ssh.run_sudo_command.return_value = ("", "install failed", 1)

        result = manager.install_protocol("telemt", "18443")
        assert result["status"] == "error"
        assert "install failed" in result["log"][-1]

    def test_install_bunkerweb_port_shift(self, manager, mock_ssh):
        mock_ssh.run_command.side_effect = [
            ("found", "", 0),  # _check_mtproxyl_installed
            ("bunkerweb", "", 0),  # _detect_bunkerweb_running
            ("", "", 0),  # port command
            ("", "", 0),  # start command
        ]
        mock_ssh.run_sudo_command.return_value = ("", "", 0)

        result = manager.install_protocol("telemt", "443")
        assert result["status"] == "success"
        assert result["port"] == "18443"
        assert any("BunkerWeb" in log for log in result["log"])

    def test_install_with_tls_domain(self, manager, mock_ssh):
        mock_ssh.run_command.side_effect = [
            ("found", "", 0),  # _check_mtproxyl_installed
            ("", "", 0),  # domain command
            ("", "", 0),  # port command
            ("", "", 0),  # start command
        ]
        mock_ssh.run_sudo_command.return_value = ("", "", 0)

        result = manager.install_protocol(
            "telemt", "18443", tls_emulation=True, tls_domain="cloud.hostup.se"
        )
        assert result["status"] == "success"
        # Verify domain command was called
        calls = mock_ssh.run_command.call_args_list
        domain_calls = [c for c in calls if "domain" in c[0][0]]
        assert len(domain_calls) >= 1


# ---------------------------------------------------------------------------
# TestRemoveContainer
# ---------------------------------------------------------------------------


class TestRemoveContainer:
    """Tests for remove_container()."""

    def test_remove_container(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("stopped", "", 0)
        manager.remove_container("telemt")
        mock_ssh.run_command.assert_called_once()
        assert "stop" in mock_ssh.run_command.call_args[0][0]


# ---------------------------------------------------------------------------
# TestDetectBunkerwebRunning
# ---------------------------------------------------------------------------


class TestDetectBunkerwebRunning:
    """Tests for _detect_bunkerweb_running()."""

    def test_bunkerweb_running(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("bunkerweb", "", 0)
        assert manager._detect_bunkerweb_running() is True

    def test_bunkerweb_not_running(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("", "", 0)
        assert manager._detect_bunkerweb_running() is False


# ---------------------------------------------------------------------------
# TestCheckMtproxylInstalled
# ---------------------------------------------------------------------------


class TestCheckMtproxylInstalled:
    """Tests for _check_mtproxyl_installed()."""

    def test_installed(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("found", "", 0)
        assert manager._check_mtproxyl_installed() is True

    def test_not_installed(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("not_found", "", 0)
        assert manager._check_mtproxyl_installed() is False


# ---------------------------------------------------------------------------
# TestFormatLimits
# ---------------------------------------------------------------------------


class TestFormatLimits:
    """Tests for _format_limits()."""

    def test_all_specified(self, manager):
        result = manager._format_limits(1073741824, 5, "0")
        assert result == "0 5 1073741824 0"

    def test_partial(self, manager):
        result = manager._format_limits(None, 3, None)
        assert result == "0 3 0 0"

    def test_none_all(self, manager):
        result = manager._format_limits(None, None, None)
        assert result == "0 0 0 0"


# ---------------------------------------------------------------------------
# TestParseTraffic
# ---------------------------------------------------------------------------


class TestParseTraffic:
    """Tests for _parse_traffic()."""

    def test_parses_traffic_lines(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = (
            "● tg_proxy: ↓ 1.96 ГБ  ↑ 96.64 ГБ  соед: 41\n"
            "● other_user: ↓ 500 МБ  ↑ 100 МБ  соед: 5\n",
            "",
            0,
        )
        traffic = manager._parse_traffic()
        assert "tg_proxy" in traffic
        assert "other_user" in traffic
        assert traffic["tg_proxy"]["connections"] == 41
        assert traffic["other_user"]["connections"] == 5
        assert traffic["tg_proxy"]["total"] > 0

    def test_empty_traffic(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("", "", 0)
        assert manager._parse_traffic() == {}

    def test_command_fails(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("", "error", 1)
        assert manager._parse_traffic() == {}

    def test_no_per_user_lines(self, manager, mock_ssh):
        mock_ssh.run_command.return_value = ("Общий трафик:\n  ↓ 10 ГБ\n", "", 0)
        assert manager._parse_traffic() == {}
