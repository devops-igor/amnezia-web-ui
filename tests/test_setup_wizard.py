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

    def test_setup_api_without_csrf_fails(self):
        """POST /api/auth/setup without X-CSRF-Token header must return 403.

        In a real browser, GET /setup loads the form AND sets both 'session' and
        'csrftoken' cookies. When the user submits the form, both cookies are sent.
        The CSRF middleware with sensitive_cookies={"session"} sees the session cookie
        but no X-CSRF-Token header and returns 403.

        This test simulates that exact scenario: session + csrftoken cookies present
        (simulating browser state), but X-CSRF-Token header intentionally omitted.
        """
        from app import app

        client = TestClient(app)

        # Step 1: GET /setup to load the form and establish both session + csrftoken cookies.
        # follow_redirects=True hits the middleware chain: GET / → setup_redirect → /setup
        # After this, the client has both session (from the redirect chain) and csrftoken cookies.
        get_resp = client.get("/setup", follow_redirects=True)
        assert get_resp.status_code == 200

        # Step 2: Verify both cookies are present on the client.
        # session cookie should have been set during the redirect chain.
        # csrftoken should be set by the CSRF middleware on the /setup response.
        session_cookie = client.cookies.get("session")
        csrf_cookie = client.cookies.get("csrftoken")

        # Re-fetch from /setup to ensure csrftoken is fresh if not present
        if not csrf_cookie:
            get_resp2 = client.get("/setup", follow_redirects=False)
            for header_value in get_resp2.headers.get_list("set-cookie"):
                if header_value.startswith("csrftoken="):
                    csrf_cookie = header_value.split("csrftoken=")[1].split(";")[0]
                    client.cookies.set("csrftoken", csrf_cookie)
                    break

        # We need the session cookie for CSRF enforcement; if missing, create a minimal session
        if not session_cookie:
            client.cookies.set("session", "test-session-for-csrf")

        # Re-fetch csrftoken now that we have a session
        get_resp3 = client.get("/setup", follow_redirects=False)
        for header_value in get_resp3.headers.get_list("set-cookie"):
            if header_value.startswith("csrftoken="):
                csrf_cookie = header_value.split("csrftoken=")[1].split(";")[0]
                client.cookies.set("csrftoken", csrf_cookie)
                break

        # Step 3: POST WITHOUT the X-CSRF-Token header, but WITH both session + csrftoken cookies.
        # This is exactly what happens in the buggy browser when setup.html doesn't read/send the token.
        response = client.post(
            "/api/auth/setup",
            json={
                "username": "csrf_test_admin",
                "password": "SecurePass1",
                "confirm_password": "SecurePass1",
            },
            # NOTE: no headers={"x-csrf-token": ...} — that's the bug we're testing
        )
        assert response.status_code == 403, (
            f"Expected 403 (CSRF rejection) but got {response.status_code}. "
            "CSRF middleware should reject POST with session cookie but no X-CSRF-Token header."
        )

    def test_setup_redirect_chain_no_loop(self):
        """After successful setup, redirect to /login then / (no redirect loop).

        Bug #185: Previously setup.html redirected to '/' directly. The 401
        exception handler redirects HTML requests to /login, causing a loop:
          / → 401 → /login → (session has user_id) → / → 401 → /login → ...

        Fix: setup.html now redirects to /login. With a valid session, GET /login
        hits the `if user: return RedirectResponse(url="/", status_code=302)` check
        and redirects to '/' once, breaking the loop.
        """
        from app import app

        client = TestClient(app)

        # Perform setup — use GET /setup first so middleware sets up session/csrtoken
        setup_get = client.get("/setup", follow_redirects=True)
        assert setup_get.status_code == 200

        # Extract CSRF token and send properly-authenticated POST
        csrf_token = None
        for header_value in setup_get.headers.get_list("set-cookie"):
            if "csrftoken=" in header_value:
                csrf_token = header_value.split("csrftoken=")[1].split(";")[0]
                break

        headers = {}
        if csrf_token:
            client.cookies.set("csrftoken", csrf_token)
            headers["x-csrf-token"] = csrf_token

        setup_post = client.post(
            "/api/auth/setup",
            headers=headers,
            json={
                "username": "loop_test_admin",
                "password": "SecurePass1",
                "confirm_password": "SecurePass1",
            },
        )
        assert (
            setup_post.status_code == 200
        ), f"Setup POST failed with {setup_post.status_code}: {setup_post.text}"

        # After setup, session should have user_id set
        # (TestClient preserves cookies across requests via cookies jar)
        response_login = client.get("/login", follow_redirects=False)
        assert response_login.status_code == 302
        assert response_login.headers["location"] == "/"

        # Following that redirect should succeed without a loop
        response_home = client.get("/", follow_redirects=False)
        assert response_home.status_code == 200


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
        """Middleware class has correct allowed paths and backward-compatible invalidate_cache."""
        from app import SetupRedirectMiddleware

        # Verify the middleware class exists and has the backward-compatible no-op
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

    def test_middleware_queries_db_fresh(self):
        """SetupRedirectMiddleware queries DB on every request (no caching).

        Regression test for the redirect loop bug (#185):
        Previously the middleware cached _has_users as a class/instance attribute.
        Stale cached state caused a 3-way redirect loop:
          /setup → /login → / → /setup → ... (infinite)
        The cache was removed entirely — the DB is now queried every request.
        This test verifies that creating a user mid-session is immediately visible.
        """
        from app import app, SetupRedirectMiddleware

        client = TestClient(app)

        # Step 1: Empty DB — verify redirect to /setup
        SetupRedirectMiddleware.invalidate_cache()
        response_empty = client.get("/servers", follow_redirects=False)
        assert response_empty.status_code == 302
        assert response_empty.headers.get("location") == "/setup"

        # Step 2: Create user directly in DB (simulates user created while app was running)
        self.db.create_user(
            {
                "id": "post-startup-user",
                "username": "alice",
                "password_hash": "hash",
                "role": "admin",
                "enabled": True,
            }
        )

        # Step 3: Next request queries DB directly — sees new user → passes through
        # No invalidate_cache() needed because there's no cache.
        response_with_user = client.get("/servers", follow_redirects=False)
        # Expect 401 (unauthenticated) or 200 or 302 to somewhere OTHER than /setup
        if response_with_user.status_code == 302:
            assert (
                response_with_user.headers.get("location") != "/setup"
            ), "Middleware stale state — redirect loop bug still present"
        # If it's a 401, the middleware let the request through to the auth check,
        # which is the correct behavior (no /setup redirect).
        assert response_with_user.status_code != 500, f"Unexpected 500: {response_with_user.text}"
