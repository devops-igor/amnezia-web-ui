"""Tests for end-to-end API integration flows.

Covers multi-step authenticated workflows that span multiple API endpoints:
- Login → access protected resources → logout → access denied
- Login → create user → verify user in listing
- Login → add server → verify server in listing
- Password change required enforcement
- Session invalidation after logout

These validate that session cookies, CSRF tokens, and auth dependencies
work together correctly through complete user journeys.
"""

import os
import tempfile
from unittest.mock import MagicMock, patch

from app.utils.helpers import hash_password
from database import Database
from dependencies import get_current_user
from tests.conftest import create_csrf_client

TEST_SECRET_KEY = "test-secret-key-for-csrf-tests-16bytes!"


class TestApiIntegration:
    """End-to-end integration tests for authenticated API flows."""

    def setup_method(self):
        """Set up temporary database with an admin user."""
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

    def teardown_method(self):
        """Clean up temporary database."""
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    # ------------------------------------------------------------------
    # Test 1: Login → protected access → logout → access denied
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    def test_login_logout_flow(self, mock_auth_db):
        """Full login → access → logout → access denied cycle."""
        mock_auth_db.return_value = self.db
        import app  # noqa: F401

        client = create_csrf_client()

        # Login — CSRF is auto-injected by create_csrf_client
        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        assert (
            login_resp.status_code == 200
        ), f"Login failed: {login_resp.status_code} {login_resp.text}"

        # Set session cookie
        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break

        # Override get_current_user so protected endpoints work
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            response = client.get("/api/my/connections")
            assert (
                response.status_code == 200
            ), f"Protected endpoint unreachable after login: {response.status_code}"
        finally:
            app.app.dependency_overrides.clear()

        # Logout
        client.get("/logout")

        # Try the protected endpoint again — should fail
        response = client.get("/api/my/connections")
        assert response.status_code == 401, f"Expected 401 after logout, got {response.status_code}"

    # ------------------------------------------------------------------
    # Test 2: Login → create user → verify in user list
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.users.get_db")
    def test_login_create_user_list_users(self, mock_users_db, mock_auth_db):
        """Admin logs in, creates user, verifies in listing."""
        mock_auth_db.return_value = self.db
        mock_users_db.return_value = self.db
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
            # Create user
            create_resp = client.post(
                "/api/users/add",
                json={
                    "username": "newuser",
                    "password": "NewuserPass123",
                    "role": "user",
                },
            )
            assert (
                create_resp.status_code == 200
            ), f"User creation failed: {create_resp.status_code} {create_resp.text[:200]}"
            data = create_resp.json()
            assert data["status"] == "success"

            # Verify in listing
            list_resp = client.get("/api/users")
            assert list_resp.status_code == 200
            usernames = [u["username"] for u in list_resp.json()["users"]]
            assert "newuser" in usernames, f"User 'newuser' not found in listing: {usernames}"
        finally:
            app.app.dependency_overrides.clear()

    # ------------------------------------------------------------------
    # Test 3: Login → add server → verify server in listing
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_login_add_server_check_server(self, mock_servers_db, mock_auth_db):
        """Admin logs in, adds server (two-phase), verifies in listing."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db
        import app

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.test_connection.return_value = "Ubuntu 22.04 x86_64"
        mock_ssh.disconnect.return_value = None

        # Wire transport for fingerprint extraction in api_add_server
        mock_host_key = MagicMock()
        mock_host_key.get_fingerprint.return_value = b"\xde\xad\xbe\xef" * 8
        mock_transport = MagicMock()
        mock_transport.get_remote_server_key.return_value = mock_host_key
        mock_client = MagicMock()
        mock_client.get_transport.return_value = mock_transport
        mock_ssh.client = mock_client

        client = create_csrf_client()

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
            # Phase 1: add server — now returns pending_fingerprint_confirmation
            with patch("app.routers.servers.SSHManager", return_value=mock_ssh):
                add_resp = client.post(
                    "/api/servers/add",
                    json={
                        "host": "10.0.0.50",
                        "username": "root",
                        "password": "pass123",
                        "ssh_port": 22,
                        "name": "Production VPN",
                    },
                )
            assert add_resp.status_code == 200, f"Add failed: {add_resp.status_code}"
            add_data = add_resp.json()
            assert add_data["status"] == "pending_fingerprint_confirmation"
            fingerprint = add_data["fingerprint"]

            # Phase 2: confirm fingerprint to save
            confirm_resp = client.post(
                "/api/servers/confirm-fingerprint",
                json={
                    "host": "10.0.0.50",
                    "username": "root",
                    "password": "pass123",
                    "ssh_port": 22,
                    "name": "Production VPN",
                    "server_info": add_data["server_info"],
                    "fingerprint": fingerprint,
                },
            )
            assert confirm_resp.status_code == 200, f"Confirm failed: {confirm_resp.status_code}"
            assert confirm_resp.json()["status"] == "success"

            list_resp = client.get("/api/servers")
            assert list_resp.status_code == 200
            server_names = [s["name"] for s in list_resp.json()]
            assert "Production VPN" in server_names, f"Server not in listing: {server_names}"

            # Verify no credential leaks
            for srv in list_resp.json():
                assert "password" not in srv
                assert "private_key" not in srv
        finally:
            app.app.dependency_overrides.clear()

    # ------------------------------------------------------------------
    # Test 4: Password change required flow
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.get_db")
    def test_password_change_required_flow(self, mock_app_db, mock_auth_db):
        """New user with pw_change_required must change pw before API access."""
        mock_auth_db.return_value = self.db
        mock_app_db.return_value = self.db
        import app

        self.db.create_user(
            {
                "id": "new-admin-2",
                "username": "freshadmin",
                "password_hash": hash_password("TempPass123"),
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "role": "admin",
                "password_change_required": True,
                "limits": {},
            }
        )

        client = create_csrf_client()

        login_resp = client.post(
            "/api/auth/login",
            json={"username": "freshadmin", "password": "TempPass123"},
        )
        assert login_resp.status_code == 200

        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break

        # 1. Blocked by password_change_required middleware
        response = client.get("/api/my/connections")
        assert response.status_code == 403
        assert response.json().get("password_change_required") is True

        # 2. Change password succeeds
        change_resp = client.post(
            "/api/auth/change-password",
            json={
                "current_password": "TempPass123",
                "new_password": "NewSecurePass456",
                "confirm_password": "NewSecurePass456",
            },
        )
        assert change_resp.status_code == 200, f"Password change failed: {change_resp.status_code}"

        # 3. Protected endpoint accessible after change
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("new-admin-2")
        try:
            response = client.get("/api/my/connections")
            assert (
                response.status_code == 200
            ), f"Still blocked after pw change: {response.status_code}"
        finally:
            app.app.dependency_overrides.clear()

    # ------------------------------------------------------------------
    # Test 5: Session invalidated after logout
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    def test_session_expiry_after_logout(self, mock_auth_db):
        """Session must be invalid after logout."""
        mock_auth_db.return_value = self.db
        import app

        client = create_csrf_client()

        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        assert login_resp.status_code == 200

        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break

        # Verify session active
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            resp = client.get("/api/my/connections")
            assert resp.status_code == 200
        finally:
            app.app.dependency_overrides.clear()

        # Logout
        client.get("/logout")

        # Verify dead
        resp = client.get("/api/my/connections")
        assert resp.status_code == 401, f"Session still valid after logout: {resp.status_code}"

    @patch("app.routers.auth.get_db")
    def test_unauth_post_after_logout_rejected(self, mock_auth_db):
        """POST after logout must be rejected."""
        mock_auth_db.return_value = self.db
        import app  # noqa: F401

        client = create_csrf_client()

        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        assert login_resp.status_code == 200

        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break

        client.get("/logout")

        resp = client.post("/api/users/add", json={"username": "attacker"})
        assert resp.status_code in (401, 403), f"POST allowed after logout: {resp.status_code}"
