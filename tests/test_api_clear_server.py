"""Tests for POST /api/servers/{server_id}/clear endpoint."""

import os
import tempfile
from unittest.mock import MagicMock, patch

import paramiko

from app.utils.helpers import hash_password
from database import Database
from dependencies import get_current_user
from tests.conftest import create_csrf_client

TEST_SECRET_KEY = "test-clear-server-secret-key-12345"


class TestApiClearServer:
    """Tests for POST /api/servers/{server_id}/clear endpoint."""

    def setup_method(self):
        """Set up temporary database with admin user, server, and connections."""
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
        self.db.create_user(
            {
                "id": "user-1",
                "username": "testuser",
                "password_hash": hash_password("UserPass123"),
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
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
                "protocols": {"awg": {"installed": True, "port": "55424"}},
            }
        )
        self.server_id = self.db.get_all_servers()[0]["id"]

        # Create connections for the server
        self.db.create_connection(
            {
                "id": "conn-1",
                "user_id": "user-1",
                "server_id": self.server_id,
                "protocol": "awg",
                "client_id": "client-1",
                "name": "Connection 1",
                "created_at": "2024-01-01T00:00:00",
            }
        )
        self.db.create_connection(
            {
                "id": "conn-2",
                "user_id": "user-1",
                "server_id": self.server_id,
                "protocol": "xray",
                "client_id": "client-2",
                "name": "Connection 2",
                "created_at": "2024-01-02T00:00:00",
            }
        )

    def teardown_method(self):
        """Clean up temporary database."""
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_clear_server_deletes_all_connections(self, mock_servers_db, mock_auth_db):
        """Clearing a server removes all user_connections for that server."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        import app

        client = create_csrf_client()
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.run_sudo_command.return_value = ("", "", 0)
        mock_ssh.disconnect.return_value = None

        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                before = self.db.get_all_connections()
                assert len(before) == 2

                resp = client.post(f"/api/servers/{self.server_id}/clear")

                assert resp.status_code == 200
                assert resp.json() == {"status": "success"}

                after = self.db.get_all_connections()
                assert len(after) == 0

                # Protocols should be cleared after connections are deleted
                server = self.db.get_server_by_id(self.server_id)
                assert server["protocols"] == {}

                # SSH commands were issued before DB cleanup
                mock_ssh.connect.assert_called_once()
                mock_ssh.disconnect.assert_called_once()
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_clear_server_nonexistent_returns_404(self, mock_servers_db, mock_auth_db):
        """Clearing a non-existent server returns 404 and affects no connections."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        import app

        client = create_csrf_client()
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")

        try:
            resp = client.post("/api/servers/999999/clear")

            assert resp.status_code == 404
            assert resp.json()["error"] == "Server not found"

            # Existing connections should remain untouched
            connections = self.db.get_all_connections()
            assert len(connections) == 2
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_clear_server_no_connections_works(self, mock_servers_db, mock_auth_db):
        """Clearing a server with no connections succeeds cleanly."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        # Remove all connections first
        for conn in self.db.get_all_connections():
            self.db.delete_connection(conn["id"])
        import app

        client = create_csrf_client()
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.run_sudo_command.return_value = ("", "", 0)
        mock_ssh.disconnect.return_value = None

        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                resp = client.post(f"/api/servers/{self.server_id}/clear")

                assert resp.status_code == 200
                assert resp.json() == {"status": "success"}

                server = self.db.get_server_by_id(self.server_id)
                assert server["protocols"] == {}
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_clear_server_ssh_failure_keeps_connections(self, mock_servers_db, mock_auth_db):
        """If SSH connection fails, connections must NOT be deleted."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        import app

        client = create_csrf_client()
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")

        mock_ssh = MagicMock()
        # Simulate failure during the SSH operation phase, before DB cleanup
        mock_ssh.connect.side_effect = paramiko.SSHException("Connection refused")

        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                resp = client.post(f"/api/servers/{self.server_id}/clear")

                assert resp.status_code == 500
                assert "error" in resp.json()

                # Connections must remain because SSH failed before cleanup
                connections = self.db.get_all_connections()
                assert len(connections) == 2

                server = self.db.get_server_by_id(self.server_id)
                assert server["protocols"] != {}
        finally:
            app.app.dependency_overrides.clear()
