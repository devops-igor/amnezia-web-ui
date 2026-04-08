"""
Unit tests for dns_manager.py
"""

from unittest.mock import MagicMock
from dns_manager import DNSManager


class TestDNSManager:
    """Tests for DNSManager protocol management."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.dns = DNSManager(self.mock_ssh)

    def test_get_server_status_running(self):
        """Test get_server_status when container is running."""
        self.mock_ssh.run_sudo_command.side_effect = [
            ("Up 5 minutes", "", 0),  # docker ps --filter name=^amnezia-dns$
            ("amnezia-dns", "", 0),  # docker ps -a --filter name=^amnezia-dns$
        ]

        result = self.dns.get_server_status("dns")

        assert result["container_exists"] is True
        assert result["container_running"] is True
        assert result["port"] == "53"
        assert result["protocol"] == "dns"

    def test_get_server_status_not_running(self):
        """Test get_server_status when container is stopped."""
        self.mock_ssh.run_sudo_command.side_effect = [
            ("Exited (0) 10 minutes ago", "", 0),  # docker ps
            ("amnezia-dns", "", 0),  # docker ps -a
        ]

        result = self.dns.get_server_status("dns")

        assert result["container_exists"] is True
        assert result["container_running"] is False

    def test_get_server_status_not_installed(self):
        """Test get_server_status when container doesn't exist."""
        self.mock_ssh.run_sudo_command.side_effect = [
            ("", "", 0),  # docker ps
            ("", "", 0),  # docker ps -a
        ]

        result = self.dns.get_server_status("dns")

        assert result["container_exists"] is False
        assert result["container_running"] is False

    def test_get_server_status_error(self):
        """Test get_server_status handles SSH errors gracefully."""
        self.mock_ssh.run_sudo_command.side_effect = Exception("SSH error")

        result = self.dns.get_server_status("dns")

        assert "error" in result

    def test_remove_container_calls_expected_commands(self):
        """Test remove_container executes correct Docker commands."""
        calls = []

        def mock_run(cmd, *args, **kwargs):
            calls.append(cmd)
            return ("", "", 0)

        self.mock_ssh.run_sudo_command.side_effect = mock_run

        self.dns.remove_container("dns")

        assert any("docker stop amnezia-dns" in cmd for cmd in calls)
        assert any("docker rm" in cmd for cmd in calls)
        assert any("rm -rf /opt/amnezia/dns" in cmd for cmd in calls)

    def test_install_protocol_checks_docker(self):
        """Test install_protocol verifies Docker is not installed."""
        # Simulate docker NOT being installed
        self.mock_ssh.run_command.return_value = ("", "", 0)

        result = self.dns.install_protocol("dns")

        self.mock_ssh.run_command.assert_called_once()
        assert result["status"] == "error"
        assert result["message"] == "Docker not installed"
