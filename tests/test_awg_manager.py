"""
Unit tests for awg_manager.py
"""

from unittest.mock import MagicMock, patch
from awg_manager import AWGManager, generate_wg_keypair, generate_psk, generate_awg_params


class TestAWGKeyGeneration:
    """Tests for standalone key/parameter generation functions."""

    def test_generate_wg_keypair_returns_two_strings(self):
        private_key, public_key = generate_wg_keypair()
        assert isinstance(private_key, str)
        assert isinstance(public_key, str)
        assert len(private_key) > 0
        assert len(public_key) > 0

    def test_generate_wg_keypair_unique(self):
        """Each call should produce a unique keypair."""
        priv1, pub1 = generate_wg_keypair()
        priv2, pub2 = generate_wg_keypair()
        assert priv1 != priv2
        assert pub1 != pub2

    def test_generate_psk_returns_string(self):
        psk = generate_psk()
        assert isinstance(psk, str)
        assert len(psk) > 0

    def test_generate_psk_unique(self):
        psk1 = generate_psk()
        psk2 = generate_psk()
        assert psk1 != psk2

    def test_generate_awg_params_returns_dict(self):
        params = generate_awg_params(use_ranges=False)
        assert isinstance(params, dict)
        assert "junk_packet_count" in params
        assert "init_packet_magic_header" in params

    def test_generate_awg_params_with_ranges(self):
        params = generate_awg_params(use_ranges=True)
        assert isinstance(params, dict)
        assert "junk_packet_count" in params


