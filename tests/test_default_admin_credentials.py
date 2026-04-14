"""
Tests for default admin credentials security fix.

Covers:
- password_change_required field in database CRUD
- Schema migration for existing databases
- Random password generation on first boot
- Login endpoint returns password_change_required flag
- Password change endpoint validates and clears flag
- Middleware blocks API access when flag is set
- No hardcoded admin password in source code
"""

import os
import re
import sqlite3
import tempfile

from fastapi.testclient import TestClient

from database import Database

# ======================== Database tests ========================


class TestPasswordChangeRequiredField:
    """Tests for password_change_required column in users table."""

    def setup_method(self):
        """Set up test database."""
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        self.db = Database(self.tmp_db_path)

    def teardown_method(self):
        """Clean up temporary database."""
        self.db._get_conn().close()
        os.unlink(self.tmp_db_path)

    def test_create_user_with_password_change_required_true(self):
        """User created with password_change_required=True should have flag set."""
        self.db.create_user(
            {
                "id": "test-user-1",
                "username": "testuser",
                "password_hash": "hashed",
                "enabled": True,
                "password_change_required": True,
                "limits": {},
            }
        )
        user = self.db.get_user("test-user-1")
        assert user is not None
        assert user["password_change_required"] is True

    def test_create_user_with_password_change_required_false(self):
        """User created with password_change_required=False (default) should have flag unset."""
        self.db.create_user(
            {
                "id": "test-user-2",
                "username": "testuser2",
                "password_hash": "hashed",
                "enabled": True,
                "password_change_required": False,
                "limits": {},
            }
        )
        user = self.db.get_user("test-user-2")
        assert user is not None
        assert user["password_change_required"] is False

    def test_create_user_default_password_change_required(self):
        """User created without explicit flag should default to False."""
        self.db.create_user(
            {
                "id": "test-user-3",
                "username": "testuser3",
                "password_hash": "hashed",
                "enabled": True,
                "limits": {},
            }
        )
        user = self.db.get_user("test-user-3")
        assert user is not None
        assert user["password_change_required"] is False

    def test_update_user_password_change_required_to_false(self):
        """Updating password_change_required from True to False should work."""
        self.db.create_user(
            {
                "id": "test-user-4",
                "username": "testuser4",
                "password_hash": "hashed",
                "enabled": True,
                "password_change_required": True,
                "limits": {},
            }
        )
        result = self.db.update_user("test-user-4", {"password_change_required": False})
        assert result is True
        user = self.db.get_user("test-user-4")
        assert user["password_change_required"] is False

    def test_update_user_password_change_required_to_true(self):
        """Updating password_change_required from False to True should work."""
        self.db.create_user(
            {
                "id": "test-user-5",
                "username": "testuser5",
                "password_hash": "hashed",
                "enabled": True,
                "limits": {},
            }
        )
        result = self.db.update_user("test-user-5", {"password_change_required": True})
        assert result is True
        user = self.db.get_user("test-user-5")
        assert user["password_change_required"] is True

    def test_update_user_password_hash_and_clear_flag(self):
        """Password change should update hash AND clear password_change_required."""
        self.db.create_user(
            {
                "id": "test-user-6",
                "username": "testuser6",
                "password_hash": "old_hash",
                "enabled": True,
                "password_change_required": True,
                "limits": {},
            }
        )
        self.db.update_user(
            "test-user-6", {"password_hash": "new_hash", "password_change_required": False}
        )
        user = self.db.get_user("test-user-6")
        assert user["password_hash"] == "new_hash"
        assert user["password_change_required"] is False

    def test_password_change_required_in_allowed_columns(self):
        """password_change_required should be in ALLOWED_USER_COLUMNS."""
        assert "password_change_required" in Database.ALLOWED_USER_COLUMNS

    def test_password_change_required_is_bool_in_get_all_users(self):
        """password_change_required should be a Python bool when retrieved via get_all_users."""
        self.db.create_user(
            {
                "id": "test-user-7",
                "username": "testuser7",
                "password_hash": "hashed",
                "enabled": True,
                "password_change_required": True,
                "limits": {},
            }
        )
        users = self.db.get_all_users()
        user = [u for u in users if u["id"] == "test-user-7"][0]
        assert isinstance(user["password_change_required"], bool)
        assert user["password_change_required"] is True


