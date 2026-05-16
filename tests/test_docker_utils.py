"""Unit tests for docker_utils.py — detect_package_manager and ensure_apparmor_utils."""

from unittest.mock import MagicMock, patch

from docker_utils import detect_package_manager, ensure_apparmor_utils


class TestDetectPackageManager:
    """Tests for _detect_package_manager()."""

    def test_detect_package_manager_apt(self):
        """detect_package_manager returns 'apt' when apt-get is available."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.return_value = ("", "", 0)  # which apt-get succeeds

        result = detect_package_manager(mock_ssh)

        assert result == "apt"
        mock_ssh.run_command.assert_called_once_with("which apt-get")

    def test_detect_package_manager_yum(self):
        """detect_package_manager returns 'yum' when yum is available and apt-get is not."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.side_effect = [
            ("", "", 1),  # which apt-get fails
            ("", "", 0),  # which yum succeeds
        ]

        result = detect_package_manager(mock_ssh)

        assert result == "yum"
        assert mock_ssh.run_command.call_count == 2

    def test_detect_package_manager_dnf(self):
        """detect_package_manager returns 'dnf' when only dnf is available."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.side_effect = [
            ("", "", 1),  # which apt-get fails
            ("", "", 1),  # which yum fails
            ("", "", 0),  # which dnf succeeds
        ]

        result = detect_package_manager(mock_ssh)

        assert result == "dnf"
        assert mock_ssh.run_command.call_count == 3

    def test_detect_package_manager_unknown(self):
        """detect_package_manager returns 'unknown' when no package manager found."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.return_value = ("", "", 1)  # All fail

        result = detect_package_manager(mock_ssh)

        assert result == "unknown"
        assert mock_ssh.run_command.call_count == 3


class TestEnsureApparmorUtils:
    """Tests for ensure_apparmor_utils()."""

    def test_already_installed(self):
        """No action when apparmor_parser is already present."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.return_value = ("", "", 0)  # which apparmor_parser succeeds

        ensure_apparmor_utils(mock_ssh)

        # Only one call: which apparmor_parser
        assert mock_ssh.run_command.call_count == 1
        mock_ssh.run_sudo_command.assert_not_called()

    def test_not_needed_kernel_apparmor_disabled(self):
        """No action when kernel AppArmor is disabled."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.side_effect = [
            ("", "", 1),  # which apparmor_parser fails
            ("N", "", 0),  # /sys/module/apparmor/parameters/enabled returns "N"
        ]

        ensure_apparmor_utils(mock_ssh)

        assert mock_ssh.run_command.call_count == 2
        mock_ssh.run_sudo_command.assert_not_called()

    def test_not_needed_kernel_module_missing(self):
        """No action when /sys/module/apparmor doesn't exist (not enabled)."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.side_effect = [
            ("", "", 1),  # which apparmor_parser fails
            ("", "", 0),  # cat /sys/module/apparmor/... returns empty stdout
        ]

        ensure_apparmor_utils(mock_ssh)

        assert mock_ssh.run_command.call_count == 2
        mock_ssh.run_sudo_command.assert_not_called()

    def test_needed_apt(self):
        """Installs apparmor via apt-get on apt-based systems."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.side_effect = [
            ("", "", 1),  # which apparmor_parser fails
            ("Y", "", 0),  # kernel AppArmor enabled
            ("", "", 0),  # which apt-get succeeds — detect_package_manager
        ]

        ensure_apparmor_utils(mock_ssh)

        mock_ssh.run_sudo_command.assert_called_once_with(
            "apt-get update -qq && apt-get install -y -qq apparmor"
        )

    def test_needed_yum(self):
        """Installs apparmor via yum on yum-based systems."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.side_effect = [
            ("", "", 1),  # which apparmor_parser fails
            ("Y", "", 0),  # kernel AppArmor enabled
            ("", "", 1),  # which apt-get fails — detect_package_manager
            ("", "", 0),  # which yum succeeds
        ]

        ensure_apparmor_utils(mock_ssh)

        mock_ssh.run_sudo_command.assert_called_once_with("yum install -y apparmor")

    def test_needed_dnf(self):
        """Installs apparmor via dnf on dnf-based systems."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.side_effect = [
            ("", "", 1),  # which apparmor_parser fails
            ("Y", "", 0),  # kernel AppArmor enabled
            ("", "", 1),  # which apt-get fails — detect_package_manager
            ("", "", 1),  # which yum fails
            ("", "", 0),  # which dnf succeeds
        ]

        ensure_apparmor_utils(mock_ssh)

        mock_ssh.run_sudo_command.assert_called_once_with("dnf install -y apparmor")

    def test_needed_unknown_pkg_mgr(self):
        """Logs warning and does NOT install when package manager is unknown."""
        mock_ssh = MagicMock()
        mock_ssh.run_command.side_effect = [
            ("", "", 1),  # which apparmor_parser fails
            ("Y", "", 0),  # kernel AppArmor enabled
            ("", "", 1),  # which apt-get fails — detect_package_manager
            ("", "", 1),  # which yum fails
            ("", "", 1),  # which dnf fails
        ]

        with patch("docker_utils.logger") as mock_logger:
            ensure_apparmor_utils(mock_ssh)

        mock_ssh.run_sudo_command.assert_not_called()
        mock_logger.warning.assert_called_once()
        warning_msg = mock_logger.warning.call_args[0][0]
        assert "Unsupported package manager" in warning_msg
        assert "unknown" in warning_msg
