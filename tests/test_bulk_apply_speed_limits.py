"""Tests for bulk applying default AWG speed limits (Issue #279)."""

import os
import tempfile
from unittest.mock import MagicMock, patch

from app.managers.awg_manager import AWGManager
from app.utils.helpers import hash_password
from database import Database
from dependencies import get_current_user
from tests.conftest import create_csrf_client

TEST_SECRET_KEY = "test-bulk-apply-speed-limits-12345"


def _make_manager_with_clients(clients_table):
    """Create an AWGManager with mocked _get_clients_table and _save_clients_table."""
    mock_ssh = MagicMock()
    manager = AWGManager(mock_ssh)
    manager._get_clients_table = MagicMock(return_value=clients_table)
    manager._save_clients_table = MagicMock()
    return manager


class TestBulkApplyDefaultSpeedLimits:
    """Unit tests for AWGManager.bulk_apply_default_speed_limits."""

    def test_bulk_apply_all_clients(self):
        """All 3 clients in clientsTable receive the default limits via batch."""
        clients = [
            {"clientId": "client-1", "userData": {}, "clientIp": "10.8.1.2"},
            {"clientId": "client-2", "userData": {}, "clientIp": "10.8.1.3"},
            {"clientId": "client-3", "userData": {}, "clientIp": "10.8.1.4"},
        ]
        manager = _make_manager_with_clients(clients)

        server_protocols = {
            "awg": {
                "awg_speed_limit_config": {
                    "default_speed_limit_down": 100,
                    "default_speed_limit_up": 50,
                }
            }
        }

        with patch("app.managers.awg_manager.awg_tc.reapply_all_limits") as mock_reapply:
            mock_reapply.return_value = {"status": "ok", "applied": 3, "errors": []}
            result = manager.bulk_apply_default_speed_limits("awg", server_protocols)

        assert result["status"] == "ok"
        assert result["applied"] == 3
        assert result["skipped"] == 0
        assert result["errors"] == []
        manager._get_clients_table.assert_called_once_with("awg")
        manager._save_clients_table.assert_called_once_with("awg", clients)
        mock_reapply.assert_called_once_with(
            manager.ssh,
            manager._container_name("awg"),
            manager._interface_name("awg"),
            clients,
            global_limit_down=None,
            global_limit_up=None,
        )

        # Verify each client has the new limits set in memory before save.
        for client in clients:
            assert client["userData"]["speed_limit_down"] == 100
            assert client["userData"]["speed_limit_up"] == 50

    def test_bulk_apply_no_defaults_configured(self):
        """If no default speed limits are set, return error status."""
        manager = _make_manager_with_clients([])
        server_protocols = {"awg": {"awg_speed_limit_config": {}}}

        result = manager.bulk_apply_default_speed_limits("awg", server_protocols)

        assert result["status"] == "error"
        assert "No default speed limits configured" in result["message"]

    def test_bulk_apply_partial_failure(self):
        """3 clients, reapply returns errors -> applied=3, errors populated."""
        clients = [
            {"clientId": "client-1", "userData": {}, "clientIp": "10.8.1.2"},
            {"clientId": "client-2", "userData": {}, "clientIp": "10.8.1.3"},
            {"clientId": "client-3", "userData": {}, "clientIp": "10.8.1.4"},
        ]
        manager = _make_manager_with_clients(clients)

        with patch("app.managers.awg_manager.awg_tc.reapply_all_limits") as mock_reapply:
            mock_reapply.return_value = {
                "status": "partial",
                "applied": 2,
                "errors": ["10.8.1.3: tc filter failed"],
            }
            server_protocols = {
                "awg": {
                    "awg_speed_limit_config": {
                        "default_speed_limit_down": 100,
                        "default_speed_limit_up": 50,
                    }
                }
            }
            result = manager.bulk_apply_default_speed_limits("awg", server_protocols)

        assert result["status"] == "ok"
        assert result["applied"] == 3
        assert result["skipped"] == 0
        assert result["errors"] == ["10.8.1.3: tc filter failed"]
        manager._get_clients_table.assert_called_once_with("awg")
        manager._save_clients_table.assert_called_once_with("awg", clients)

    def test_bulk_apply_zero_means_unlimited(self):
        """Default limits of 0 are normalized to None before applying."""
        clients = [
            {"clientId": "client-1", "userData": {}, "clientIp": "10.8.1.2"},
        ]
        manager = _make_manager_with_clients(clients)

        server_protocols = {
            "awg": {
                "awg_speed_limit_config": {
                    "default_speed_limit_down": 0,
                    "default_speed_limit_up": 0,
                }
            }
        }

        with patch("app.managers.awg_manager.awg_tc.reapply_all_limits") as mock_reapply:
            mock_reapply.return_value = {"status": "ok", "applied": 0, "errors": []}
            result = manager.bulk_apply_default_speed_limits("awg", server_protocols)

        assert result["status"] == "ok"
        assert result["applied"] == 1
        assert result["skipped"] == 0
        assert result["errors"] == []
        assert clients[0]["userData"]["speed_limit_down"] is None
        assert clients[0]["userData"]["speed_limit_up"] is None
        manager._save_clients_table.assert_called_once_with("awg", clients)

    def test_bulk_apply_empty_clients_table(self):
        """Empty clientsTable returns applied=0, skipped=0."""
        manager = _make_manager_with_clients([])
        server_protocols = {
            "awg": {
                "awg_speed_limit_config": {
                    "default_speed_limit_down": 100,
                    "default_speed_limit_up": 50,
                }
            }
        }

        with patch("app.managers.awg_manager.awg_tc.reapply_all_limits") as mock_reapply:
            mock_reapply.return_value = {"status": "ok", "applied": 0, "errors": []}
            result = manager.bulk_apply_default_speed_limits("awg", server_protocols)

        assert result["status"] == "ok"
        assert result["applied"] == 0
        assert result["skipped"] == 0
        assert result["errors"] == []
        mock_reapply.assert_called_once_with(
            manager.ssh,
            manager._container_name("awg"),
            manager._interface_name("awg"),
            [],
            global_limit_down=None,
            global_limit_up=None,
        )
        manager._save_clients_table.assert_called_once_with("awg", [])

    def test_bulk_apply_passes_global_limits(self):
        """Default and global speed limits are forwarded to reapply_all_limits."""
        clients = [
            {"clientId": "client-1", "userData": {}, "clientIp": "10.8.1.2"},
        ]
        manager = _make_manager_with_clients(clients)
        server_protocols = {
            "awg": {
                "awg_speed_limit_config": {
                    "default_speed_limit_down": 100,
                    "default_speed_limit_up": 50,
                    "global_speed_limit_down": 500,
                    "global_speed_limit_up": 250,
                }
            }
        }

        with patch("app.managers.awg_manager.awg_tc.reapply_all_limits") as mock_reapply:
            mock_reapply.return_value = {"status": "ok", "applied": 1, "errors": []}
            result = manager.bulk_apply_default_speed_limits("awg", server_protocols)

        assert result["status"] == "ok"
        assert result["applied"] == 1
        mock_reapply.assert_called_once_with(
            manager.ssh,
            manager._container_name("awg"),
            manager._interface_name("awg"),
            clients,
            global_limit_down=500,
            global_limit_up=250,
        )


