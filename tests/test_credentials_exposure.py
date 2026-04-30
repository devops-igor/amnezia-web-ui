"""Tests for credential exposure prevention in API responses.

Covers:
- GET /api/servers must not expose password field in JSON response
- GET /api/servers must not expose private_key field in JSON response
- The Database layer (get_server_by_id) still returns credentials internally

This verifies the #114 fix — credential stripping happens at the API
boundary in the route handler, not at the database level.
"""

import os
import tempfile

from tests.conftest import create_csrf_client
from database import Database
from dependencies import get_current_user

TEST_SECRET_KEY = "test-secret-key-for-csrf-tests-16bytes!"


class TestCredentialsExposure:
    """Tests that sensitive credential fields are stripped from API responses."""

    def setup_method(self):
        """Set up temporary database with an admin user and a server with credentials."""
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        os.environ["SECRET_KEY"] = TEST_SECRET_KEY
        self.db = Database(self.tmp_db_path, secret_key=TEST_SECRET_KEY)

        self.db.create_user(
            {
                "id": "admin-1",
                "username": "admin",
                "password_hash": "$2b$12$fakehashedpasswordfor TestingPurposesOnly",
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "role": "admin",
                "limits": {},
            }
        )

        # Create a server WITH credentials — they must be stored in DB
        # but stripped from API responses
        self.db.create_server(
            {
                "name": "Secured Server",
                "host": "secure.example.com",
                "protocols": {},
                "password": "supersecretpassword",
                "private_key": "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAK...",
            }
        )

    def teardown_method(self):
        """Clean up temporary database."""
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    def test_api_list_servers_no_password(self):
        """GET /api/servers response must NOT contain the 'password' field.

        The #114 fix strips password at the API layer. Even if the DB
        stores it, the HTTP response must never expose it.
        """
        import app
        from unittest.mock import patch

        with patch("app.routers.servers.get_db", return_value=self.db):
            app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
            try:
                client = create_csrf_client()
                response = client.get("/api/servers")
                assert response.status_code == 200

                servers = response.json()
                assert len(servers) >= 1

                for server in servers:
                    assert (
                        "password" not in server
                    ), f"'password' field leaked in API response for server '{server['name']}'"
            finally:
                app.app.dependency_overrides.clear()

    def test_api_list_servers_no_private_key(self):
        """GET /api/servers response must NOT contain the 'private_key' field.

        Private keys are sensitive — they must never appear in the JSON
        returned to any client, even authenticated admins.
        """
        import app
        from unittest.mock import patch

        with patch("app.routers.servers.get_db", return_value=self.db):
            app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
            try:
                client = create_csrf_client()
                response = client.get("/api/servers")
                assert response.status_code == 200

                servers = response.json()
                assert len(servers) >= 1

                for server in servers:
                    assert (
                        "private_key" not in server
                    ), f"'private_key' field leaked in API response for server '{server['name']}'"
            finally:
                app.app.dependency_overrides.clear()

    def test_server_dict_still_has_credentials_internally(self):
        """Database.get_server_by_id() still returns password and private_key.

        This verifies that the stripping is done at the API boundary only,
        not in the database layer. SSHManager needs these credentials
        to establish connections — stripping them at the DB would break
        all server operations.
        """
        # Get the actual server ID (SQLite AUTOINCREMENT starts from 1)
        all_servers = self.db.get_all_servers()
        assert len(all_servers) >= 1, "No servers found in database"
        server_id = all_servers[0]["id"]

        server = self.db.get_server_by_id(server_id)
        assert server is not None, f"Test server not found in database (id={server_id})"
        assert (
            server.get("password") == "supersecretpassword"
        ), "Database must still return password for internal use (SSHManager)"
        assert (
            server.get("private_key") == "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAK..."
        ), "Database must still return private_key for internal use (SSHManager)"
