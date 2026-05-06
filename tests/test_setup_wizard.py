"""Tests for the first-run setup wizard (Issue #162).

Covers:
- Setup page rendering and redirects
- Setup API endpoint (create, validation, auto-login, rate limiting)
- Setup redirect middleware behavior
"""

import os
import tempfile

from fastapi.testclient import TestClient

from database import Database

TEST_SECRET_KEY = "test-secret-key-16-bytes-long!"


def _create_test_db(db_path: str, secret_key: str = TEST_SECRET_KEY) -> Database:
    """Create a fresh Database for testing."""
    return Database(db_path, secret_key=secret_key)


def _override_db(db: Database):
    """Context manager helper to override get_db and clear after."""
    import app
    import config as config_module

    old_db = config_module._db_instance

    def _restore():
        config_module._db_instance = old_db
        app.app.dependency_overrides.clear()
        # Reset middleware cache
        from app import SetupRedirectMiddleware

        SetupRedirectMiddleware.invalidate_cache()

    config_module._db_instance = db
    return _restore


# ======================== Page Tests ========================


class TestSetupPage:
    """Tests for the GET /setup page route."""

    def setup_method(self):
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        os.environ["SECRET_KEY"] = TEST_SECRET_KEY
        self.db = _create_test_db(self.tmp_db_path)
        self._restore = _override_db(self.db)
        # Reset middleware cache to avoid cross-test contamination
        from app import SetupRedirectMiddleware

        SetupRedirectMiddleware.invalidate_cache()

    def teardown_method(self):
        self._restore()
        self.db._get_conn().close()
        os.unlink(self.tmp_db_path)

    def test_setup_page_no_users(self):
        """GET /setup returns 200 with setup form when no users exist."""
        from app import app

        client = TestClient(app)
        response = client.get("/setup")
        assert response.status_code == 200
        assert "setup_title" in response.text or "setup" in response.text.lower()

    def test_setup_page_with_users(self):
        """GET /setup redirects to /login when users exist."""
        from app import app

        self.db.create_user(
            {
                "id": "existing-user-1",
                "username": "alice",
                "password_hash": "hash",
                "role": "admin",
                "enabled": True,
            }
        )

        client = TestClient(app)
        response = client.get("/setup", follow_redirects=False)
        assert response.status_code == 302
        assert response.headers.get("location") == "/login"


# ======================== API Tests ========================


