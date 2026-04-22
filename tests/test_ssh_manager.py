"""
Unit tests for ssh_manager.py — focused on host key verification logic.
"""

from unittest.mock import MagicMock, patch
import paramiko
import pytest

from ssh_manager import SSHManager, SSHHostKeyError


class TestSSHManagerConnect:
    """Tests for SSHManager.connect() host key verification."""

    def setup_method(self):
        """Set up test fixtures."""
        self.host = "192.168.1.100"
        self.port = 22
        self.username = "testuser"
        self.password = "testpass"
        self.mock_db = MagicMock()
        self.server_id = 1

    @patch("ssh_manager.paramiko.SSHClient")
    def test_connect_first_time_stores_fingerprint(self, mock_ssh_class):
        """First connection with no known fingerprint uses AutoAddPolicy and stores."""
        mock_client = MagicMock()
        mock_ssh_class.return_value = mock_client

        # Simulate: no known fingerprint in DB
        self.mock_db.get_known_host_fingerprint.return_value = None

        # Simulate host key returned after connect
        mock_host_key = MagicMock()
        mock_host_key.get_fingerprint.return_value = b"\x01" * 32  # 256-bit key
        mock_transport = MagicMock()
        mock_transport.get_remote_server_key.return_value = mock_host_key
        mock_client.get_transport.return_value = mock_transport

        mgr = SSHManager(
            host=self.host,
            port=self.port,
            username=self.username,
            password=self.password,
            database=self.mock_db,
            server_id=self.server_id,
        )
        result = mgr.connect()

        assert result is True
        # Should have called set_missing_host_key_policy with AutoAddPolicy
        policy_calls = mock_client.set_missing_host_key_policy.call_args_list
        assert any(
            isinstance(c[0][0], paramiko.AutoAddPolicy) for c in policy_calls
        ), f"Expected AutoAddPolicy in {policy_calls}"
        # Should have stored the fingerprint
        self.mock_db.save_known_host_fingerprint.assert_called_once()
        # Should have called connect with valid paramiko kwargs only
        connect_kwargs = mock_client.connect.call_args[1]
        assert "host_key_verify" not in connect_kwargs
        assert "progress_handler" not in connect_kwargs
        assert "disabled_algorithms" not in connect_kwargs
        # Should end with RejectPolicy
        final_policy_call = mock_client.set_missing_host_key_policy.call_args_list[-1]
        assert isinstance(final_policy_call[0][0], paramiko.RejectPolicy)

    @patch("ssh_manager.paramiko.SSHClient")
    def test_connect_subsequent_matching_fingerprint(self, mock_ssh_class):
        """Subsequent connection with matching fingerprint uses RejectPolicy."""
        mock_client = MagicMock()
        mock_ssh_class.return_value = mock_client

        stored_fingerprint = "01" * 32  # hex string matching b"\x01" * 32
        self.mock_db.get_known_host_fingerprint.return_value = stored_fingerprint

        # Simulate host key returned after connect
        mock_host_key = MagicMock()
        mock_host_key.get_fingerprint.return_value = b"\x01" * 32
        mock_transport = MagicMock()
        mock_transport.get_remote_server_key.return_value = mock_host_key
        mock_client.get_transport.return_value = mock_transport

        mgr = SSHManager(
            host=self.host,
            port=self.port,
            username=self.username,
            password=self.password,
            database=self.mock_db,
            server_id=self.server_id,
        )
        result = mgr.connect()

        assert result is True
        # Should NOT have used AutoAddPolicy — RejectPolicy stays
        policy_calls = mock_client.set_missing_host_key_policy.call_args_list
        assert all(
            not isinstance(c[0][0], paramiko.AutoAddPolicy) for c in policy_calls
        ), f"AutoAddPolicy should not be used when fingerprint is known: {policy_calls}"
        # Should NOT have saved fingerprint (already known)
        self.mock_db.save_known_host_fingerprint.assert_not_called()
        # Should have called connect
        mock_client.connect.assert_called_once()

    @patch("ssh_manager.paramiko.SSHClient")
    def test_connect_fingerprint_mismatch_raises_error(self, mock_ssh_class):
        """Connection with mismatched fingerprint raises SSHHostKeyError."""
        mock_client = MagicMock()
        mock_ssh_class.return_value = mock_client

        # Stored fingerprint differs from actual
        stored_fingerprint = "aa" * 32
        self.mock_db.get_known_host_fingerprint.return_value = stored_fingerprint

        # Actual fingerprint is different
        mock_host_key = MagicMock()
        mock_host_key.get_fingerprint.return_value = b"\xbb" * 32
        mock_transport = MagicMock()
        mock_transport.get_remote_server_key.return_value = mock_host_key
        mock_client.get_transport.return_value = mock_transport

        mgr = SSHManager(
            host=self.host,
            port=self.port,
            username=self.username,
            password=self.password,
            database=self.mock_db,
            server_id=self.server_id,
        )

        with pytest.raises(SSHHostKeyError) as exc_info:
            mgr.connect()

        assert "Host key mismatch" in str(exc_info.value)
        assert self.host in str(exc_info.value)
        # Should have closed the connection on mismatch
        mock_client.close.assert_called_once()

    @patch("ssh_manager.paramiko.SSHClient")
    def test_connect_no_database(self, mock_ssh_class):
        """Connect without database — uses RejectPolicy, no fingerprint logic."""
        mock_client = MagicMock()
        mock_ssh_class.return_value = mock_client

        mock_host_key = MagicMock()
        mock_host_key.get_fingerprint.return_value = b"\x01" * 32
        mock_transport = MagicMock()
        mock_transport.get_remote_server_key.return_value = mock_host_key
        mock_client.get_transport.return_value = mock_transport

        mgr = SSHManager(
            host=self.host,
            port=self.port,
            username=self.username,
            password=self.password,
            database=None,
            server_id=None,
        )
        result = mgr.connect()

        assert result is True
        # No database calls should have been made
        self.mock_db.get_known_host_fingerprint.assert_not_called()
        self.mock_db.save_known_host_fingerprint.assert_not_called()
        # Should still connect
        mock_client.connect.assert_called_once()

    @patch("ssh_manager.paramiko.SSHClient")
    def test_connect_uses_autoadd_then_restores_reject(self, mock_ssh_class):
        """First connect sets AutoAddPolicy, then restores RejectPolicy after."""
        mock_client = MagicMock()
        mock_ssh_class.return_value = mock_client

        self.mock_db.get_known_host_fingerprint.return_value = None

        mock_host_key = MagicMock()
        mock_host_key.get_fingerprint.return_value = b"\xab" * 32
        mock_transport = MagicMock()
        mock_transport.get_remote_server_key.return_value = mock_host_key
        mock_client.get_transport.return_value = mock_transport

        mgr = SSHManager(
            host=self.host,
            port=self.port,
            username=self.username,
            password=self.password,
            database=self.mock_db,
            server_id=self.server_id,
        )
        mgr.connect()

        # Verify policy sequence:
        # [0] RejectPolicy (from connect() line 46) -> [1] AutoAddPolicy (first
        #     connect) -> [2] RejectPolicy (restored at end)
        policy_calls = mock_client.set_missing_host_key_policy.call_args_list
        assert len(policy_calls) == 3, f"Expected 3 policy calls, got: {policy_calls}"
        assert isinstance(policy_calls[0][0][0], paramiko.RejectPolicy)
        assert isinstance(policy_calls[1][0][0], paramiko.AutoAddPolicy)
        assert isinstance(policy_calls[2][0][0], paramiko.RejectPolicy)

    @patch("ssh_manager.paramiko.SSHClient")
    def test_connect_stores_hex_fingerprint(self, mock_ssh_class):
        """Fingerprint stored as hex string (not raw bytes)."""
        mock_client = MagicMock()
        mock_ssh_class.return_value = mock_client

        self.mock_db.get_known_host_fingerprint.return_value = None

        raw_fingerprint = b"\xde\xad\xbe\xef" + b"\x00" * 28
        mock_host_key = MagicMock()
        mock_host_key.get_fingerprint.return_value = raw_fingerprint
        mock_transport = MagicMock()
        mock_transport.get_remote_server_key.return_value = mock_host_key
        mock_client.get_transport.return_value = mock_transport

        mgr = SSHManager(
            host=self.host,
            port=self.port,
            username=self.username,
            password=self.password,
            database=self.mock_db,
            server_id=self.server_id,
        )
        mgr.connect()

        # Verify save_known_host_fingerprint was called with hex string
        saved_fp = self.mock_db.save_known_host_fingerprint.call_args[0][1]
        assert isinstance(saved_fp, str), f"Expected str, got {type(saved_fp)}"
        assert saved_fp == raw_fingerprint.hex()

    @patch("ssh_manager.paramiko.SSHClient")
    def test_connect_no_invalid_kwargs(self, mock_ssh_class):
        """Verify connect() only passes valid paramiko kwargs."""
        mock_client = MagicMock()
        mock_ssh_class.return_value = mock_client

        self.mock_db.get_known_host_fingerprint.return_value = None

        mock_host_key = MagicMock()
        mock_host_key.get_fingerprint.return_value = b"\x01" * 32
        mock_transport = MagicMock()
        mock_transport.get_remote_server_key.return_value = mock_host_key
        mock_client.get_transport.return_value = mock_transport

        mgr = SSHManager(
            host=self.host,
            port=self.port,
            username=self.username,
            password=self.password,
            database=self.mock_db,
            server_id=self.server_id,
        )
        mgr.connect()

        # Verify no invalid kwargs were passed
        connect_call = mock_client.connect.call_args
        kwargs = connect_call[1] if connect_call[1] else {}
        invalid_params = {"host_key_verify", "progress_handler"}
        for param in invalid_params:
            assert param not in kwargs, f"Invalid param '{param}' found in connect() call"