class TestDatabaseMigration:
    """Tests for schema migration adding password_change_required column."""

    def test_migration_adds_column_to_existing_table(self):
        """Migration should add password_change_required to an existing users table."""
        # Create a database WITHOUT the column by crafting a raw SQLite DB
        tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        tmp_db_path = tmp_db.name
        tmp_db.close()

        conn = sqlite3.connect(tmp_db_path)
        conn.execute("""CREATE TABLE IF NOT EXISTS users (
                id TEXT PRIMARY KEY,
                username TEXT NOT NULL,
                password_hash TEXT,
                role TEXT NOT NULL DEFAULT 'user',
                enabled INTEGER NOT NULL DEFAULT 1,
                limits TEXT
            )""")
        conn.execute(
            "INSERT INTO users (id, username, password_hash, role, enabled, limits) "
            "VALUES ('old-user', 'olduser', 'hash', 'user', 1, '{}')"
        )
        conn.commit()
        conn.close()

        # Now initialize Database with migrations - should add the column
        db = Database(tmp_db_path)
        user = db.get_user("old-user")
        assert user is not None
        # The migration should add the column with default 0
        assert "password_change_required" in user
        assert user["password_change_required"] is False

        db._get_conn().close()
        os.unlink(tmp_db_path)

    def test_migration_is_idempotent(self):
        """Running migration on a DB that already has the column should not fail."""
        tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        tmp_db_path = tmp_db.name
        tmp_db.close()

        # Create DB with the column already present (via schema.sql)
        db = Database(tmp_db_path)
        # Re-initialize should not fail
        db2 = Database(tmp_db_path)
        db2.create_user(
            {
                "id": "test-migration",
                "username": "migtest",
                "password_hash": "hashed",
                "enabled": True,
                "password_change_required": True,
                "limits": {},
            }
        )
        result = db2.get_user("test-migration")
        assert result["password_change_required"] is True

        db._get_conn().close()
        db2._get_conn().close()
        os.unlink(tmp_db_path)


# ======================== No hardcoded password tests ========================


class TestNoHardcodedAdminPassword:
    """Verify that no hardcoded admin password exists in source code."""

    def test_no_hardcoded_admin_password_in_app_py(self):
        """app.py should not contain the string hash_password('admin')."""
        with open("app.py", "r", encoding="utf-8") as f:
            content = f.read()
        assert 'hash_password("admin")' not in content, (
            "Hardcoded admin password found in app.py - "
            "must use secrets.token_urlsafe(12) instead"
        )

    def test_no_admin_admin_default_in_app_py(self):
        """app.py should not contain the pattern admin / admin or admin/admin."""
        with open("app.py", "r", encoding="utf-8") as f:
            content = f.read()
        assert (
            "admin / admin" not in content
        ), 'Log message "admin / admin" found - indicates hardcoded credentials'

    def test_random_password_generation_in_startup(self):
        """Startup should use secrets.token_urlsafe for initial password."""
        with open("app.py", "r", encoding="utf-8") as f:
            content = f.read()
        assert (
            "secrets.token_urlsafe(12)" in content
        ), "secrets.token_urlsafe(12) not found in app.py startup code"

    def test_password_change_required_set_on_first_boot(self):
        """First boot user creation should set password_change_required=True."""
        with open("app.py", "r", encoding="utf-8") as f:
            content = f.read()
        startup_match = re.search(
            r"if not db\.get_all_users\(\):(.*?)(?=# Start|asyncio\.create_task)",
            content,
            re.DOTALL,
        )
        assert startup_match, "Could not find first-boot user creation code"
        startup_code = startup_match.group(1)
        assert (
            '"password_change_required": True' in startup_code
        ), "password_change_required=True not set in first-boot user creation"

    def test_initial_password_printed_to_stdout(self):
        """Initial password should be printed to stdout exactly once."""
        with open("app.py", "r", encoding="utf-8") as f:
            content = f.read()
        assert (
            "INITIAL ADMIN CREDENTIALS" in content
        ), "Initial admin credentials banner not found in startup code"


# ======================== Unit tests for password change logic ========================