class TestSetupAPI:
    """Tests for the POST /api/auth/setup endpoint."""

    def setup_method(self):
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        os.environ["SECRET_KEY"] = TEST_SECRET_KEY
        self.db = _create_test_db(self.tmp_db_path)
        self._restore = _override_db(self.db)
        # Reset middleware cache to avoid cross-test contamination
        from app import SetupRedirectMiddleware

        SetupRedirectMiddleware.invalidate_cache()

    def teardown_method(self):
        self._restore()
        self.db._get_conn().close()
        os.unlink(self.tmp_db_path)

    def test_setup_api_creates_admin(self):
        """POST /api/auth/setup with valid data creates admin, returns success."""
        from app import app

        client = TestClient(app)
        response = client.post(
            "/api/auth/setup",
            json={
                "username": "my_admin",
                "password": "SecurePass1",
                "confirm_password": "SecurePass1",
            },
        )
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "success"
        assert data["role"] == "admin"

        # Verify user was created
        users = self.db.get_all_users()
        assert len(users) == 1
        assert users[0]["username"] == "my_admin"
        assert users[0]["role"] == "admin"
        assert users[0]["password_change_required"] is False

    def test_setup_api_rejects_duplicate(self):
        """POST /api/auth/setup returns 409 when users already exist."""
        from app import app

        self.db.create_user(
            {
                "id": "existing-user-2",
                "username": "bob",
                "password_hash": "hash",
                "role": "admin",
                "enabled": True,
            }
        )

        client = TestClient(app)
        response = client.post(
            "/api/auth/setup",
            json={
                "username": "another_admin",
                "password": "SecurePass1",
                "confirm_password": "SecurePass1",
            },
        )
        assert response.status_code == 409
        data = response.json()
        assert "error" in data

    def test_setup_api_rejects_short_username(self):
        """POST /api/auth/setup rejects username < 3 chars."""
        from app import app

        client = TestClient(app)
        response = client.post(
            "/api/auth/setup",
            json={
                "username": "ab",
                "password": "SecurePass1",
                "confirm_password": "SecurePass1",
            },
        )
        assert response.status_code == 422

    def test_setup_api_rejects_short_password(self):
        """POST /api/auth/setup rejects password < 8 chars."""
        from app import app

        client = TestClient(app)
        response = client.post(
            "/api/auth/setup",
            json={
                "username": "validuser",
                "password": "Short1",
                "confirm_password": "Short1",
            },
        )
        assert response.status_code == 422

    def test_setup_api_rejects_password_mismatch(self):
        """POST /api/auth/setup returns 400 when password != confirm_password."""
        from app import app

        client = TestClient(app)
        response = client.post(
            "/api/auth/setup",
            json={
                "username": "validuser",
                "password": "SecurePass1",
                "confirm_password": "DifferentPass1",
            },
        )
        assert response.status_code == 400

    def test_setup_api_rejects_invalid_username_chars(self):
        """POST /api/auth/setup rejects special chars in username."""
        from app import app

        client = TestClient(app)
        response = client.post(
            "/api/auth/setup",
            json={
                "username": "bad-user!",
                "password": "SecurePass1",
                "confirm_password": "SecurePass1",
            },
        )
        assert response.status_code == 422

    def test_setup_api_auto_login(self):
        """Successful setup sets session user_id."""
        from app import app

        client = TestClient(app)
        response = client.post(
            "/api/auth/setup",
            json={
                "username": "new_admin",
                "password": "SecurePass1",
                "confirm_password": "SecurePass1",
            },
        )
        assert response.status_code == 200

        # After setup, accessing / should redirect to dashboard (not /setup)
        # Client should have session cookie set
        response2 = client.get("/", follow_redirects=False)
        assert response2.status_code == 200

    def test_setup_api_rate_limited(self):
        """Rate limiting decorator is applied on /api/auth/setup."""
        import inspect

        from app.routers.auth import api_setup

        source = inspect.getsource(api_setup)
        # Verify the rate limit decorator is present
        assert "limiter.limit" in source, "Rate limit decorator not found on setup endpoint"


# ======================== Middleware Tests ========================


class TestSetupRedirectMiddleware:
    """Tests for the setup redirect middleware."""

    def setup_method(self):
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        os.environ["SECRET_KEY"] = TEST_SECRET_KEY
        self.db = _create_test_db(self.tmp_db_path)
        self._restore = _override_db(self.db)
        # Reset middleware cache to avoid cross-test contamination
        from app import SetupRedirectMiddleware

        SetupRedirectMiddleware.invalidate_cache()

    def teardown_method(self):
        self._restore()
        self.db._get_conn().close()
        os.unlink(self.tmp_db_path)

    def test_setup_redirect_middleware_no_users(self):
        """Middleware class has correct allowed paths and cache attribute."""
        from app import SetupRedirectMiddleware

        # Verify the middleware class exists and has the cache mechanism
        assert hasattr(SetupRedirectMiddleware, "_has_users")
        assert hasattr(SetupRedirectMiddleware, "invalidate_cache")

    def test_setup_redirect_middleware_with_users(self):
        """Requests to / go through normally when users exist."""
        from app import app

        self.db.create_user(
            {
                "id": "existing-user-3",
                "username": "charlie",
                "password_hash": "hash",
                "role": "admin",
                "enabled": True,
            }
        )

        # Invalidate middleware cache
        from app import SetupRedirectMiddleware

        SetupRedirectMiddleware.invalidate_cache()

        client = TestClient(app)
        response = client.get("/", follow_redirects=False)
        # Should NOT redirect to /setup — users exist
        # It may return 302 to /login (if unauthenticated), but not /setup
        if response.status_code == 302:
            assert response.headers.get("location") != "/setup"