class TestSSHManagerConnectAuth:
    """Tests for connect() auth method selection."""

    @patch("ssh_manager.paramiko.SSHClient")
    def test_connect_with_password(self, mock_ssh_class):
        """Password auth: password passed to connect()."""
        mock_client = MagicMock()
        mock_ssh_class.return_value = mock_client

        mock_host_key = MagicMock()
        mock_host_key.get_fingerprint.return_value = b"\x01" * 32
        mock_transport = MagicMock()
        mock_transport.get_remote_server_key.return_value = mock_host_key
        mock_client.get_transport.return_value = mock_transport

        mgr = SSHManager(host="host", port=22, username="user", password="pass")
        mgr.connect()

        connect_kwargs = mock_client.connect.call_args[1]
        assert connect_kwargs["password"] == "pass"

    @patch("ssh_manager.paramiko.SSHClient")
    def test_connect_with_private_key(self, mock_ssh_class):
        """Key auth: pkey passed to connect()."""
        mock_client = MagicMock()
        mock_ssh_class.return_value = mock_client

        mock_host_key = MagicMock()
        mock_host_key.get_fingerprint.return_value = b"\x01" * 32
        mock_transport = MagicMock()
        mock_transport.get_remote_server_key.return_value = mock_host_key
        mock_client.get_transport.return_value = mock_transport

        # Patch RSAKey to avoid real key parsing
        with patch("ssh_manager.paramiko.RSAKey") as mock_rsa:
            mock_rsa.from_private_key.return_value = MagicMock()
            mgr = SSHManager(
                host="host",
                port=22,
                username="user",
                private_key="fake-key-data",
            )
            mgr.connect()

            connect_kwargs = mock_client.connect.call_args[1]
            assert "pkey" in connect_kwargs
            assert "password" not in connect_kwargs


