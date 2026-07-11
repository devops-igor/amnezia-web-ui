"""Tests for stale connection cleanup when protocols are removed."""

import os
import tempfile
from unittest.mock import MagicMock, patch

import paramiko

from app.services.startup_reconciliation import cleanup_stale_protocols
from app.utils.helpers import hash_password
from database import Database
from dependencies import get_current_user
from tests.conftest import create_csrf_client

TEST_SECRET_KEY = "test-stale-connections-secret-key-12345"


class TestStaleConnectionsCleanup:
    """Tests for stale user_connections cleanup across all three code paths."""

    def setup_method(self):
        """Set up temporary database with admin, user, server, and connections."""
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
                "protocols": {"awg": {"installed": True, "port": "55424"}},
            }
        )
        self.server_id = self.db.get_all_servers()[0]["id"]

    def teardown_method(self):
        """Clean up temporary database."""
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    def _create_connections(self, protocols):
        """Create one connection per protocol for the test server."""
        for idx, proto in enumerate(protocols):
            self.db.create_connection(
                {
                    "id": f"conn-{idx}",
                    "user_id": "user-1",
                    "server_id": self.server_id,
                    "protocol": proto,
                    "client_id": f"client-{idx}",
                    "name": f"Connection {idx}",
                    "created_at": f"2024-01-0{idx + 1}T00:00:00",
                }
            )

    def test_db_delete_connections_by_server_and_protocol(self):
        """DB method deletes only matching (server_id, protocol) rows."""
        self._create_connections(["awg", "xray", "telemt"])

        deleted = self.db.delete_connections_by_server_and_protocol(self.server_id, "xray")
        assert deleted == 1

        remaining = self.db.get_all_connections()
        protocols = {conn["protocol"] for conn in remaining}
        assert protocols == {"awg", "telemt"}

    def test_db_delete_connections_by_server_and_protocol_no_match(self):
        """DB method returns 0 when no connections match."""
        self._create_connections(["awg"])

        deleted = self.db.delete_connections_by_server_and_protocol(self.server_id, "xray")
        assert deleted == 0

        remaining = self.db.get_all_connections()
        assert len(remaining) == 1
        assert remaining[0]["protocol"] == "awg"

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_api_check_server_deletes_stale_protocol_connections(
        self, mock_servers_db, mock_auth_db
    ):
        """checkServer removes stale protocol from DB and deletes its connections."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db
        self._create_connections(["awg", "xray"])

        import app

        client = create_csrf_client()
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.disconnect.return_value = None

        mock_manager = MagicMock()
        mock_manager.check_docker_installed.return_value = True

        def mock_get_server_status(proto):
            return {"container_exists": proto == "xray", "port": "443"}

        mock_manager.get_server_status.side_effect = mock_get_server_status

        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                with patch("app.routers.servers.get_protocol_manager", return_value=mock_manager):
                    resp = client.post(f"/api/servers/{self.server_id}/check")

                    assert resp.status_code == 200
                    data = resp.json()
                    assert data["protocols"]["awg"].get("container_exists") is False
                    assert data["protocols"]["xray"].get("container_exists") is True

                    server = self.db.get_server_by_id(self.server_id)
                    assert server is not None
                    assert "awg" not in server["protocols"]
                    assert "xray" in server["protocols"]

                    remaining = self.db.get_all_connections()
                    assert len(remaining) == 1
                    assert remaining[0]["protocol"] == "xray"
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_api_uninstall_protocol_deletes_connections(self, mock_servers_db, mock_auth_db):
        """Uninstalling a protocol deletes its user_connections."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db
        self._create_connections(["awg", "xray"])

        # Ensure awg is present in server protocols so uninstall proceeds
        self.db.update_server(self.server_id, {"protocols": {"awg": {"installed": True}}})

        import app

        client = create_csrf_client()
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.disconnect.return_value = None

        mock_manager = MagicMock()
        mock_manager.remove_container.return_value = None

        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                with patch("app.routers.servers.get_protocol_manager", return_value=mock_manager):
                    resp = client.post(
                        f"/api/servers/{self.server_id}/uninstall",
                        json={"protocol": "awg"},
                    )

                    assert resp.status_code == 200
                    assert resp.json() == {"status": "success"}

                    server = self.db.get_server_by_id(self.server_id)
                    assert server is not None
                    assert "awg" not in server["protocols"]

                    remaining = self.db.get_all_connections()
                    assert len(remaining) == 1
                    assert remaining[0]["protocol"] == "xray"
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.services.startup_reconciliation.get_db")
    @patch("app.services.startup_reconciliation.get_ssh")
    @patch("app.services.startup_reconciliation.get_protocol_manager")
    def test_cleanup_stale_protocols_removes_stale(self, mock_get_pm, mock_get_ssh, mock_get_db):
        """Startup reconciliation deletes stale protocol connections and updates DB."""
        mock_get_db.return_value = self.db
        self.db.update_server(
            self.server_id, {"protocols": {"awg": {"installed": True}, "xray": {"installed": True}}}
        )
        self._create_connections(["awg", "xray"])

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.disconnect.return_value = None
        mock_get_ssh.return_value = mock_ssh

        def fake_manager(ssh, proto):
            mgr = MagicMock()
            if proto == "awg":
                mgr.check_protocol_installed.side_effect = lambda _pt: False
            else:
                mgr.check_protocol_installed.side_effect = lambda: True
            return mgr

        mock_get_pm.side_effect = fake_manager

        cleanup_stale_protocols()

        server = self.db.get_server_by_id(self.server_id)
        assert "awg" not in server["protocols"]
        assert "xray" in server["protocols"]

        remaining = self.db.get_all_connections()
        assert len(remaining) == 1
        assert remaining[0]["protocol"] == "xray"

    @patch("app.services.startup_reconciliation.get_db")
    @patch("app.services.startup_reconciliation.get_ssh")
    @patch("app.services.startup_reconciliation.get_protocol_manager")
    def test_cleanup_stale_protocols_healthy_server_unchanged(
        self, mock_get_pm, mock_get_ssh, mock_get_db
    ):
        """Healthy protocols are left untouched."""
        mock_get_db.return_value = self.db
        self._create_connections(["awg"])

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.disconnect.return_value = None
        mock_get_ssh.return_value = mock_ssh

        mock_manager = MagicMock()
        mock_manager.check_protocol_installed.return_value = True
        mock_get_pm.return_value = mock_manager

        cleanup_stale_protocols()

        server = self.db.get_server_by_id(self.server_id)
        assert "awg" in server["protocols"]
        assert len(self.db.get_all_connections()) == 1

    @patch("app.services.startup_reconciliation.get_db")
    @patch("app.services.startup_reconciliation.get_ssh")
    @patch("app.services.startup_reconciliation.get_protocol_manager")
    def test_cleanup_stale_protocols_unreachable_server_skipped(
        self, mock_get_pm, mock_get_ssh, mock_get_db
    ):
        """Unreachable server is skipped and other servers are still processed."""
        mock_get_db.return_value = self.db
        self._create_connections(["awg"])

        # Add a second server with a stale protocol
        self.db.create_server(
            {
                "name": "Other Server",
                "host": "10.0.0.2",
                "username": "root",
                "password": "***",
                "ssh_port": 22,
                "protocols": {"xray": {"installed": True}},
            }
        )
        other_id = self.db.get_all_servers()[-1]["id"]
        self.db.create_connection(
            {
                "id": "conn-other",
                "user_id": "user-1",
                "server_id": other_id,
                "protocol": "xray",
                "client_id": "client-other",
                "name": "Other connection",
                "created_at": "2024-01-01T00:00:00",
            }
        )

        def fake_ssh(server, db=None):
            ssh = MagicMock()
            if server["id"] == self.server_id:
                ssh.connect.side_effect = paramiko.SSHException("Connection refused")
            else:
                ssh.connect.return_value = None
                ssh.disconnect.return_value = None
            return ssh

        mock_get_ssh.side_effect = fake_ssh

        def fake_manager(ssh, proto):
            mgr = MagicMock()
            if proto == "awg":
                mgr.check_protocol_installed.side_effect = lambda _pt: False
            else:
                mgr.check_protocol_installed.side_effect = lambda: False
            return mgr

        mock_get_pm.side_effect = fake_manager

        cleanup_stale_protocols()

        # First server unreachable, so awg connection remains and protocol untouched
        first = self.db.get_server_by_id(self.server_id)
        assert first is not None
        assert "awg" in first["protocols"]
        assert len(self.db.get_connections_by_server_and_protocol(self.server_id, "awg")) == 1

        # Second server processed successfully
        other = self.db.get_server_by_id(other_id)
        assert other is not None
        assert "xray" not in other["protocols"]
        assert len(self.db.get_connections_by_server_and_protocol(other_id, "xray")) == 0

    @patch("app.services.startup_reconciliation.get_db")
    @patch("app.services.startup_reconciliation.get_ssh")
    def test_cleanup_stale_protocols_no_protocols_skipped(self, mock_get_ssh, mock_get_db):
        """Servers with no protocols are skipped without SSH."""
        mock_get_db.return_value = self.db
        self.db.update_server(self.server_id, {"protocols": {}})

        cleanup_stale_protocols()

        mock_get_ssh.assert_not_called()

    @patch("app.services.startup_reconciliation.get_db")
    @patch("app.services.startup_reconciliation.get_ssh")
    @patch("app.services.startup_reconciliation.get_protocol_manager")
    def test_cleanup_stale_protocols_dns_uses_status(self, mock_get_pm, mock_get_ssh, mock_get_db):
        """DNS protocol uses get_server_status for existence check."""
        mock_get_db.return_value = self.db
        self.db.update_server(self.server_id, {"protocols": {"dns": {"installed": True}}})
        self._create_connections(["dns"])

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.disconnect.return_value = None
        mock_get_ssh.return_value = mock_ssh

        mock_manager = MagicMock()
        mock_manager.get_server_status.return_value = {"container_exists": False}
        mock_get_pm.return_value = mock_manager

        cleanup_stale_protocols()

        server = self.db.get_server_by_id(self.server_id)
        assert server is not None
        assert "dns" not in server["protocols"]
        assert len(self.db.get_all_connections()) == 0
        mock_manager.get_server_status.assert_called_once_with("dns")
