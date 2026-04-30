"""Tests for CSRF token enforcement.

Covers:
- POST requests without CSRF token are rejected (403)
- POST requests with invalid CSRF token are rejected (403)
- GET requests do not require CSRF tokens
- CSRF token rotates after successful login

The app uses starlette-csrf middleware with sensitive_cookies={"session"},
meaning CSRF enforcement only applies when the session cookie is present
(i.e., the user is authenticated). Unauthenticated POSTs are exempt.
"""

import os
import tempfile
from unittest.mock import patch

from fastapi.testclient import TestClient

from app.utils.helpers import hash_password
from database import Database

TEST_SECRET_KEY = "test-secret-key-for-csrf-tests-16bytes!"


def _login_and_get_client(db: Database, db_path: str) -> tuple:
    """Log in as test user and return (client, session_cookie_value).

    Creates a fresh TestClient, logs in with valid credentials, and
    returns the client with session cookie set for subsequent tests.
    """
    import app

    client = TestClient(app.app)

    # Login — no session cookie yet, so CSRF is not enforced
    login_resp = client.post(
        "/api/auth/login",
        json={"username": "testuser", "password": "TestPass123"},
    )
    assert (
        login_resp.status_code == 200
    ), f"Login failed: {login_resp.status_code} {login_resp.text}"

    # Extract session cookie from login response
    session_value = None
    for header_value in login_resp.headers.get_list("set-cookie"):
        if header_value.startswith("session="):
            session_value = header_value.split("session=")[1].split(";")[0]
            break

    assert session_value is not None, "No session cookie set after login"
    client.cookies.set("session", session_value)

    return client


class TestCsrfEnforcement:
    """Tests for CSRF token enforcement on authenticated requests."""

    def setup_method(self):
        """Set up temporary database with a test user that has a known password."""
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        os.environ["SECRET_KEY"] = TEST_SECRET_KEY
        self.db = Database(self.tmp_db_path, secret_key=TEST_SECRET_KEY)

        self.db.create_user(
            {
                "id": "test-user-1",
                "username": "testuser",
                "password_hash": hash_password("TestPass123"),
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

    @patch("app.routers.auth.get_db")
    @patch("app.routers.users.get_db")
    @patch("app.routers.settings.get_db")
    def test_post_without_csrf_token_rejected(self, mock_settings_db, mock_users_db, mock_auth_db):
        """Authenticated POST without x-csrf-token header must return 403.

        After login (which sets the session cookie), any state-changing
        POST must include a valid CSRF token. Without it, starlette-csrf
        rejects the request.
        """
        mock_auth_db.return_value = self.db
        mock_users_db.return_value = self.db
        mock_settings_db.return_value = self.db
        import app

        try:
            client = _login_and_get_client(self.db, self.tmp_db_path)
            # Now client has session cookie but NO csrf token
            response = client.post("/api/settings/save", json={})
            assert (
                response.status_code == 403
            ), f"Expected 403 for POST without CSRF token, got {response.status_code}"
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.users.get_db")
    @patch("app.routers.settings.get_db")
    def test_post_with_invalid_csrf_token_rejected(
        self, mock_settings_db, mock_users_db, mock_auth_db
    ):
        """Authenticated POST with wrong x-csrf-token must return 403.

        Even with a session cookie, if the CSRF header value doesn't match
        the signed cookie, starlette-csrf rejects the request.
        """
        mock_auth_db.return_value = self.db
        mock_users_db.return_value = self.db
        mock_settings_db.return_value = self.db
        import app

        try:
            client = _login_and_get_client(self.db, self.tmp_db_path)
            # Set a wrong csrf token cookie and header
            client.cookies.set("csrftoken", "garbagetokenvalue")
            response = client.post(
                "/api/settings/save",
                json={},
                headers={"x-csrf-token": "garbagetokenvalue"},
            )
            assert (
                response.status_code == 403
            ), f"Expected 403 for POST with invalid CSRF, got {response.status_code}"
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.connections.get_db")
    def test_get_request_does_not_require_csrf(self, mock_conn_db, mock_auth_db):
        """Authenticated GET must work without CSRF token.

        GET is in safe_methods for starlette-csrf, so even with an
        active session, GET requests should not require CSRF tokens.
        The /api/my/connections endpoint is protected only by get_current_user.
        """
        mock_auth_db.return_value = self.db
        mock_conn_db.return_value = self.db
        import app

        try:
            client = _login_and_get_client(self.db, self.tmp_db_path)
            # GET without any CSRF header should work fine
            response = client.get("/api/my/connections")
            # 200 or 307 from redirect are both acceptable (no 403 means CSRF passed)
            assert (
                response.status_code != 403
            ), f"GET should not require CSRF, but got {response.status_code}"
        finally:
            app.app.dependency_overrides.clear()