class TestSSHManagerRunCommand:
    """Tests for run_command and run_sudo_command."""

    def setup_method(self):
        self.mock_client = MagicMock()
        self.mock_db = MagicMock()
        self.mgr = SSHManager(
            host="host",
            port=22,
            username="user",
            password="pass",
            database=self.mock_db,
            server_id=1,
        )
        self.mgr.client = self.mock_client

    def test_run_command_returns_output(self):
        """run_command returns stdout, stderr, exit_code."""
        mock_stdout = MagicMock()
        mock_stderr = MagicMock()
        mock_stdout.read.return_value = b"hello\n"
        mock_stderr.read.return_value = b""
        mock_stdout.channel.recv_exit_status.return_value = 0
        self.mock_client.exec_command.return_value = (MagicMock(), mock_stdout, mock_stderr)

        result = self.mgr.run_command("echo hello")
        assert result == ("hello", "", 0)

    def test_run_command_not_connected(self):
        """run_command raises ConnectionError when not connected."""
        self.mgr.client = None
        with pytest.raises(ConnectionError):
            self.mgr.run_command("echo hello")

    def test_run_sudo_command_as_root(self):
        """run_sudo_command delegates to run_command when user is root."""
        mgr = SSHManager(host="h", port=22, username="root", password=None)
        mgr.client = MagicMock()

        # Mock run_command since we'll call it directly
        mock_stdout = MagicMock()
        mock_stderr = MagicMock()
        mock_stdout.read.return_value = b"ok\n"
        mock_stderr.read.return_value = b""
        mock_stdout.channel.recv_exit_status.return_value = 0
        mgr.client.exec_command.return_value = (MagicMock(), mock_stdout, mock_stderr)

        result = mgr.run_sudo_command("apt update")
        # When root, should call run_command without "sudo" prefix
        call_args = mgr.client.exec_command.call_args[0][0]
        assert "sudo" not in call_args


class TestSSHHostKeyError:
    """Tests for SSHHostKeyError exception."""

    def test_exception_is_exception(self):
        """SSHHostKeyError is a proper exception subclass."""
        assert issubclass(SSHHostKeyError, Exception)

    def test_exception_message(self):
        """SSHHostKeyError preserves error message."""
        msg = "Host key mismatch for server"
        err = SSHHostKeyError(msg)
        assert str(err) == msg
