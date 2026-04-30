"""Tests for GET /api/servers endpoint."""

import os
import tempfile

from database import Database
from dependencies import get_current_user
from tests.conftest import create_csrf_client
from unittest.mock import patch


class TestApiListServers:
    """Tests for GET /api/servers endpoint."""

    def setup_method(self):
        """Set up test database and user."""
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        self.db = Database(self.tmp_db_path)

        self.db.create_user(
            {
                "id": "admin-1",
                "username": "admin",
                "password_hash": "hashed",
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "limits": {},
            }
        )

        self.db.create_server(
            {
                "name": "Server Alpha",
                "host": "alpha.example.com",
                "protocols": {"awg": {"installed": True, "port": "55424"}},
            }
        )
        self.db.create_server(
            {
                "name": "Server Beta",
                "host": "beta.example.com",
                "protocols": {},
            }
        )

    def teardown_method(self):
        """Clean up temporary database."""
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    @patch("app.routers.servers.get_db")
    def test_list_servers_returns_json_list(self, mock_get_db):
        """GET /api/servers returns a JSON list of all servers."""
        import app

        mock_get_db.return_value = self.db
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            client = create_csrf_client()
            response = client.get("/api/servers")
            assert response.status_code == 200
            data = response.json()
            assert isinstance(data, list)
            assert len(data) == 2
            assert data[0]["name"] == "Server Alpha"
            assert data[1]["name"] == "Server Beta"
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.servers.get_db")
    def test_list_servers_empty(self, mock_get_db):
        """GET /api/servers returns empty list when no servers exist."""
        import app

        empty_db = Database(tempfile.NamedTemporaryFile(suffix=".db", delete=False).name)
        empty_db.create_user(
            {
                "id": "admin-1",
                "username": "admin",
                "password_hash": "hashed",
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "limits": {},
            }
        )
        mock_get_db.return_value = empty_db
        app.app.dependency_overrides[get_current_user] = lambda: empty_db.get_user("admin-1")
        try:
            client = create_csrf_client()
            response = client.get("/api/servers")
            assert response.status_code == 200
            assert response.json() == []
        finally:
            app.app.dependency_overrides.clear()
            conn = empty_db._get_conn()
            conn.close()
            os.unlink(empty_db.db_path)

    def test_list_servers_unauthenticated_returns_401(self):
        """GET /api/servers without auth returns 401."""
        client = create_csrf_client()
        response = client.get("/api/servers")
        assert response.status_code == 401

    @patch("app.routers.servers.get_db")
    def test_list_servers_includes_host_and_protocols(self, mock_get_db):
        """GET /api/servers returns server details including host and protocols."""
        import app

        mock_get_db.return_value = self.db
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            client = create_csrf_client()
            response = client.get("/api/servers")
            assert response.status_code == 200
            data = response.json()
            alpha = [s for s in data if s["name"] == "Server Alpha"][0]
            assert alpha["host"] == "alpha.example.com"
            assert "protocols" in alpha
            assert alpha["protocols"]["awg"]["installed"] is True
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.servers.get_db")
    def test_api_list_servers_strips_sensitive_fields(self, mock_get_db):
        """GET /api/servers response has no password or private_key fields."""
        import app

        srv_db = Database(tempfile.NamedTemporaryFile(suffix=".db", delete=False).name)
        srv_db.create_user(
            {
                "id": "admin-1",
                "username": "admin",
                "password_hash": "hashed",
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "limits": {},
            }
        )
        # Create servers with real credentials so we can verify they get stripped
        srv_db.create_server(
            {
                "name": "Sensitive Server",
                "host": "sens.example.com",
                "username": "root",
                "password": "supersecret",
                "private_key": "-----BEGIN RSA PRIVATE KEY-----\n...",
                "protocols": {},
            }
        )
        srv_db.create_server(
            {
                "name": "No Creds Server",
                "host": "nocreds.example.com",
                "username": "root",
                "password": "",
                "private_key": "",
                "protocols": {},
            }
        )
        mock_get_db.return_value = srv_db
        app.app.dependency_overrides[get_current_user] = lambda: srv_db.get_user("admin-1")
        try:
            client = create_csrf_client()
            response = client.get("/api/servers")
            assert response.status_code == 200
            data = response.json()
            assert len(data) == 2
            for server in data:
                assert (
                    "password" not in server
                ), f"Server '{server.get('name')}' leaked password field"
                assert (
                    "private_key" not in server
                ), f"Server '{server.get('name')}' leaked private_key field"
            # Verify non-sensitive fields are still present
            assert data[0]["name"] == "Sensitive Server"
            assert data[0]["host"] == "sens.example.com"
            assert data[0]["username"] == "root"
        finally:
            app.app.dependency_overrides.clear()
            conn = srv_db._get_conn()
            conn.close()
            os.unlink(srv_db.db_path)
