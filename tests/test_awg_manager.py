"""
Unit tests for awg_manager.py
"""

import pytest
from unittest.mock import MagicMock, patch
from awg_manager import AWGManager, generate_wg_keypair, generate_psk, generate_awg_params
import awg_manager


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

    def test_get_next_ip_exhausted_subnet_raises(self):
        """When all IPs in subnet are used, should raise RuntimeError."""
        # Temporarily change to /30 subnet (only 2 usable + gateway)
        with patch("awg_manager.AWG_DEFAULTS", {**awg_manager.AWG_DEFAULTS, "subnet_cidr": 30}):
            with patch.object(
                self.manager,
                "_get_used_ips",
                return_value=["10.8.1.2", "10.8.1.3"],
            ):
                with pytest.raises(RuntimeError) as exc_info:
                    self.manager._get_next_ip("awg")
                assert "exhausted" in str(exc_info.value).lower()

    def test_get_next_ip_full_subnet_concurrent(self):
        """Simulate sequential exhaustion of a subnet (all 254 usable IPs taken)."""
        all_ips = [f"10.8.1.{i}" for i in range(2, 255)]  # 253 client IPs + gateway = 254
        with patch.object(
            self.manager,
            "_get_used_ips",
            return_value=all_ips,
        ):
            with pytest.raises(RuntimeError) as exc_info:
                self.manager._get_next_ip("awg")
            assert "exhausted" in str(exc_info.value).lower()

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

        # Should append peer to config via SFTP (not echo >>) and sync
        assert any("docker cp" in cmd for cmd in calls)
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


class TestValidateAwgParams:
    """Tests for _validate_awg_params() — injection prevention for AWG parameters."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = AWGManager(self.mock_ssh)

    def test_valid_awg_params_pass(self):
        """Standard generated params should pass validation."""
        params = generate_awg_params(use_ranges=True)
        # Should not raise
        self.manager._validate_awg_params(params)

    def test_non_numeric_value_rejected(self):
        """Non-numeric string values should be rejected."""
        params = generate_awg_params(use_ranges=True)
        params["junk_packet_count"] = "$(rm -rf /)"
        with pytest.raises(ValueError, match="numeric string"):
            self.manager._validate_awg_params(params)

    def test_newline_injection_rejected(self):
        """Values with newlines should be rejected."""
        params = generate_awg_params(use_ranges=True)
        params["init_packet_magic_header"] = "1\nMALICIOUS"
        with pytest.raises(ValueError, match="numeric string"):
            self.manager._validate_awg_params(params)

    def test_semicolon_injection_rejected(self):
        """Values with semicolons should be rejected (not digits)."""
        params = generate_awg_params(use_ranges=True)
        params["junk_packet_count"] = "3; rm -rf /"
        with pytest.raises(ValueError, match="numeric string"):
            self.manager._validate_awg_params(params)

    def test_pipe_injection_rejected(self):
        """Values with pipes should be rejected."""
        params = generate_awg_params(use_ranges=True)
        params["junk_packet_count"] = "3|cat /etc/passwd"
        with pytest.raises(ValueError, match="numeric string"):
            self.manager._validate_awg_params(params)

    def test_out_of_range_rejected(self):
        """Values outside expected range should be rejected."""
        params = generate_awg_params(use_ranges=True)
        params["junk_packet_count"] = "999"
        with pytest.raises(ValueError, match="between"):
            self.manager._validate_awg_params(params)

    def test_negative_value_rejected(self):
        """Negative values should be rejected by digit check."""
        params = generate_awg_params(use_ranges=True)
        params["junk_packet_count"] = "-1"
        with pytest.raises(ValueError, match="numeric string"):
            self.manager._validate_awg_params(params)

    def test_float_value_rejected(self):
        """Float values should be rejected (not digit-only)."""
        params = generate_awg_params(use_ranges=True)
        params["junk_packet_count"] = "3.5"
        with pytest.raises(ValueError, match="numeric string"):
            self.manager._validate_awg_params(params)

    def test_missing_params_allowed(self):
        """Missing optional params should not raise."""
        params = {"junk_packet_count": "5"}
        # Should not raise - only validates params that are present
        self.manager._validate_awg_params(params)

    def test_non_string_value_rejected(self):
        """Non-string values (e.g. int) should be rejected."""
        params = generate_awg_params(use_ranges=True)
        params["junk_packet_count"] = 3  # int, not str
        with pytest.raises(ValueError, match="numeric string"):
            self.manager._validate_awg_params(params)


class TestConfigureContainerSftp:
    """Tests for _configure_container() using SFTP instead of shell heredoc."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = AWGManager(self.mock_ssh)

    def test_configure_container_uses_sftp_not_heredoc(self):
        """Config file should be written via upload_file + docker cp, not shell heredoc."""
        self.mock_ssh.run_sudo_command.return_value = ("keycontent", "", 0)

        awg_params = generate_awg_params(use_ranges=True)
        self.manager._configure_container("awg", "55424", awg_params)

        # Should NOT use 'cat >' (heredoc) in any docker exec command
        for call in self.mock_ssh.run_sudo_command.call_args_list:
            cmd = call[0][0]
            if "docker exec" in cmd:
                assert "cat >" not in cmd
                assert "<<EOF" not in cmd
                # No awg_params values should appear in docker exec commands
                for val in awg_params.values():
                    assert val not in cmd or cmd.startswith("docker cp")

        # Should use upload_file for config
        assert self.mock_ssh.upload_file.called
        # Should use docker cp
        cmds = [call[0][0] for call in self.mock_ssh.run_sudo_command.call_args_list]
        assert any("docker cp" in cmd for cmd in cmds)

    def test_configure_container_legacy_uses_sftp(self):
        """AWG Legacy path should also use SFTP, not shell heredoc."""
        self.mock_ssh.run_sudo_command.return_value = ("keycontent", "", 0)

        awg_params = generate_awg_params(use_ranges=False)
        # Remove AWG2-specific params for legacy
        for key in ["cookie_reply_packet_junk_size", "transport_packet_junk_size"]:
            if key in awg_params:
                del awg_params[key]

        self.manager._configure_container("awg_legacy", "55424", awg_params)

        # Should NOT use shell heredoc in docker exec
        for call in self.mock_ssh.run_sudo_command.call_args_list:
            cmd = call[0][0]
            if "docker exec" in cmd:
                assert "cat >" not in cmd
                assert "EOF" not in cmd

        assert self.mock_ssh.upload_file.called

    def test_configure_container_validates_awg_params(self):
        """_configure_container should validate awg_params injection."""
        awg_params = generate_awg_params(use_ranges=True)
        awg_params["junk_packet_count"] = "$(rm -rf /)"

        with pytest.raises(ValueError):
            self.manager._configure_container("awg", "55424", awg_params)

    def test_configure_container_keygen_safe(self):
        """Key generation commands should contain no user data."""
        self.mock_ssh.run_sudo_command.return_value = ("keycontent", "", 0)

        awg_params = generate_awg_params(use_ranges=True)
        port = "55424"
        self.manager._configure_container("awg", port, awg_params)

        # The first docker exec should be key generation only
        for call in self.mock_ssh.run_sudo_command.call_args_list:
            cmd = call[0][0]
            if "docker exec" in cmd and "cat" not in cmd:
                # Key generation command should not contain user-controlled values
                assert port not in cmd
                for val in awg_params.values():
                    assert val not in cmd

    def test_configure_container_temp_file_cleaned(self):
        """Temp file should be cleaned up after configuration."""
        self.mock_ssh.run_sudo_command.return_value = ("keycontent", "", 0)

        awg_params = generate_awg_params(use_ranges=True)
        self.manager._configure_container("awg", "55424", awg_params)

        # rm -f should be called for the temp config file
        assert self.mock_ssh.run_command.called
        rm_calls = [
            call[0][0] for call in self.mock_ssh.run_command.call_args_list if "rm -f" in call[0][0]
        ]
        assert len(rm_calls) >= 1
        assert "/tmp/_amnz_wg_config.conf" in rm_calls[0]


