"""Tests for authentication bypass and permission escalation prevention.

Covers:
- Unauthenticated users cannot access API endpoints (401)
- Regular users (non-admin) cannot access admin-only endpoints (403)

These tests verify that the FastAPI dependency chain correctly enforces
authentication and authorization boundaries at every protected endpoint.
"""

import os
import tempfile

from fastapi.testclient import TestClient

from database import Database
from dependencies import get_current_user

TEST_SECRET_KEY = "test-secret-key-for-csrf-tests-16bytes!"


class TestAuthBypass:
    """Tests for auth bypass prevention — unauthenticated and non-admin access."""

    def setup_method(self):
        """Set up temporary database with admin and regular users, plus a test server."""
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        os.environ["SECRET_KEY"] = TEST_SECRET_KEY
        self.db = Database(self.tmp_db_path, secret_key=TEST_SECRET_KEY)

        # Admin user (for testing that endpoints work when properly authenticated)
        self.db.create_user(
            {
                "id": "admin-1",
                "username": "admin",
                "password_hash": "hashed",
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "role": "admin",
                "limits": {},
            }
        )

        # Regular user with no admin privileges
        self.db.create_user(
            {
                "id": "regular-1",
                "username": "regular",
                "password_hash": "hashed",
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "role": "user",
                "limits": {},
            }
        )

        # Test server for server-related bypass tests
        self.db.create_server(
            {
                "name": "Test Server",
                "host": "test.example.com",
                "protocols": {},
            }
        )

    def teardown_method(self):
        """Clean up temporary database."""
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    # ------------------------------------------------------------------
    # Unauthenticated tests — no session → 401
    # ------------------------------------------------------------------

    def test_unauthenticated_cannot_access_api_servers(self):
        """GET /api/servers without authentication must return 401.

        Verifies that get_current_user dependency rejects requests
        with no session cookie at the server listing endpoint.
        """
        import app

        client = TestClient(app.app)
        response = client.get("/api/servers")
        assert (
            response.status_code == 401
        ), f"Expected 401 for unauthenticated GET /api/servers, got {response.status_code}"

    def test_unauthenticated_cannot_access_api_users(self):
        """GET /api/users without authentication must return 401.

        Verifies that require_admin → get_current_user chain rejects
        unauthenticated requests at the user listing endpoint.
        """
        import app

        client = TestClient(app.app)
        response = client.get("/api/users")
        assert (
            response.status_code == 401
        ), f"Expected 401 for unauthenticated GET /api/users, got {response.status_code}"

    def test_unauthenticated_cannot_create_user(self):
        """POST /api/users/add without authentication must return 401.

        Verifies that dependency chain rejects unauthenticated mutation requests.
        The 401 is raised by get_current_user before body parsing.
        """
        import app

        client = TestClient(app.app)
        response = client.post("/api/users/add", json={"username": "attacker"})
        assert (
            response.status_code == 401
        ), f"Expected 401 for unauthenticated POST /api/users/add, got {response.status_code}"

    # ------------------------------------------------------------------
    # Regular-user-trying-admin-endpoint tests — authenticated but not admin → 403
    # ------------------------------------------------------------------

    def test_regular_user_cannot_add_server(self):
        """Non-admin user POST /api/servers/add must return 403.

        Verifies that require_admin dependency blocks users with role='user'
        from creating new servers.
        """
        import app

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("regular-1")
        try:
            client = TestClient(app.app)
            response = client.post(
                "/api/servers/add",
                json={
                    "host": "10.0.0.1",
                    "username": "root",
                    "password": "test",
                    "ssh_port": 22,
                    "name": "evil-server",
                },
            )
            assert (
                response.status_code == 403
            ), f"Expected 403 for non-admin POST /api/servers/add, got {response.status_code}"
        finally:
            app.app.dependency_overrides.clear()

    def test_regular_user_cannot_delete_user(self):
        """Non-admin user POST /api/users/{id}/delete must return 403.

        Verifies that require_admin blocks non-admin users from deleting
        other users — a dangerous privileged operation.
        """
        import app

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("regular-1")
        try:
            client = TestClient(app.app)
            response = client.post("/api/users/admin-1/delete")
            assert (
                response.status_code == 403
            ), f"Expected 403 for non-admin POST /api/users/delete, got {response.status_code}"
        finally:
            app.app.dependency_overrides.clear()

    def test_regular_user_cannot_toggle_user(self):
        """Non-admin user POST /api/users/{id}/toggle must return 403.

        Verifies that require_admin blocks non-admin users from enabling
        or disabling other user accounts.
        """
        import app

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("regular-1")
        try:
            client = TestClient(app.app)
            response = client.post("/api/users/admin-1/toggle", json={"enabled": False})
            assert (
                response.status_code == 403
            ), f"Expected 403 for non-admin POST /api/users/toggle, got {response.status_code}"
        finally:
            app.app.dependency_overrides.clear()

    def test_regular_user_cannot_update_settings(self):
        """Non-admin user POST /api/settings/save must return 403.

        Verifies that require_admin blocks non-admin users from modifying
        global panel settings (telegram, SSL, captcha, etc.).
        """
        import app

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("regular-1")
        try:
            client = TestClient(app.app)
            response = client.post(
                "/api/settings/save",
                json={
                    "appearance": {},
                    "sync": {},
                    "captcha": {},
                    "telegram": {"enabled": False, "token": ""},
                    "ssl": {},
                    "limits": {},
                    "protocol_paths": {},
                },
            )
            assert (
                response.status_code == 403
            ), f"Expected 403 for non-admin POST /api/settings/save, got {response.status_code}"
        finally:
            app.app.dependency_overrides.clear()

    def test_regular_user_cannot_add_connection(self):
        """Non-admin user POST /api/users/{id}/connections/add must return 403.

        Verifies that require_admin blocks non-admin users from adding
        VPN connections to arbitrary user accounts.
        """
        import app

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("regular-1")
        try:
            client = TestClient(app.app)
            response = client.post(
                "/api/users/regular-1/connections/add",
                json={
                    "server_id": 0,
                    "protocol": "awg",
                    "name": "evil-connection",
                },
            )
            assert response.status_code == 403, (
                f"Expected 403 for non-admin POST /api/users/connections/add, "
                f"got {response.status_code}"
            )
        finally:
            app.app.dependency_overrides.clear()