class TestBulkApplyEndpoint:
    """Integration tests for POST /api/servers/{id}/awg/apply-default-speed-limits."""

    def setup_method(self):
        """Set up temporary database with admin and a server with AWG installed."""
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
        self.db.create_user(
            {
                "id": "user-1",
                "username": "testuser",
                "password_hash": hash_password("UserPass123"),
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "role": "user",
                "limits": {},
            }
        )
        self.db.create_server(
            {
                "name": "Test Server",
                "host": "10.0.0.1",
                "username": "root",
                "password": "***",
                "ssh_port": 22,
                "protocols": {
                    "awg": {
                        "installed": True,
                        "port": "55424",
                        "awg_speed_limit_config": {
                            "default_speed_limit_down": 100,
                            "default_speed_limit_up": 50,
                        },
                    }
                },
            }
        )
        self.server_id = self.db.get_all_servers()[0]["id"]

    def teardown_method(self):
        """Clean up temporary database."""
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_endpoint_apply_default_speed_limits(self, mock_servers_db, mock_auth_db):
        """Admin POST applies default limits and returns applied count."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        import app

        client = create_csrf_client()
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.disconnect.return_value = None

        mock_manager = MagicMock(spec=AWGManager)
        mock_manager.bulk_apply_default_speed_limits.return_value = {
            "status": "ok",
            "applied": 3,
            "skipped": 0,
            "errors": [],
        }

        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                with patch("app.routers.servers.get_protocol_manager", return_value=mock_manager):
                    resp = client.post(
                        f"/api/servers/{self.server_id}/awg/apply-default-speed-limits", json={}
                    )
                    assert resp.status_code == 200
                    data = resp.json()
                    assert data["status"] == "ok"
                    assert data["applied"] == 3
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_endpoint_no_awg_installed(self, mock_servers_db, mock_auth_db):
        """If AWG is not installed, endpoint returns 400."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        import app

        client = create_csrf_client()
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")

        # Remove awg protocol
        self.db.update_server(self.server_id, {"protocols": {}})

        try:
            resp = client.post(
                f"/api/servers/{self.server_id}/awg/apply-default-speed-limits", json={}
            )
            assert resp.status_code == 400
            assert "AWG protocol is not installed" in resp.json()["error"]
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_endpoint_server_not_found(self, mock_servers_db, mock_auth_db):
        """Request to nonexistent server returns 404."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        import app

        client = create_csrf_client()
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")

        try:
            resp = client.post("/api/servers/999999/awg/apply-default-speed-limits", json={})
            assert resp.status_code == 404
            assert "Server not found" in resp.json()["error"]
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_endpoint_requires_admin(self, mock_servers_db, mock_auth_db):
        """Non-admin user gets 403."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        import app

        client = create_csrf_client()
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("user-1")

        try:
            resp = client.post(
                f"/api/servers/{self.server_id}/awg/apply-default-speed-limits", json={}
            )
            assert resp.status_code == 403
            assert "Admin access required" in resp.json()["detail"]
        finally:
            app.app.dependency_overrides.clear()
