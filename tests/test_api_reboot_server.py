"""Tests for POST /api/servers/{server_id}/reboot endpoint."""

import os
import tempfile
from unittest.mock import MagicMock, patch

import paramiko

from app.utils.helpers import hash_password
from database import Database
from dependencies import get_current_user
from tests.conftest import create_csrf_client

TEST_SECRET_KEY = "test-reboot-s...tes!"


class TestApiRebootServer:
    """Tests for POST /api/servers/{server_id}/reboot endpoint."""

    def setup_method(self):
        """Set up temporary database with admin user and a server."""
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        os.environ["SECRET_KEY"] = TEST_SECRET_KEY
        self.db = Database(self.tmp_db_path, secret_key=TEST_SECRET_KEY)

        self.db.create_user(
            {
                "id": "admin-1",
                "username": "admin",
                "password_hash": hash_password("AdminPass123"),
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "role": "admin",
                "limits": {},
            }
        )
        self.db.create_server(
            {
                "name": "Test Server",
                "host": "10.0.0.1",
                "username": "root",
                "password": "***",
                "ssh_port": 22,
                "protocols": {},
            }
        )

    def teardown_method(self):
        """Clean up temporary database."""
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_reboot_disconnect_failure_returns_success(self, mock_servers_db, mock_auth_db):
        """A disconnect failure after reboot still returns success.

        After sending the reboot command, the server may drop the SSH
        connection before we can cleanly disconnect.  The endpoint should
        catch the resulting SSHException/OSError, log at DEBUG level,
        and still return {'status': 'success'}.
        """
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        # Build a mock SSH manager whose disconnect raises paramiko.SSHException
        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.run_sudo_command.return_value = ("", "", 0)
        mock_ssh.disconnect.side_effect = paramiko.SSHException("Socket is closed")

        import app

        client = create_csrf_client()

        # Login
        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        assert login_resp.status_code == 200
        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                server_id = self.db.get_all_servers()[0]["id"]
                resp = client.post(f"/api/servers/{server_id}/reboot")
                assert resp.status_code == 200
                data = resp.json()
                assert data == {"status": "success"}
                # Verify disconnect was called (the reboot handler always tries)
                mock_ssh.disconnect.assert_called_once()
        finally:
            app.app.dependency_overrides.clear()