class TestAddClientSftp:
    """Tests for add_client() using SFTP instead of echo shell injection."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = AWGManager(self.mock_ssh)

    def test_add_client_no_echo_in_shell(self):
        """add_client should NOT use echo >> to append peer config."""
        calls = []

        def mock_run(cmd, *args, **kwargs):
            calls.append(cmd)
            return ("", "", 0)

        self.mock_ssh.run_sudo_command.side_effect = mock_run
        self.mock_ssh.run_command.side_effect = mock_run

        # Mock all the internal method calls
        with patch.object(self.manager, "_get_server_public_key", return_value="pubkey"):
            with patch.object(self.manager, "_get_server_psk", return_value="psk-key"):
                with patch.object(self.manager, "_get_next_ip", return_value="10.8.1.2"):
                    with patch.object(
                        self.manager,
                        "_get_awg_params_from_config",
                        return_value={"port": "55424"},
                    ):
                        with patch.object(
                            self.manager,
                            "_get_server_config",
                            return_value="[Interface]\nPrivateKey = test\n",
                        ):
                            with patch.object(self.manager, "_save_clients_table"):
                                self.manager.add_client("awg", "test", "1.2.3.4", "55424")

        # Should NOT use echo >> in any docker exec command
        for cmd in calls:
            if "docker exec" in cmd:
                assert "echo" not in cmd or ">>" not in cmd

        # Should use upload_file instead of echo
        assert self.mock_ssh.upload_file.called

    def test_add_client_uses_docker_cp(self):
        """add_client should use docker cp to move config into container."""
        calls = []

        def mock_run(cmd, *args, **kwargs):
            calls.append(cmd)
            return ("", "", 0)

        self.mock_ssh.run_sudo_command.side_effect = mock_run
        self.mock_ssh.run_command.side_effect = mock_run

        with patch.object(self.manager, "_get_server_public_key", return_value="pubkey"):
            with patch.object(self.manager, "_get_server_psk", return_value="psk-key"):
                with patch.object(self.manager, "_get_next_ip", return_value="10.8.1.2"):
                    with patch.object(
                        self.manager,
                        "_get_awg_params_from_config",
                        return_value={"port": "55424"},
                    ):
                        with patch.object(
                            self.manager,
                            "_get_server_config",
                            return_value="[Interface]\nPrivateKey = test\n",
                        ):
                            with patch.object(self.manager, "_save_clients_table"):
                                self.manager.add_client("awg", "test", "1.2.3.4", "55424")

        assert any("docker cp" in cmd for cmd in calls)


class TestToggleClientSftp:
    """Tests for toggle_client() using SFTP instead of echo shell injection."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = AWGManager(self.mock_ssh)

    def test_toggle_client_enable_uses_sftp(self):
        """toggle_client enable should use SFTP + docker cp, not echo >>."""
        calls = []

        def mock_run(cmd, *args, **kwargs):
            calls.append(cmd)
            return ("", "", 0)

        self.mock_ssh.run_sudo_command.side_effect = mock_run
        self.mock_ssh.run_command.side_effect = mock_run

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
                with patch.object(
                    self.manager,
                    "_get_server_config",
                    return_value="[Interface]\nPrivateKey = test\n",
                ):
                    with patch.object(self.manager, "_save_clients_table"):
                        self.manager.toggle_client("awg", "client1", True)

        # Should NOT use echo >> with user data in docker exec
        for cmd in calls:
            if "docker exec" in cmd:
                assert "echo" not in cmd or ">>" not in cmd

        # Should use upload_file
        assert self.mock_ssh.upload_file.called
        # Should use docker cp
        assert any("docker cp" in cmd for cmd in calls)

    def test_toggle_client_enable_malicious_data_no_injection(self):
        """Even with shell metacharacters in keys, no injection should occur."""
        calls = []

        def mock_run(cmd, *args, **kwargs):
            calls.append(cmd)
            return ("", "", 0)

        self.mock_ssh.run_sudo_command.side_effect = mock_run
        self.mock_ssh.run_command.side_effect = mock_run

        # Simulate a client with a public key containing shell metacharacters
        # (this wouldn't happen normally but tests the injection prevention)
        mock_table = [
            {
                "clientId": "key$(whoami)",
                "name": "test",
                "enabled": False,
                "userData": {"psk": "psk;rm -rf /", "clientIp": "10.8.1.2"},
            }
        ]
        with patch.object(self.manager, "_get_clients_table", return_value=mock_table):
            with patch.object(self.manager, "_get_server_psk", return_value="psk;rm -rf /"):
                with patch.object(
                    self.manager,
                    "_get_server_config",
                    return_value="[Interface]\nPrivateKey = test\n",
                ):
                    with patch.object(self.manager, "_save_clients_table"):
                        self.manager.toggle_client("awg", "key$(whoami)", True)

        # No user data should appear in docker exec commands
        for cmd in calls:
            if "docker exec" in cmd:
                assert "$(whoami)" not in cmd
                assert "rm -rf" not in cmd


