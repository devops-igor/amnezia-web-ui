"""Tests for batch 3C — hardcoded value fixes.

Covers:
- #72: XRAY_VERSION constant and f-string interpolation in xray_manager.py
"""

from __future__ import annotations

import re
from unittest.mock import MagicMock

from app.managers import XRAY_VERSION, XrayManager

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
        with open("app/managers/xray_manager.py", "r") as f:
            content = f.read()
        assert "1.8.4" not in content
