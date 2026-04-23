"""Tests for batch 3C — hardcoded value fixes.

Covers:
- #72: XRAY_VERSION constant and f-string interpolation in xray_manager.py
- #73: _detect_package_manager() and conditional apt/yum in telemt_manager.py
"""

from __future__ import annotations

import io
import re
from unittest.mock import MagicMock, mock_open, patch

from telemt_manager import TelemtManager
from xray_manager import XRAY_VERSION, XrayManager

# ----------------------------------------------------------------
# #72: XRAY_VERSION constant
# ----------------------------------------------------------------


class TestXrayVersionConstant:
    """Tests for the XRAY_VERSION module-level constant."""

    def test_xray_version_is_string(self):
        assert isinstance(XRAY_VERSION, str)

    def test_xray_version_format(self):
        """Version should be a semver-like string (e.g. '1.8.24')."""
        assert re.match(r"^\d+\.\d+\.\d+$", XRAY_VERSION)

    def test_xray_version_not_old_hardcoded(self):
        """Must not be the old hardcoded '1.8.4'."""
        assert XRAY_VERSION != "1.8.4"

    def test_xray_version_is_latest(self):
        """Should be 1.8.24 (latest stable as of April 2026)."""
        assert XRAY_VERSION == "1.8.24"


class TestXrayVersionInDockerfile:
    """Test that install_protocol uses XRAY_VERSION in the Dockerfile."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = XrayManager(self.mock_ssh)

    def test_dockerfile_url_uses_xray_version(self):
        """The Dockerfile curl URL should use XRAY_VERSION, not a hardcoded value."""
        # Make SSH default return succeed for everything
        self.mock_ssh.run_command.return_value = ("Docker version 24.0.0", "", 0)
        self.mock_ssh.run_sudo_command.return_value = ("", "", 0)
        self.mock_ssh.upload_file_sudo.return_value = None

        # Override specific calls that need non-empty output
        xray_output = "Private key: privKEY\nPublic key: pubKEY"
        openssl_output = "abcd1234"

        def sudo_side_effect(cmd, *args, **kwargs):
            if "x25519" in cmd:
                return (xray_output, "", 0)
            if "openssl rand" in cmd:
                return (openssl_output, "", 0)
            return ("", "", 0)

        self.mock_ssh.run_sudo_command.side_effect = sudo_side_effect

        uploaded_files: dict[str, str] = {}

        def capture_upload(content: str, path: str):
            uploaded_files[path] = content

        self.mock_ssh.upload_file_sudo.side_effect = capture_upload

        self.manager.install_protocol(port=443, site_name="yahoo.com")

        # Find Dockerfile in uploaded files
        dockerfile_content = None
        for path, content in uploaded_files.items():
            if path.endswith("Dockerfile"):
                dockerfile_content = content
                break

        assert dockerfile_content is not None, "Dockerfile was not uploaded"
        expected_url = (
            f"https://github.com/XTLS/Xray-core/releases/download/"
            f"v{XRAY_VERSION}/Xray-linux-64.zip"
        )
        assert expected_url in dockerfile_content
        old_url = "https://github.com/XTLS/Xray-core/releases/download/" "v1.8.4/Xray-linux-64.zip"
        assert old_url not in dockerfile_content


class TestNoBareHardcodedVersion:
    """Source file should not contain bare '1.8.4'."""

    def test_no_bare_184_in_xray_manager(self):
        with open("xray_manager.py", "r") as f:
            content = f.read()
        assert "1.8.4" not in content


# ----------------------------------------------------------------
# #73: _detect_package_manager
# ----------------------------------------------------------------


class TestDetectPackageManager:
    """Tests for TelemtManager._detect_package_manager()."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)

    def test_detects_apt_when_apt_get_exists(self):
        self.mock_ssh.run_command.return_value = ("/usr/bin/apt-get", "", 0)
        assert self.manager._detect_package_manager() == "apt"

    def test_detects_yum_when_apt_get_missing(self):
        self.mock_ssh.run_command.return_value = ("", "not found", 1)
        assert self.manager._detect_package_manager() == "yum"

    def test_detects_yum_on_nonzero_exit(self):
        """Any non-zero exit code from 'which apt-get' means yum."""
        self.mock_ssh.run_command.return_value = ("", "error", 127)
        assert self.manager._detect_package_manager() == "yum"

    def test_calls_which_apt_get(self):
        """Should run 'which apt-get' to detect package manager."""
        self.mock_ssh.run_command.return_value = ("/usr/bin/apt-get", "", 0)
        self.manager._detect_package_manager()
        self.mock_ssh.run_command.assert_called_with("which apt-get")


# Minimal config/compose/dockerfile template content for telemt install_protocol tests
_FAKE_CONFIG = """tls_emulation = true
tls_domain = ""
max_connections = 0
[general.links]
public_port = 443
hello = "default"
"""

_FAKE_COMPOSE = """services:
  telemt:
    ports:
      - "443:443"
"""

_FAKE_DOCKERFILE = "FROM alpine:3.15\nRUN echo hello\n"


