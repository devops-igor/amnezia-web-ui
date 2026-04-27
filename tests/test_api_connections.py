"""
Tests for API connection endpoints
"""

import json
import pytest
from unittest.mock import MagicMock, patch
from database import Database
import tempfile
import os

from dependencies import get_current_user, require_admin
from tests.conftest import create_csrf_client


class TestApiMyAddConnection:
    """Tests for /api/my/connections/add endpoint"""

    def setup_method(self):
        """Set up test client and mock data"""
        # Create a temporary database for each test
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        self.db = Database(self.tmp_db_path)

        # Insert a test user
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

        # Insert a test server
        self.db.create_server(
            {
                "name": "Test Server",
                "host": "test.example.com",
                "protocols": {"awg": {"installed": True, "port": "55424"}},
            }
        )

        # Configure settings for rate limiting
        self.db.update_setting(
            "limits",
            {
                "max_connections_per_user": 10,
                "connection_rate_limit_count": 5,
                "connection_rate_limit_window": 60,
            },
        )

    def teardown_method(self):
        """Clean up temporary database."""
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    @patch("app.utils.helpers.get_ssh")
    @patch("app.utils.helpers.get_protocol_manager")
    @patch("app.routers.connections.get_db")
    def test_duplicate_connection_name_returns_json_error(
        self,
        mock_get_db,
        mock_get_protocol_manager,
        mock_get_ssh,
    ):
        """Test that duplicate connection names return proper JSON error"""
        import app

        mock_get_db.return_value = self.db
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("test-user-1")
        try:
            # Mock SSH and protocol manager
            mock_ssh = MagicMock()
            mock_get_ssh.return_value = mock_ssh
            mock_manager = MagicMock()
            mock_manager.add_client.return_value = {"client_id": "test-client-1"}
            mock_get_protocol_manager.return_value = mock_manager

            client = create_csrf_client()

            # First connection should succeed
            # server_id must match the actual DB primary key (auto-increment starts at 1)
            test_server_id = self.db.get_all_servers()[0]["id"]
            response1 = client.post(
                "/api/my/connections/add",
                json={"server_id": test_server_id, "protocol": "awg", "name": "Test Connection"},
                headers={"Authorization": "Bearer test-token"},
            )

            # Update mock data to include the first connection
            self.db.create_connection(
                {
                    "id": "conn-1",
                    "user_id": "test-user-1",
                    "server_id": test_server_id,
                    "protocol": "awg",
                    "client_id": "test-client-1",
                    "name": "Test Connection",
                    "created_at": "2024-01-01T00:00:00",
                }
            )

            # Second connection with duplicate name should fail with JSON error
            response2 = client.post(
                "/api/my/connections/add",
                json={"server_id": test_server_id, "protocol": "awg", "name": "Test Connection"},
                headers={"Authorization": "Bearer test-token"},
            )

            # Verify response
            assert response2.status_code == 409

            # Check that response is valid JSON (not HTML)
            try:
                data = response2.json()
                assert isinstance(data, dict)
                assert data["error"] == "duplicate_name"
                assert "message" in data
                assert "already exists" in data["message"]
            except Exception:
                pytest.fail("Response is not valid JSON")
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.connections.get_db")
    def test_duplicate_connection_error_message_format(self, mock_get_db):
        """Test that the duplicate connection error message is user-friendly"""
        import app

        mock_get_db.return_value = self.db
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("test-user-1")
        try:
            # server_id must match the actual DB primary key (auto-increment starts at 1)
            test_server_id = self.db.get_all_servers()[0]["id"]

            # Add a connection to the test data via the database
            self.db.create_connection(
                {
                    "id": "conn-1",
                    "user_id": "test-user-1",
                    "server_id": test_server_id,
                    "protocol": "awg",
                    "client_id": "test-client-1",
                    "name": "Existing Connection",
                    "created_at": "2024-01-01T00:00:00",
                }
            )

            client = create_csrf_client()

            # Try to create a connection with duplicate name
            response = client.post(
                "/api/my/connections/add",
                json={
                    "server_id": test_server_id,
                    "protocol": "awg",
                    "name": "Existing Connection",
                },
                headers={"Authorization": "Bearer test-token"},
            )

            # Verify response
            assert response.status_code == 409
            data = response.json()

            # Check error message format
            assert data["error"] == "duplicate_name"
            assert "message" in data
            assert "already exists" in data["message"]

            # The expected message should match the frontend expectation
            expected_message = "A connection with this name already exists."
            assert data["message"] == expected_message
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.connections.get_db")
    def test_rate_limit_returns_json_error(self, mock_get_db):
        """Test that rate limit errors return proper JSON response with retry_after"""
        import app

        mock_get_db.return_value = self.db
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("test-user-1")
        try:
            # server_id must match the actual DB primary key (auto-increment starts at 1)
            test_server_id = self.db.get_all_servers()[0]["id"]

            # Simulate rate limiting by adding many recent connection creations
            for i in range(5):  # Same as rate_limit_count
                self.db.log_connection_creation("test-user-1")

            client = create_csrf_client()

            # Try to create a connection (should be rate limited)
            response = client.post(
                "/api/my/connections/add",
                json={"server_id": test_server_id, "protocol": "awg", "name": "Test Connection"},
                headers={"Authorization": "Bearer test-token"},
            )

            # Verify response — rate limit returns 428
            assert response.status_code == 428

            # Check that response is valid JSON
            try:
                data = response.json()
                assert isinstance(data, dict)
                assert "error" in data
                assert "rate limit" in data["error"].lower()
                assert "retry_after" in data
                assert isinstance(data["retry_after"], int)
                assert data["retry_after"] > 0

                # Check Retry-After header
                assert "Retry-After" in response.headers
                assert response.headers["Retry-After"] == str(data["retry_after"])
            except json.JSONDecodeError:
                pytest.fail("Response is not valid JSON")
        finally:
            app.app.dependency_overrides.clear()