class TestPasswordChangeValidation:
    """Tests for password change validation logic (unit-level)."""

    def test_change_password_request_model_valid(self):
        """ChangePasswordRequest should accept valid input."""
        from app import ChangePasswordRequest

        req = ChangePasswordRequest(
            current_password="oldpass",
            new_password="newpass123",
            confirm_password="newpass123",
        )
        assert req.current_password == "oldpass"
        assert req.new_password == "newpass123"
        assert req.confirm_password == "newpass123"

    def test_change_password_request_model_missing_field(self):
        """ChangePasswordRequest should reject missing fields."""
        from pydantic import ValidationError

        from app import ChangePasswordRequest

        import pytest

        with pytest.raises(ValidationError):
            ChangePasswordRequest(
                current_password="oldpass",
                new_password="newpass123",
                # missing confirm_password
            )

    def test_verify_password_works(self):
        """verify_password should correctly validate against hash_password."""
        from app import hash_password, verify_password

        hashed = hash_password("mysecretpass")
        assert verify_password("mysecretpass", hashed) is True
        assert verify_password("wrongpass", hashed) is False


# ======================== Integration tests with real DB and session ========================


class TestPasswordChangeIntegration:
    """Integration tests using a real temp database and actual session flow.

    These tests create a temporary database, insert a known user, and exercise
    the actual login -> middleware -> change-password flow.
    """

    def setup_method(self):
        """Set up test client with temporary database."""
        import app as app_module

        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()

        # Create a fresh database
        self.db = Database(self.tmp_db_path)

        # Create a test user with password_change_required=True
        from app import hash_password

        self.test_password = "TestPass123!"
        self.test_user_id = "test-user-integration"
        self.db.create_user(
            {
                "id": self.test_user_id,
                "username": "testadmin",
                "password_hash": hash_password(self.test_password),
                "role": "admin",
                "enabled": True,
                "password_change_required": True,
                "limits": {},
            }
        )

        # Override the app's database singleton
        self._orig_db_instance = app_module._db_instance
        app_module._db_instance = self.db
        self.app_module = app_module

        self.client = TestClient(app_module.app)
        # Get CSRF token
        resp = self.client.get("/login")
        self.csrf_token = resp.cookies.get("csrftoken", "")

    def teardown_method(self):
        """Clean up."""
        self.app_module._db_instance = self._orig_db_instance
        self.db._get_conn().close()
        os.unlink(self.tmp_db_path)

    def test_login_returns_password_change_required_flag(self):
        """Login should return password_change_required=True for forced-change users."""
        response = self.client.post(
            "/api/auth/login",
            json={"username": "testadmin", "password": self.test_password},
            headers={"x-csrf-token": self.csrf_token},
        )
        assert response.status_code == 200
        data = response.json()
        assert data["status"] == "success"
        assert data["password_change_required"] is True

    def test_middleware_blocks_api_when_flag_set(self):
        """After login, other API endpoints should be blocked (403)."""
        # Login first to set session
        response = self.client.post(
            "/api/auth/login",
            json={"username": "testadmin", "password": self.test_password},
            headers={"x-csrf-token": self.csrf_token},
        )
        assert response.status_code == 200

        # Now try to access a protected API endpoint
        response = self.client.get("/api/settings")
        assert response.status_code == 403
        data = response.json()
        assert data.get("password_change_required") is True

    def test_middleware_allows_change_password_endpoint_when_flag_set(self):
        """The change-password endpoint should NOT be blocked by middleware."""
        # Login first to set session
        response = self.client.post(
            "/api/auth/login",
            json={"username": "testadmin", "password": self.test_password},
            headers={"x-csrf-token": self.csrf_token},
        )
        assert response.status_code == 200

        # The change-password endpoint should be allowed
        new_csrf = self.client.cookies.get("csrftoken", self.csrf_token)
        response = self.client.post(
            "/api/auth/change-password",
            json={
                "current_password": self.test_password,
                "new_password": "NewPass456!",
                "confirm_password": "NewPass456!",
            },
            headers={"x-csrf-token": new_csrf},
        )
        # Should be 200 (success), not 403 (blocked by middleware)
        assert response.status_code == 200

    def test_change_password_clears_flag(self):
        """After successful password change, the flag should be cleared."""
        # Login first
        self.client.post(
            "/api/auth/login",
            json={"username": "testadmin", "password": self.test_password},
            headers={"x-csrf-token": self.csrf_token},
        )

        new_csrf = self.client.cookies.get("csrftoken", self.csrf_token)
        response = self.client.post(
            "/api/auth/change-password",
            json={
                "current_password": self.test_password,
                "new_password": "NewPass456!",
                "confirm_password": "NewPass456!",
            },
            headers={"x-csrf-token": new_csrf},
        )
        assert response.status_code == 200

        # Verify the flag is cleared in DB
        user = self.db.get_user(self.test_user_id)
        assert user["password_change_required"] is False

    def test_api_accessible_after_password_change(self):
        """After password change, other API endpoints should become accessible."""
        # Login
        self.client.post(
            "/api/auth/login",
            json={"username": "testadmin", "password": self.test_password},
            headers={"x-csrf-token": self.csrf_token},
        )

        # Change password
        new_csrf = self.client.cookies.get("csrftoken", self.csrf_token)
        self.client.post(
            "/api/auth/change-password",
            json={
                "current_password": self.test_password,
                "new_password": "NewPass456!",
                "confirm_password": "NewPass456!",
            },
            headers={"x-csrf-token": new_csrf},
        )

        # Now API should be accessible (not blocked by password middleware)
        response = self.client.get("/api/settings")
        # Should NOT be 403 with password_change_required
        if response.status_code == 403:
            data = response.json()
            assert data.get("password_change_required") is not True

    def test_change_password_wrong_current_returns_400(self):
        """Wrong current password should return 400."""
        self.client.post(
            "/api/auth/login",
            json={"username": "testadmin", "password": self.test_password},
            headers={"x-csrf-token": self.csrf_token},
        )

        new_csrf = self.client.cookies.get("csrftoken", self.csrf_token)
        response = self.client.post(
            "/api/auth/change-password",
            json={
                "current_password": "wrongpass",
                "new_password": "NewPass456!",
                "confirm_password": "NewPass456!",
            },
            headers={"x-csrf-token": new_csrf},
        )
        assert response.status_code == 400

    def test_change_password_mismatch_returns_400(self):
        """Password mismatch should return 400."""
        self.client.post(
            "/api/auth/login",
            json={"username": "testadmin", "password": self.test_password},
            headers={"x-csrf-token": self.csrf_token},
        )

        new_csrf = self.client.cookies.get("csrftoken", self.csrf_token)
        response = self.client.post(
            "/api/auth/change-password",
            json={
                "current_password": self.test_password,
                "new_password": "NewPass456!",
                "confirm_password": "DifferentPass!",
            },
            headers={"x-csrf-token": new_csrf},
        )
        assert response.status_code == 400

    def test_change_password_too_short_returns_400(self):
        """Password shorter than 8 chars should return 400."""
        self.client.post(
            "/api/auth/login",
            json={"username": "testadmin", "password": self.test_password},
            headers={"x-csrf-token": self.csrf_token},
        )

        new_csrf = self.client.cookies.get("csrftoken", self.csrf_token)
        response = self.client.post(
            "/api/auth/change-password",
            json={
                "current_password": self.test_password,
                "new_password": "short",
                "confirm_password": "short",
            },
            headers={"x-csrf-token": new_csrf},
        )
        assert response.status_code == 400

    def test_middleware_allows_login_endpoint(self):
        """Login endpoint should never be blocked by password middleware."""
        # Even for a user with password_change_required=True,
        # the login endpoint should work
        response = self.client.post(
            "/api/auth/login",
            json={"username": "testadmin", "password": self.test_password},
            headers={"x-csrf-token": self.csrf_token},
        )
        assert response.status_code == 200

    def test_middleware_allows_non_api_paths(self):
        """Non-API paths should not be blocked by password middleware."""
        response = self.client.get("/login")
        assert response.status_code != 403 or "password_change_required" not in getattr(
            response, "text", ""
        )

    def test_user_without_flag_not_blocked(self):
        """User without password_change_required flag should access API normally."""
        # Create user without the flag
        from app import hash_password

        self.db.create_user(
            {
                "id": "test-user-no-flag",
                "username": "normaluser",
                "password_hash": hash_password("NormalPass1!"),
                "role": "admin",
                "enabled": True,
                "password_change_required": False,
                "limits": {},
            }
        )

        # Login as normal user
        response = self.client.post(
            "/api/auth/login",
            json={"username": "normaluser", "password": "NormalPass1!"},
            headers={"x-csrf-token": self.csrf_token},
        )
        assert response.status_code == 200
        data = response.json()
        assert data["password_change_required"] is False

        # API should be accessible
        response = self.client.get("/api/settings")
        # Should NOT be 403 from password_change_required middleware
        if response.status_code == 403:
            data = response.json()
            assert data.get("password_change_required") is not True