class TestAWGManager:
    """Tests for AWGManager class."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = AWGManager(self.mock_ssh)

    # ---- Internal helpers ----

    def test_container_name_awg(self):
        assert self.manager._container_name("awg") == "amnezia-awg"

    def test_container_name_awg_legacy(self):
        assert self.manager._container_name("awg_legacy") == "amnezia-awg-legacy"

    def test_container_name_awg2(self):
        assert self.manager._container_name("awg2") == "amnezia-awg2"

    def test_config_path_awg(self):
        assert self.manager._config_path("awg") == "/opt/amnezia/awg/awg0.conf"

    def test_config_path_awg_legacy(self):
        assert self.manager._config_path("awg_legacy") == "/opt/amnezia/awg/wg0.conf"

    def test_docker_image_awg(self):
        assert self.manager._docker_image("awg") == "amneziavpn/amneziawg-go:latest"

    def test_docker_image_awg_legacy(self):
        assert self.manager._docker_image("awg_legacy") == "amneziavpn/amnezia-wg:latest"

    # ---- Docker checks ----

    def test_check_docker_installed_true(self):
        self.mock_ssh.run_command.side_effect = [
            ("Docker version 24.0.0", "", 0),
            ("active", "", 0),
        ]
        assert self.manager.check_docker_installed() is True

    def test_check_docker_installed_false_version(self):
        self.mock_ssh.run_command.return_value = ("", "", 127)
        assert self.manager.check_docker_installed() is False

    def test_check_docker_installed_false_not_active(self):
        self.mock_ssh.run_command.side_effect = [
            ("Docker version 24.0.0", "", 0),
            ("stopped", "", 0),
        ]
        # "stopped" doesn't contain "active" or "running"
        assert self.manager.check_docker_installed() is False

    def test_check_docker_installed_false_service_down(self):
        self.mock_ssh.run_command.side_effect = [
            ("Docker version 24.0.0", "", 0),
            ("", "", 1),
        ]
        assert self.manager.check_docker_installed() is False

    def test_check_container_running_true(self):
        self.mock_ssh.run_sudo_command.return_value = ("Up 5 minutes", "", 0)
        assert self.manager.check_container_running("awg") is True

    def test_check_container_running_false(self):
        self.mock_ssh.run_sudo_command.return_value = ("", "", 0)
        assert self.manager.check_container_running("awg") is False

    def test_check_protocol_installed_true(self):
        self.mock_ssh.run_sudo_command.return_value = ("amnezia-awg\n", "", 0)
        assert self.manager.check_protocol_installed("awg") is True

    def test_check_protocol_installed_false(self):
        self.mock_ssh.run_sudo_command.return_value = ("", "", 0)
        assert self.manager.check_protocol_installed("awg") is False

    # ---- Removal ----

    def test_remove_container_calls_docker_commands(self):
        calls = []

        def mock_run(cmd, *args, **kwargs):
            calls.append(cmd)
            return ("", "", 0)

        self.mock_ssh.run_sudo_command.side_effect = mock_run
        self.manager.remove_container("awg")

        assert any("docker stop amnezia-awg" in cmd for cmd in calls)
        assert any("docker rm -fv amnezia-awg" in cmd for cmd in calls)
        assert any("docker rmi amnezia-awg" in cmd for cmd in calls)

    # ---- IP allocation ----

    def test_get_next_ip_no_used_ips(self):
        """When no IPs are used, should return the first available IP."""
        self.mock_ssh.run_sudo_command.return_value = ("", "", 0)
        ip = self.manager._get_next_ip("awg")
        assert ip == "10.8.1.2"

    def test_get_next_ip_skips_used(self):
        """Should skip IPs that are already in use."""
        # _get_used_ips calls _get_server_config which calls run_sudo_command
        # then parses IPs from the config. Mock _get_used_ips directly.
        with patch.object(self.manager, "_get_used_ips", return_value=["10.8.1.2"]):
            ip = self.manager._get_next_ip("awg")
            assert ip == "10.8.1.3"

    # ---- Parse bytes ----

    def test_parse_bytes_mib(self):
        assert self.manager._parse_bytes("1.50 MiB") == int(1.5 * 1024 * 1024)

    def test_parse_bytes_kib(self):
        assert self.manager._parse_bytes("512 KiB") == 512 * 1024

    def test_parse_bytes_gib(self):
        assert self.manager._parse_bytes("1 GiB") == 1024 * 1024 * 1024

    def test_parse_bytes_invalid(self):
        assert self.manager._parse_bytes("invalid") == 0
        assert self.manager._parse_bytes("") == 0

    # ---- Clients ----

    def test_get_clients_empty(self):
        """When no clients exist, return empty list."""
        self.mock_ssh.run_sudo_command.return_value = ("", "", 0)
        clients = self.manager.get_clients("awg")
        assert clients == []

    def test_toggle_client_enable(self):
        """Test toggling a client on."""
        calls = []

        def mock_run(cmd, *args, **kwargs):
            calls.append(cmd)
            return ("", "", 0)

        self.mock_ssh.run_sudo_command.side_effect = mock_run

        # _get_clients_table returns a list of client dicts
        mock_table = [
            {
                "clientId": "client1",
                "name": "test",
                "enabled": False,
                "userData": {"psk": "testpsk", "clientIp": "10.8.1.2"},
            }
        ]
        with patch.object(self.manager, "_get_clients_table", return_value=mock_table):
            with patch.object(self.manager, "_get_server_psk", return_value="testpsk"):
                with patch.object(self.manager, "_save_clients_table"):
                    self.manager.toggle_client("awg", "client1", True)

        # Should append peer to config and sync
        assert any("echo" in cmd and ">>" in cmd for cmd in calls)
        assert any("syncconf" in cmd for cmd in calls)

    def test_toggle_client_disable(self):
        """Test toggling a client off."""
        calls = []

        def mock_run(cmd, *args, **kwargs):
            calls.append(cmd)
            return ("", "", 0)

        self.mock_ssh.run_sudo_command.side_effect = mock_run

        mock_table = [{"clientId": "client1", "name": "test", "enabled": True}]
        with patch.object(self.manager, "_get_clients_table", return_value=mock_table):
            with patch.object(self.manager, "_get_server_config", return_value="[Interface]\n"):
                with patch.object(self.manager, "_save_clients_table"):
                    self.manager.toggle_client("awg", "client1", False)

        # Should upload config and sync
        assert any("docker cp" in cmd for cmd in calls)
        assert any("syncconf" in cmd for cmd in calls)

    def test_remove_client(self):
        """Test removing a client."""
        calls = []

        def mock_run(cmd, *args, **kwargs):
            calls.append(cmd)
            return ("", "", 0)

        self.mock_ssh.run_sudo_command.side_effect = mock_run

        mock_table = [{"clientId": "client1", "name": "test"}]
        with patch.object(self.manager, "_get_clients_table", return_value=mock_table):
            with patch.object(self.manager, "_save_clients_table"):
                with patch.object(self.manager, "_wg_show", return_value=[]):
                    self.manager.remove_client("awg", "client1")

        # Should upload config and sync
        assert any("docker cp" in cmd for cmd in calls) or any("syncconf" in cmd for cmd in calls)

    # ---- Config ----

    def test_get_awg_params_from_config(self):
        """Test parsing AWG params from a config string."""
        config = """
[Interface]
PrivateKey = abc123
Address = 10.8.1.1/24
ListenPort = 55424
JunkPacketCount = 3
InitPacketMagicHeader = 1020325451
"""
        self.mock_ssh.run_sudo_command.return_value = (config, "", 0)
        params = self.manager._get_awg_params_from_config("awg")
        assert isinstance(params, dict)
        assert "junk_packet_count" in params or len(params) > 0

    def test_save_server_config(self):
        """Test saving server config via SSH."""
        self.mock_ssh.run_sudo_command.return_value = ("", "", 0)
        self.mock_ssh.upload_file.return_value = True

        self.manager.save_server_config("awg", "[Interface]\nPrivateKey = test")

        self.mock_ssh.upload_file.assert_called_once()
        assert self.mock_ssh.run_sudo_command.call_count >= 2  # docker cp + restart
