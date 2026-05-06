"""Tests for POST /api/servers/{server_id}/stats endpoint — combined SSH round-trips."""

import os
import tempfile
from unittest.mock import MagicMock, patch

from app.utils.helpers import hash_password
from database import Database
from dependencies import get_current_user
from tests.conftest import create_csrf_client

TEST_SECRET_KEY = "test-stats-s...tes!"


class TestParseCombinedStats:
    """Unit tests for _parse_combined_stats parser."""

    def setup_method(self):
        from app.routers.servers import _parse_combined_stats

        self.parse = _parse_combined_stats

    def test_all_sections_present(self):
        raw = (
            "===CPU===\n42.5\n===RAM===\n123456 789012\n"
            "===DISK===\n111222 333444\n"
            "===NET===\n555 666\n===UPTIME===\nup 3 days"
        )
        result = self.parse(raw)
        assert result["CPU"] == "42.5"
        assert result["RAM"] == "123456 789012"
        assert result["DISK"] == "111222 333444"
        assert result["NET"] == "555 666"
        assert result["UPTIME"] == "up 3 days"

    def test_empty_output_returns_empty_strings(self):
        raw = "===CPU===\n===RAM===\n===DISK===\n===NET===\n===UPTIME==="
        result = self.parse(raw)
        assert result["CPU"] == ""
        assert result["RAM"] == ""
        assert result["DISK"] == ""
        assert result["NET"] == ""
        assert result["UPTIME"] == ""

    def test_partial_sections_still_returned(self):
        raw = "===CPU===\n12.3\n===RAM===\n111 222"
        result = self.parse(raw)
        assert result["CPU"] == "12.3"
        assert result["RAM"] == "111 222"
        assert "DISK" not in result
        assert "NET" not in result
        assert "UPTIME" not in result

    def test_no_sections_returns_empty_dict(self):
        result = self.parse("garbage with no sections")
        assert result == {}

    def test_whitespace_around_values_stripped(self):
        raw = "===CPU===\n  8.9  \n===RAM===\n  100 200  \n"
        result = self.parse(raw)
        assert result["CPU"] == "8.9"
        assert result["RAM"] == "100 200"

    def test_multiline_uptime_value(self):
        raw = "===UPTIME===\nup 3 days, 2 hours, 15 minutes\n===CPU===\n5.0"
        result = self.parse(raw)
        assert "up 3 days, 2 hours, 15 minutes" in result["UPTIME"]
        assert result["CPU"] == "5.0"


class TestApiServerStats:
    """Integration tests for POST /api/servers/{server_id}/stats endpoint."""

    def setup_method(self):
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
        self.db.create_server(
            {
                "name": "Test Server",
                "host": "10.0.0.1",
                "username": "root",
                "password": "***",
                "ssh_port": 22,
                "protocols": {},
            }
        )

    def teardown_method(self):
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_combined_command_produces_expected_response(self, mock_servers_db, mock_auth_db):
        """One SSH run_command call produces the full stats dict."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.run_command.return_value = (
            "===CPU===\n42.5\n===RAM===\n123456000 789012000\n"
            "===DISK===\n111222000 333444000\n"
            "===NET===\n555 666\n===UPTIME===\nup 3 days",
            "",
            0,
        )
        mock_ssh.disconnect.return_value = None

        import app

        client = create_csrf_client()

        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        assert login_resp.status_code == 200
        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                server_id = self.db.get_all_servers()[0]["id"]
                resp = client.post(f"/api/servers/{server_id}/stats")
                assert resp.status_code == 200
                data = resp.json()

                assert data["cpu"] == 42.5
                assert data["ram_used"] == 123456000
                assert data["ram_total"] == 789012000
                assert data["disk_used"] == 111222000
                assert data["disk_total"] == 333444000
                assert data["net_rx"] == 555
                assert data["net_tx"] == 666
                assert "up 3 days" in data["uptime"]

                # Verify exactly one run_command call was made
                assert mock_ssh.run_command.call_count == 1
                # Verify the combined command contains all section markers
                cmd = mock_ssh.run_command.call_args[0][0]
                assert "===CPU===" in cmd
                assert "===RAM===" in cmd
                assert "===DISK===" in cmd
                assert "===NET===" in cmd
                assert "===UPTIME===" in cmd
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_graceful_fallback_on_garbled_output(self, mock_servers_db, mock_auth_db):
        """When SSH output is garbled, all stats default to 0 or empty."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.run_command.return_value = ("", "", 0)
        mock_ssh.disconnect.return_value = None

        import app

        client = create_csrf_client()

        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                server_id = self.db.get_all_servers()[0]["id"]
                resp = client.post(f"/api/servers/{server_id}/stats")
                assert resp.status_code == 200
                data = resp.json()

                assert data["cpu"] == 0
                assert data["ram_used"] == 0
                assert data["ram_total"] == 0
                assert data["ram_percent"] == 0
                assert data["disk_used"] == 0
                assert data["disk_total"] == 0
                assert data["disk_percent"] == 0
                assert data["net_rx"] == 0
                assert data["net_tx"] == 0
                assert data["uptime"] == ""
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_server_not_found_returns_404(self, mock_servers_db, mock_auth_db):
        """Non-existent server returns 404."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        import app

        client = create_csrf_client()

        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            resp = client.post("/api/servers/99999/stats")
            assert resp.status_code == 404
            data = resp.json()
            assert "error" in data
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_ram_percent_and_disk_percent_computed(self, mock_servers_db, mock_auth_db):
        """ram_percent and disk_percent are computed correctly."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.run_command.return_value = (
            "===CPU===\n10.0\n===RAM===\n250000000 1000000000\n"
            "===DISK===\n30000000000 100000000000\n"
            "===NET===\n100 200\n===UPTIME===\nup 1 day",
            "",
            0,
        )
        mock_ssh.disconnect.return_value = None

        import app

        client = create_csrf_client()

        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        try:
            with patch("app.routers.servers.get_ssh", return_value=mock_ssh):
                server_id = self.db.get_all_servers()[0]["id"]
                resp = client.post(f"/api/servers/{server_id}/stats")
                assert resp.status_code == 200
                data = resp.json()

                assert data["ram_percent"] == 25.0
                assert data["disk_percent"] == 30.0
        finally:
            app.app.dependency_overrides.clear()