class TestInstallProtocolWithPackageManager:
    """Test that install_protocol uses the correct package manager for plugin install."""

    def setup_method(self):
        self.mock_ssh = MagicMock()
        self.manager = TelemtManager(self.mock_ssh)
        self.mock_ssh.host = "1.2.3.4"

    def _run_install_with_pkg_mgr(self, pkg_mgr: str, docker_installed: bool) -> list:
        """Helper to run install_protocol with a given package manager and return sudo calls."""
        with (
            patch.object(self.manager, "_detect_package_manager", return_value=pkg_mgr),
            patch.object(self.manager, "check_docker_installed", return_value=docker_installed),
            patch.object(self.manager, "check_protocol_installed", return_value=False),
            patch("telemt_manager.verify_integrity", return_value=True),
            patch("telemt_manager.load_expected_hash", return_value="abc123"),
            patch(
                "builtins.open",
                mock_open(
                    read_data=_FAKE_CONFIG,
                ),
            ) as mock_file_open,
            patch(
                "os.path.join",
                side_effect=lambda *args: "/".join(args),
            ),
            patch("os.path.dirname", return_value="/fake"),
        ):
            # Handle the multiple open() calls for config, compose, and dockerfile
            # mock_open only supports one read_data. We need multiple file reads.
            # Let's use a custom side_effect for open instead.
            file_contents = {
                "config.toml": _FAKE_CONFIG,
                "docker-compose.yml": _FAKE_COMPOSE,
                "Dockerfile": _FAKE_DOCKERFILE,
            }

            def custom_open(path, *args, **kwargs):
                path_str = str(path)
                for key, content in file_contents.items():
                    if key in path_str:
                        return io.StringIO(content)
                return io.StringIO("")

            with patch("builtins.open", side_effect=custom_open):
                self.mock_ssh.run_command.return_value = ("", "", 0)
                self.mock_ssh.run_sudo_command.return_value = ("", "", 0)
                self.manager.install_protocol()

        return [c[0][0] for c in self.mock_ssh.run_sudo_command.call_args_list]

    def test_apt_when_docker_not_installed(self):
        """When docker not installed and OS is Debian, use apt-get for plugins."""
        sudo_calls = self._run_install_with_pkg_mgr("apt", docker_installed=False)
        apt_calls = [c for c in sudo_calls if "apt-get install" in c]
        yum_calls = [c for c in sudo_calls if "yum install" in c]
        # Both plugin install calls (inside and outside docker check) use apt-get
        assert len(apt_calls) == 2
        assert len(yum_calls) == 0

    def test_yum_when_docker_not_installed(self):
        """When docker not installed and OS is RHEL/CentOS, use yum for plugins."""
        sudo_calls = self._run_install_with_pkg_mgr("yum", docker_installed=False)
        apt_calls = [c for c in sudo_calls if "apt-get install" in c]
        yum_calls = [c for c in sudo_calls if "yum install" in c]
        assert len(yum_calls) == 2
        assert len(apt_calls) == 0

    def test_apt_when_docker_already_installed(self):
        """When docker already installed and OS is Debian, only external apt-get call."""
        sudo_calls = self._run_install_with_pkg_mgr("apt", docker_installed=True)
        apt_calls = [c for c in sudo_calls if "apt-get install" in c]
        yum_calls = [c for c in sudo_calls if "yum install" in c]
        assert len(apt_calls) == 1
        assert len(yum_calls) == 0

    def test_yum_when_docker_already_installed(self):
        """When docker already installed and OS is RHEL, only external yum call."""
        sudo_calls = self._run_install_with_pkg_mgr("yum", docker_installed=True)
        apt_calls = [c for c in sudo_calls if "apt-get install" in c]
        yum_calls = [c for c in sudo_calls if "yum install" in c]
        assert len(yum_calls) == 1
        assert len(apt_calls) == 0

    def test_detect_package_manager_called_per_branch(self):
        """_detect_package_manager should be called for both plugin install points."""
        mock_detect = MagicMock(return_value="apt")
        with (
            patch.object(self.manager, "_detect_package_manager", mock_detect),
            patch.object(self.manager, "check_docker_installed", return_value=False),
            patch.object(self.manager, "check_protocol_installed", return_value=False),
            patch("telemt_manager.verify_integrity", return_value=True),
            patch("telemt_manager.load_expected_hash", return_value="abc123"),
        ):
            file_contents = {
                "config.toml": _FAKE_CONFIG,
                "docker-compose.yml": _FAKE_COMPOSE,
                "Dockerfile": _FAKE_DOCKERFILE,
            }

            def custom_open(path, *args, **kwargs):
                path_str = str(path)
                for key, content in file_contents.items():
                    if key in path_str:
                        return io.StringIO(content)
                return io.StringIO("")

            with (
                patch("builtins.open", side_effect=custom_open),
                patch("os.path.join", side_effect=lambda *a: "/".join(a)),
                patch("os.path.dirname", return_value="/fake"),
            ):
                self.mock_ssh.run_command.return_value = ("", "", 0)
                self.mock_ssh.run_sudo_command.return_value = ("", "", 0)
                self.manager.install_protocol()

        # Called twice: once inside docker-not-installed block, once outside
        assert mock_detect.call_count == 2


class TestNoBareAptGetWithoutDetection:
    """Source file should not contain the old 'apt-get || yum' fallback pattern."""

    def test_no_apt_get_or_yum_fallback(self):
        with open("telemt_manager.py", "r") as f:
            content = f.read()
        assert "|| yum install" not in content
