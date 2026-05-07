"""
Tests for connection rename endpoint — POST /api/my/connections/{connection_id}/rename
"""

import os
import tempfile
from unittest.mock import patch

from database import Database
from dependencies import get_current_user
from tests.conftest import create_csrf_client


class TestRenameConnection:
    """Tests for POST /api/my/connections/{connection_id}/rename"""

    def setup_method(self):
        """Set up temp DB, test user, test server, and test connections."""
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        self.db = Database(self.tmp_db_path)

        # Create test user
        self.db.create_user(
            {
                "id": "test-user-1",
                "username": "testuser",
                "password_hash": "hashed_password",
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "limits": {},
            }
        )

        # Create a second user for ownership tests
        self.db.create_user(
            {
                "id": "test-user-2",
                "username": "otheruser",
                "password_hash": "hashed_password",
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "limits": {},
            }
        )

        # Create test server
        self.db.create_server(
            {
                "name": "Test Server",
                "host": "test.example.com",
                "protocols": {"awg": {"installed": True, "port": "55424"}},
            }
        )

        # Create test connections
        self.db.create_connection(
            {
                "id": "conn-1",
                "user_id": "test-user-1",
                "server_id": 1,
                "protocol": "awg",
                "client_id": "client-1",
                "name": "My VPN",
                "created_at": "2026-01-01T00:00:00",
            }
        )

        self.db.create_connection(
            {
                "id": "conn-2",
                "user_id": "test-user-1",
                "server_id": 1,
                "protocol": "awg",
                "client_id": "client-2",
                "name": "Work VPN",
                "created_at": "2026-01-02T00:00:00",
            }
        )

        self.db.create_connection(
            {
                "id": "conn-3",
                "user_id": "test-user-2",
                "server_id": 1,
                "protocol": "awg",
                "client_id": "client-3",
                "name": "Other VPN",
                "created_at": "2026-01-03T00:00:00",
            }
        )

    def teardown_method(self):
        """Clean up temporary database."""
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    @patch("app.routers.connections.get_db")
    def test_rename_success(self, mock_get_db):
        """Rename a connection returns 200 with status and new name."""
        import app

        mock_get_db.return_value = self.db
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("test-user-1")
        try:
            client = create_csrf_client()

            response = client.post(
                "/api/my/connections/conn-1/rename",
                json={"name": "Home Server"},
            )

            assert response.status_code == 200
            data = response.json()
            assert data["status"] == "success"
            assert data["name"] == "Home Server"

            # Verify DB was actually updated
            updated = self.db.get_connection_by_id("conn-1")
            assert updated["name"] == "Home Server"
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.connections.get_db")
    def test_rename_not_found(self, mock_get_db):
        """Renaming a non-existent connection returns 404."""
        import app

        mock_get_db.return_value = self.db
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("test-user-1")
        try:
            client = create_csrf_client()

            response = client.post(
                "/api/my/connections/nonexistent-id/rename",
                json={"name": "Whatever"},
            )

            assert response.status_code == 404
            data = response.json()
            assert data["error"] == "Connection not found"
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.connections.get_db")
    def test_rename_other_users_connection(self, mock_get_db):
        """Cannot rename a connection belonging to another user — returns 404."""
        import app

        mock_get_db.return_value = self.db
        # User 1 tries to rename User 2's connection
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("test-user-1")
        try:
            client = create_csrf_client()

            response = client.post(
                "/api/my/connections/conn-3/rename",
                json={"name": "Stolen Connection"},
            )

            assert response.status_code == 404
            data = response.json()
            assert data["error"] == "Connection not found"

            # Verify DB was NOT changed
            unchanged = self.db.get_connection_by_id("conn-3")
            assert unchanged["name"] == "Other VPN"
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.connections.get_db")
    def test_rename_duplicate_name(self, mock_get_db):
        """Renaming to a name that already exists returns 409."""
        import app

        mock_get_db.return_value = self.db
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("test-user-1")
        try:
            client = create_csrf_client()

            # conn-1 is "My VPN", conn-2 is "Work VPN"
            # Try to rename conn-1 to "Work VPN" (already used by conn-2)
            response = client.post(
                "/api/my/connections/conn-1/rename",
                json={"name": "Work VPN"},
            )

            assert response.status_code == 409
            data = response.json()
            assert data["error"] == "duplicate_name"
            assert "message" in data
            assert "already exists" in data["message"]

            # Verify DB was NOT changed
            unchanged = self.db.get_connection_by_id("conn-1")
            assert unchanged["name"] == "My VPN"
        finally:
            app.app.dependency_overrides.clear()

    def test_rename_empty_name(self):
        """Empty name is rejected with 422 by Pydantic validation."""
        import app

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("test-user-1")
        try:
            client = create_csrf_client()

            response = client.post(
                "/api/my/connections/conn-1/rename",
                json={"name": ""},
            )

            assert response.status_code == 422
        finally:
            app.app.dependency_overrides.clear()

    def test_rename_whitespace_only(self):
        """Whitespace-only name is rejected with 422."""
        import app

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("test-user-1")
        try:
            client = create_csrf_client()

            response = client.post(
                "/api/my/connections/conn-1/rename",
                json={"name": "   "},
            )

            assert response.status_code == 422
        finally:
            app.app.dependency_overrides.clear()

    def test_rename_max_length(self):
        """Name exceeding 255 characters is rejected with 422."""
        import app

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("test-user-1")
        try:
            client = create_csrf_client()

            response = client.post(
                "/api/my/connections/conn-1/rename",
                json={"name": "A" * 256},
            )

            assert response.status_code == 422
        finally:
            app.app.dependency_overrides.clear()
