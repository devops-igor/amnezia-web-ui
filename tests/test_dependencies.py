"""Unit tests for FastAPI dependency functions in dependencies.py.

Covers get_current_user, require_admin, and get_current_user_optional.
"""

import os
import tempfile
from datetime import datetime
from unittest.mock import patch

import pytest
from fastapi import HTTPException, Request

from database import Database
from dependencies import get_current_user, get_current_user_optional, require_admin


@pytest.fixture
def temp_db():
    """Create a temporary SQLite database for testing."""
    with tempfile.NamedTemporaryFile(suffix=".db", delete=False) as f:
        db_path = f.name
    db = Database(db_path)
    yield db
    conn = db._get_conn()
    conn.close()
    os.unlink(db_path)


def _make_request(session_data: dict | None = None) -> Request:
    """Build a minimal Starlette Request with a mock session."""
    scope = {
        "type": "http",
        "method": "GET",
        "path": "/",
        "headers": [],
    }
    request = Request(scope)
    # Inject session directly into scope so SessionMiddleware is not needed
    request.scope["session"] = session_data or {}
    return request


class TestGetCurrentUser:
    """Tests for get_current_user dependency."""

    @pytest.mark.asyncio
    async def test_raises_401_when_no_session(self):
        """Should raise HTTPException 401 when no user_id in session."""
        request = _make_request(session_data={})
        with pytest.raises(HTTPException) as exc_info:
            await get_current_user(request)
        assert exc_info.value.status_code == 401
        assert exc_info.value.detail == "Not authenticated"

    @pytest.mark.asyncio
    async def test_raises_401_when_user_not_found(self):
        """Should raise HTTPException 401 when user_id does not exist in DB."""
        request = _make_request(session_data={"user_id": "nonexistent-user"})
        with pytest.raises(HTTPException) as exc_info:
            await get_current_user(request)
        assert exc_info.value.status_code == 401
        assert exc_info.value.detail == "Not authenticated"

    @pytest.mark.asyncio
    async def test_returns_user_when_authenticated(self, temp_db):
        """Should return user dict when session has valid user_id."""
        temp_db.create_user(
            {
                "id": "user-1",
                "username": "alice",
                "password_hash": "salt$hash",
                "role": "user",
                "enabled": True,
                "created_at": datetime.now().isoformat(),
            }
        )
        request = _make_request(session_data={"user_id": "user-1"})
        with patch("config.get_db", return_value=temp_db):
            user = await get_current_user(request)
        assert user["id"] == "user-1"
        assert user["username"] == "alice"
        assert user["role"] == "user"


class TestRequireAdmin:
    """Tests for require_admin dependency."""

    @pytest.mark.asyncio
    async def test_raises_403_for_non_admin_user(self):
        """Should raise HTTPException 403 when user role is not admin/support."""
        user = {"role": "user"}
        with pytest.raises(HTTPException) as exc_info:
            await require_admin(user)
        assert exc_info.value.status_code == 403
        assert exc_info.value.detail == "Admin access required"

    @pytest.mark.asyncio
    async def test_returns_admin_user(self):
        """Should return user dict when role is admin."""
        user = {"role": "admin", "id": "admin-1"}
        result = await require_admin(user)
        assert result == user

    @pytest.mark.asyncio
    async def test_returns_support_user(self):
        """Should return user dict when role is support."""
        user = {"role": "support", "id": "support-1"}
        result = await require_admin(user)
        assert result == user

    def test_dependency_chain_via_fastapi_app(self):
        """require_admin should be overridable via dependency_overrides in TestClient."""
        import app
        from fastapi.testclient import TestClient

        client = TestClient(app.app)
        # Override get_current_user to return a non-admin
        app.app.dependency_overrides[get_current_user] = lambda: {"role": "user", "id": "u1"}
        try:
            response = client.post("/api/servers/add", json={})
            # Because get_current_user returns non-admin, require_admin should still
            # raise 403 even though get_current_user is overridden
            assert response.status_code == 403
        finally:
            app.app.dependency_overrides.clear()


class TestGetCurrentUserOptional:
    """Tests for get_current_user_optional dependency."""

    def test_returns_none_when_no_session(self):
        """Should return None when no user_id in session."""
        request = _make_request(session_data={})
        result = get_current_user_optional(request)
        assert result is None

    def test_returns_none_when_user_not_found(self):
        """Should return None when user_id does not exist in DB."""
        request = _make_request(session_data={"user_id": "missing-user"})
        result = get_current_user_optional(request)
        assert result is None

    def test_returns_user_when_authenticated(self, temp_db):
        """Should return user dict when session has valid user_id."""
        temp_db.create_user(
            {
                "id": "user-2",
                "username": "bob",
                "password_hash": "salt$hash",
                "role": "user",
                "enabled": True,
                "created_at": datetime.now().isoformat(),
            }
        )
        request = _make_request(session_data={"user_id": "user-2"})
        with patch("config.get_db", return_value=temp_db):
            result = get_current_user_optional(request)
        assert result["id"] == "user-2"
        assert result["username"] == "bob"


class TestDependencyOverrides:
    """Tests verifying FastAPI dependency_override mechanism works end-to-end."""

    def test_override_get_current_user_via_testclient(self, temp_db):
        """app.app.dependency_overrides[get_current_user] should inject user into routes."""
        import app
        from fastapi.testclient import TestClient

        client = TestClient(app.app)
        fake_user = {"id": "fake-1", "username": "fake", "role": "user"}
        app.app.dependency_overrides[get_current_user] = lambda: fake_user
        try:
            with patch("config.get_db", return_value=temp_db):
                response = client.get("/api/leaderboard")
            assert response.status_code == 200
            assert response.json()["current_user_rank"] is None
        finally:
            app.app.dependency_overrides.clear()

    def test_override_require_admin_via_testclient(self):
        """app.app.dependency_overrides[require_admin] should inject admin into routes."""
        import app
        from tests.conftest import create_csrf_client

        client = create_csrf_client()
        fake_admin = {"id": "admin-1", "username": "admin", "role": "admin"}
        app.app.dependency_overrides[require_admin] = lambda: fake_admin
        try:
            # POST to /api/servers/add with empty body — 4xx means auth passed
            response = client.post("/api/servers/add", json={})
            assert response.status_code in (400, 422)
        finally:
            app.app.dependency_overrides.clear()

    def test_clear_overrides_restores_default_401(self, temp_db):
        """After clearing overrides, routes should require real auth again."""
        import app
        from fastapi.testclient import TestClient

        client = TestClient(app.app)
        # Set and then immediately clear override
        app.app.dependency_overrides[get_current_user] = lambda: {
            "id": "fake",
            "username": "fake",
            "role": "user",
        }
        app.app.dependency_overrides.clear()
        with patch("config.get_db", return_value=temp_db):
            response = client.get("/api/leaderboard")
        assert response.status_code == 401