class TestApiAddConnectionTelemtFailure:
    """Tests for telemt API failure handling in /api/servers/{server_id}/connections/add"""

    def setup_method(self):
        self.client = create_csrf_client()
        self.mock_data = {
            "users": [
                {
                    "id": "test-user-1",
                    "username": "testuser",
                    "password_hash": "hashed_password",
                    "enabled": True,
                    "traffic_limit": 0,
                    "traffic_used": 0,
                    "limits": {},
                }
            ],
            "servers": [
                {
                    "id": 0,
                    "name": "Test Server",
                    "host": "test.example.com",
                    "protocols": {"telemt": {"installed": True, "port": "443"}},
                }
            ],
            "user_connections": [],
            "connection_creation_log": [],
            "settings": {},
        }

    @patch("app.routers.servers.get_db")
    @patch("app.routers.servers.get_ssh")
    @patch("app.routers.servers.get_protocol_manager")
    def test_telemt_api_failure_returns_500_no_data_written(
        self,
        mock_get_protocol_manager,
        mock_get_ssh,
        mock_get_db,
    ):
        """When telemt API fails, return 500 and do NOT write to database."""
        import app

        mock_db = MagicMock()
        mock_db.get_server_by_id.return_value = self.mock_data["servers"][0]
        mock_db.get_all_users.return_value = []
        mock_db.get_connections_by_user.return_value = []
        mock_db.get_setting.return_value = {}
        mock_get_db.return_value = mock_db
        app.app.dependency_overrides[require_admin] = lambda: {
            "role": "admin",
            "id": 1,
            "username": "admin",
        }

        mock_ssh = MagicMock()
        mock_get_ssh.return_value = mock_ssh
        mock_manager = MagicMock()
        # Simulate telemt API failure
        mock_manager.add_client.return_value = {
            "client_id": "",
            "config": "",
            "vpn_link": "",
            "error": "User already exists",
        }
        mock_get_protocol_manager.return_value = mock_manager

        try:
            response = self.client.post(
                "/api/servers/0/connections/add",
                json={"protocol": "telemt", "name": "Test Connection"},
                headers={"Authorization": "Bearer test-token"},
            )

            assert response.status_code == 500
            data = response.json()
            assert "error" in data
            assert data["error"] == "User already exists"
            # Verify create_connection was never called (no connection written)
            mock_db.create_connection.assert_not_called()
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.servers.get_db")
    @patch("app.routers.servers.get_ssh")
    @patch("app.routers.servers.get_protocol_manager")
    def test_telemt_success_writes_connection(
        self,
        mock_get_protocol_manager,
        mock_get_ssh,
        mock_get_db,
    ):
        """When telemt API succeeds, connection is written to database."""
        import app

        mock_db = MagicMock()
        mock_db.get_server_by_id.return_value = self.mock_data["servers"][0]
        mock_db.get_all_users.return_value = []
        mock_db.get_connections_by_user.return_value = []
        mock_db.get_setting.return_value = {}
        mock_get_db.return_value = mock_db
        app.app.dependency_overrides[require_admin] = lambda: {
            "role": "admin",
            "id": 1,
            "username": "admin",
        }

        mock_ssh = MagicMock()
        mock_get_ssh.return_value = mock_ssh
        mock_manager = MagicMock()
        mock_manager.add_client.return_value = {
            "client_id": "test_user",
            "config": "tg://proxy?server=test.example.com&port=443&secret=abc123",
            "vpn_link": "tg://proxy?server=test.example.com&port=443&secret=abc123",
        }
        mock_get_protocol_manager.return_value = mock_manager

        try:
            response = self.client.post(
                "/api/servers/0/connections/add",
                json={"protocol": "telemt", "name": "Test Connection", "user_id": "test-user-1"},
                headers={"Authorization": "Bearer test-token"},
            )

            assert response.status_code == 200
            # Verify create_connection was called (connection written)
            mock_db.create_connection.assert_called_once()
        finally:
            app.app.dependency_overrides.clear()