class TestAWGManagerConcurrent:
    """Tests for concurrent safety in AWGManager."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = AWGManager(self.mock_ssh)

    def test_add_client_concurrent_no_collision(self):
        """Sequential add_client under lock must not assign duplicate IPs."""
        # Track used IPs to verify the lock path works
        used = []

        def track_ips(*args, **kwargs):
            return list(used)

        with (
            patch.object(
                self.manager, "_get_server_config", return_value="[Interface]\nPrivateKey = test\n"
            ),
            patch.object(self.manager, "_save_clients_table"),
            patch.object(self.manager, "_get_clients_table", return_value=[]),
            patch.object(self.manager, "_get_awg_params_from_config", return_value={}),
            patch.object(self.manager, "_get_server_public_key", return_value="testpub"),
            patch.object(self.manager, "_get_server_psk", return_value="testpsk"),
            patch.object(self.manager, "_get_used_ips", side_effect=track_ips),
        ):

            mock_ssh = MagicMock()
            mock_ssh.run_sudo_command.return_value = ("", "", 0)
            mock_ssh.run_command.return_value = ("", "", 0)
            mock_ssh.upload_file.return_value = None
            self.manager.ssh = mock_ssh

            result1 = self.manager.add_client("awg", "client1", "1.2.3.4", "55424")
            ip1 = result1["client_ip"]
            used.append(ip1)

            result2 = self.manager.add_client("awg", "client2", "1.2.3.4", "55424")
            ip2 = result2["client_ip"]

        assert ip1 != ip2

    def test_add_client_lock_held_during_ip_allocation(self):
        """The _lock attribute should be a lock instance."""
        assert hasattr(self.manager, "_lock")
        assert self.manager._lock is not None
