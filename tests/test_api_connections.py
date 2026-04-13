"""
Tests for API connection endpoints
"""

import json
import pytest
from unittest.mock import MagicMock, patch
from fastapi.testclient import TestClient
from app import app


class TestApiMyAddConnection:
    """Tests for /api/my/connections/add endpoint"""

    def setup_method(self):
        """Set up test client and mock data"""
        self.client = TestClient(app)
        # Mock the load_data and save_data functions
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
                    "protocols": {"awg": {"installed": True, "port": "55424"}},
                }
            ],
            "user_connections": [],
            "connection_creation_log": [],
            "settings": {
                "limits": {
                    "max_connections_per_user": 10,
                    "connection_rate_limit_count": 5,
                    "connection_rate_limit_window": 60,
                }
            },
        }

    @patch("app.load_data")
    @patch("app.save_data")
    @patch("app.get_current_user")
    @patch("app.get_ssh")
    @patch("app.get_protocol_manager")
    def test_duplicate_connection_name_returns_json_error(
        self,
        mock_get_protocol_manager,
        mock_get_ssh,
        mock_get_current_user,
        mock_save_data,
        mock_load_data,
    ):
        """Test that duplicate connection names return proper JSON error"""
        # Setup mocks
        mock_load_data.return_value = self.mock_data.copy()
        mock_get_current_user.return_value = self.mock_data["users"][0]

        # Mock SSH and protocol manager
        mock_ssh = MagicMock()
        mock_get_ssh.return_value = mock_ssh
        mock_manager = MagicMock()
        mock_manager.add_client.return_value = {"client_id": "test-client-1"}
        mock_get_protocol_manager.return_value = mock_manager

        # First connection should succeed
        response1 = self.client.post(
            "/api/my/connections/add",
            json={"server_id": 0, "protocol": "awg", "name": "Test Connection"},
            headers={"Authorization": "Bearer test-token"},
        )

        # Update mock data to include the first connection
        self.mock_data["user_connections"].append(
            {
                "id": "conn-1",
                "user_id": "test-user-1",
                "server_id": 0,
                "protocol": "awg",
                "client_id": "test-client-1",
                "name": "Test Connection",
                "created_at": "2024-01-01T00:00:00",
            }
        )
        mock_load_data.return_value = self.mock_data.copy()

        # Second connection with duplicate name should fail with JSON error
        response2 = self.client.post(
            "/api/my/connections/add",
            json={"server_id": 0, "protocol": "awg", "name": "Test Connection"},
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
        except json.JSONDecodeError:
            pytest.fail("Response is not valid JSON")

    @patch("app.load_data")
    @patch("app.save_data")
    @patch("app.get_current_user")
    def test_duplicate_connection_error_message_format(
        self, mock_get_current_user, mock_save_data, mock_load_data
    ):
        """Test that the duplicate connection error message is user-friendly"""
        # Setup mocks
        mock_load_data.return_value = self.mock_data.copy()
        mock_get_current_user.return_value = self.mock_data["users"][0]

        # Add a connection to the mock data
        self.mock_data["user_connections"].append(
            {
                "id": "conn-1",
                "user_id": "test-user-1",
                "server_id": 0,
                "protocol": "awg",
                "client_id": "test-client-1",
                "name": "Existing Connection",
                "created_at": "2024-01-01T00:00:00",
            }
        )

        # Try to create a connection with duplicate name
        response = self.client.post(
            "/api/my/connections/add",
            json={"server_id": 0, "protocol": "awg", "name": "Existing Connection"},
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

    @patch("app.load_data")
    @patch("app.get_current_user")
    def test_rate_limit_returns_json_error(self, mock_get_current_user, mock_load_data):
        """Test that rate limit errors return proper JSON response with retry_after"""
        # Setup mocks
        mock_load_data.return_value = self.mock_data.copy()
        mock_get_current_user.return_value = self.mock_data["users"][0]

        # Simulate rate limiting by adding many recent connection creations
        from datetime import datetime, timedelta

        now = datetime.now()
        recent_entries = []
        for i in range(5):  # Same as rate_limit_count
            recent_entries.append(
                {
                    "user_id": "test-user-1",
                    "timestamp": (now - timedelta(seconds=i * 10)).isoformat(),
                }
            )

        self.mock_data["connection_creation_log"] = recent_entries
        mock_load_data.return_value = self.mock_data.copy()

        # Try to create a connection (should be rate limited)
        response = self.client.post(
            "/api/my/connections/add",
            json={"server_id": 0, "protocol": "awg", "name": "Test Connection"},
            headers={"Authorization": "Bearer test-token"},
        )

        # Verify response
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


class TestApiAddConnectionTelemtFailure:
    """Tests for telemt API failure handling in /api/servers/{server_id}/connections/add"""

    def setup_method(self):
        self.client = TestClient(app)
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

    @patch("app.load_data")
    @patch("app.save_data")
    @patch("app._check_admin")
    @patch("app.get_ssh")
    @patch("app.get_protocol_manager")
    def test_telemt_api_failure_returns_500_no_data_written(
        self,
        mock_get_protocol_manager,
        mock_get_ssh,
        mock_check_admin,
        mock_save_data,
        mock_load_data,
    ):
        """When telemt API fails, return 500 and do NOT write to data.json."""
        mock_check_admin.return_value = True
        mock_load_data.return_value = self.mock_data.copy()

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

        response = self.client.post(
            "/api/servers/0/connections/add",
            json={"protocol": "telemt", "name": "Test Connection"},
            headers={"Authorization": "Bearer test-token"},
        )

        assert response.status_code == 500
        data = response.json()
        assert "error" in data
        assert data["error"] == "User already exists"
        # Verify save_data was never called (no connection written)
        mock_save_data.assert_not_called()

    @patch("app.load_data")
    @patch("app.save_data")
    @patch("app._check_admin")
    @patch("app.get_ssh")
    @patch("app.get_protocol_manager")
    def test_telemt_success_writes_connection(
        self,
        mock_get_protocol_manager,
        mock_get_ssh,
        mock_check_admin,
        mock_save_data,
        mock_load_data,
    ):
        """When telemt API succeeds, connection is written to data.json."""
        mock_check_admin.return_value = True
        mock_load_data.return_value = self.mock_data.copy()

        mock_ssh = MagicMock()
        mock_get_ssh.return_value = mock_ssh
        mock_manager = MagicMock()
        mock_manager.add_client.return_value = {
            "client_id": "test_user",
            "config": "tg://proxy?server=test.example.com&port=443&secret=abc123",
            "vpn_link": "tg://proxy?server=test.example.com&port=443&secret=abc123",
        }
        mock_get_protocol_manager.return_value = mock_manager

        response = self.client.post(
            "/api/servers/0/connections/add",
            json={"protocol": "telemt", "name": "Test Connection", "user_id": "test-user-1"},
            headers={"Authorization": "Bearer test-token"},
        )

        assert response.status_code == 200
        # Verify save_data was called (connection written)
        mock_save_data.assert_called_once()
