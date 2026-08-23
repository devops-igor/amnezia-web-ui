"""Tests for Issue #357 — User Health Checks and IP Sanitization.

Verifies that:
1. compute_simplified_server_health computes online, degraded, offline, pending statuses correctly.
2. sanitize_server_for_user strictly hides IP, hostname, SSH credentials, and sensitive fields.
3. GET /my page renders simplified network health widget, status badges, and NO server host/IP.
4. GET /api/my/connections enriches connections with server_name and server_status/server_reachable.
5. POST /api/my/connections/add enriches new connections with sanitized server_name and health status.
6. GET /api/servers/ and GET /api/servers/{id}/reachability sanitize responses for role 'user'.
7. Translations exist across en, ru, fr, fa, zh for all status keys.
"""

import json
import os
import tempfile
from unittest.mock import MagicMock, patch

from app.core.database import Database
from app.core.dependencies import get_current_user
from app.utils.helpers import (
    compute_simplified_server_health,
    sanitize_server_for_user,
)
from tests.conftest import create_csrf_client


class TestSimplifiedHealthLogic:
    """Unit tests for simplified server health calculation and server sanitization."""

    def test_health_pending_when_reach_info_is_none(self):
        status, latency_ms, last_checked, reachable = compute_simplified_server_health(None)
        assert status == "pending"
        assert latency_ms is None
        assert last_checked is None
        assert reachable is None

    def test_health_offline_when_unreachable(self):
        reach_info = {
            "reachable": False,
            "latency_ms": 0,
            "last_checked": "2026-08-23T12:00:00",
            "error": "Connection timed out",
        }
        status, latency_ms, last_checked, reachable = compute_simplified_server_health(reach_info)
        assert status == "offline"
        assert latency_ms is None
        assert last_checked == "2026-08-23T12:00:00"
        assert reachable is False

    def test_health_online_when_reachable_no_auto_trials(self):
        reach_info = {
            "reachable": True,
            "latency_ms": 24,
            "last_checked": "2026-08-23T12:00:00",
        }
        status, latency_ms, last_checked, reachable = compute_simplified_server_health(reach_info)
        assert status == "online"
        assert latency_ms == 24
        assert last_checked == "2026-08-23T12:00:00"
        assert reachable is True

    def test_health_online_when_all_auto_trials_reachable(self):
        reach_info = {
            "reachable": True,
            "latency_ms": 25,
            "last_checked": "2026-08-23T12:00:00",
            "auto_trials": {
                "tls": {"reachable": True, "latency_ms": 20},
                "quic": {"reachable": True, "latency_ms": 22},
                "dns": {"reachable": True, "latency_ms": 21},
                "sip": {"reachable": True, "latency_ms": 23},
            },
        }
        status, latency_ms, last_checked, reachable = compute_simplified_server_health(reach_info)
        assert status == "online"
        assert latency_ms == 25
        assert reachable is True

    def test_health_degraded_when_some_auto_trials_fail(self):
        reach_info = {
            "reachable": True,
            "latency_ms": 30,
            "last_checked": "2026-08-23T12:00:00",
            "auto_trials": {
                "tls": {"reachable": True, "latency_ms": 20},
                "quic": {"reachable": False, "error": "Handshake timeout"},
                "dns": {"reachable": True, "latency_ms": 21},
                "sip": {"reachable": True, "latency_ms": 23},
            },
        }
        status, latency_ms, last_checked, reachable = compute_simplified_server_health(reach_info)
        assert status == "degraded"
        assert latency_ms == 30
        assert reachable is True

    def test_sanitize_server_for_user_strips_sensitive_data(self):
        raw_server = {
            "id": 42,
            "name": "Production Server",
            "host": "198.51.100.25",
            "ssh_port": 2222,
            "username": "root",
            "password": "supersecretpassword",
            "private_key": "-----BEGIN RSA PRIVATE KEY-----\nMIIE...",
            "protocols": {
                "awg": {"installed": True, "port": "55424", "server_privkey": "privkey123"},
                "xray": {"installed": False, "port": "443"},
            },
        }
        reach_info = {"reachable": True, "latency_ms": 15, "last_checked": "2026-08-23T12:00:00"}
        sanitized = sanitize_server_for_user(raw_server, reach_info)

        assert sanitized["id"] == 42
        assert sanitized["name"] == "Production Server"
        assert sanitized["status"] == "online"
        assert sanitized["latency_ms"] == 15
        assert sanitized["reachable"] is True

        # Sensitive keys MUST NOT exist
        assert "host" not in sanitized
        assert "ssh_port" not in sanitized
        assert "username" not in sanitized
        assert "password" not in sanitized
        assert "private_key" not in sanitized

        # Protocols must only have installed status
        assert sanitized["protocols"] == {
            "awg": {"installed": True},
            "xray": {"installed": False},
        }

    def test_sanitize_server_for_user_fallback_name(self):
        raw_server = {
            "id": 7,
            "name": "",
            "host": "203.0.113.10",
            "protocols": {},
        }
        sanitized = sanitize_server_for_user(raw_server, None)
        assert sanitized["name"] == "Server #7"
        assert sanitized["status"] == "pending"

    def test_sanitize_server_for_user_raw_protocol_values(self):
        raw_server = {
            "id": 9,
            "name": None,
            "host": "203.0.113.12",
            "protocols": {
                "awg": True,
                "xray": False,
            },
        }
        sanitized = sanitize_server_for_user(raw_server, None)
        assert sanitized["name"] == "Server #9"
        assert sanitized["protocols"] == {
            "awg": {"installed": True},
            "xray": {"installed": False},
        }

    def test_sanitize_server_for_user_none_protocols(self):
        raw_server = {
            "id": 11,
            "name": "   ",
            "host": "203.0.113.15",
            "protocols": None,
        }
        sanitized = sanitize_server_for_user(raw_server, None)
        assert sanitized["name"] == "Server #11"
        assert sanitized["protocols"] == {}


TEST_SECRET_KEY = "test-secret-key-for-health-checks-32b"


class TestMyConnectionsHealthAndPrivacy:
    """Integration tests for /my and user endpoints verifying health visibility and IP safety."""

    def setup_method(self):
        """Set up test database with a user, servers, and connections."""
        os.environ["SECRET_KEY"] = TEST_SECRET_KEY
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        self.db = Database(self.tmp_db_path, secret_key=TEST_SECRET_KEY)

        self.db.create_user(
            {
                "id": "user-100",
                "username": "alice",
                "password_hash": "hashed",
                "role": "user",
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "limits": {},
            }
        )

        self.db.create_user(
            {
                "id": "admin-1",
                "username": "admin",
                "password_hash": "hashed",
                "role": "admin",
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "limits": {},
            }
        )

        self.db.create_server(
            {
                "name": "Stockholm Node",
                "host": "198.51.100.77",
                "ssh_port": 2200,
                "username": "root",
                "password": "secretpassword",
                "private_key": "privatekeycontent",
                "protocols": {"awg": {"installed": True, "port": "55424"}},
            }
        )
        self.db.create_server(
            {
                "name": "",
                "host": "198.51.100.88",
                "ssh_port": 22,
                "username": "root",
                "protocols": {"xray": {"installed": True, "port": "443"}},
            }
        )

        servers = self.db.get_all_servers()
        self.server1_id = servers[0]["id"]
        self.server2_id = servers[1]["id"]

        self.db.create_connection(
            {
                "id": "conn-1",
                "user_id": "user-100",
                "server_id": self.server1_id,
                "protocol": "awg",
                "client_id": "client-1",
                "name": "Alice Stockholm",
                "awg_mimicry": "tls",
                "created_at": "2026-08-23T10:00:00",
                "traffic_total_rx": 1000,
                "traffic_total_tx": 2000,
                "traffic_total": 3000,
            }
        )

    def teardown_method(self):
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    @patch("app.routers.pages.get_db")
    @patch(
        "app.services.background_orchestrator.BackgroundTaskOrchestrator.get_cached_server_reachability"
    )
    def test_my_page_renders_health_widget_and_no_ips(self, mock_reach, mock_get_db):
        """GET /my must render simplified health badges and NEVER leak server IP or host."""
        import app

        mock_get_db.return_value = self.db
        mock_reach.return_value = {
            self.server1_id: {
                "reachable": True,
                "latency_ms": 18,
                "last_checked": "2026-08-23T12:00:00",
            },
            self.server2_id: {
                "reachable": False,
                "latency_ms": 0,
                "last_checked": "2026-08-23T12:00:00",
            },
        }
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("user-100")
        try:
            client = create_csrf_client()
            response = client.get("/my")
            assert response.status_code == 200
            html = response.text

            # Health widgets and badges must be present
            assert "Stockholm Node" in html
            assert f"Server #{self.server2_id}" in html
            assert "Online" in html or "18ms" in html
            assert "Unavailable" in html

            # Network Health widget must be positioned above My Connections section
            health_pos = html.find("Stockholm Node")
            header_pos = html.find("section-title")
            connections_pos = html.find("myConnectionsList")
            assert health_pos != -1
            assert header_pos != -1
            assert connections_pos != -1
            assert health_pos < header_pos
            assert health_pos < connections_pos

            # Sensitive server IPs and hostnames must NEVER appear in regular user HTML
            assert "198.51.100.77" not in html
            assert "198.51.100.88" not in html
            assert "secretpassword" not in html
            assert "privatekeycontent" not in html
            assert "2200" not in html  # custom ssh port hidden
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.connections.get_db")
    @patch(
        "app.services.background_orchestrator.BackgroundTaskOrchestrator.get_cached_server_reachability"
    )
    def test_api_my_connections_enriches_health_and_no_ips(self, mock_reach, mock_get_db):
        """GET /api/my/connections must return server_name and health status without IPs."""
        import app

        mock_get_db.return_value = self.db
        mock_reach.return_value = {
            self.server1_id: {
                "reachable": True,
                "latency_ms": 22,
                "last_checked": "2026-08-23T12:00:00",
            }
        }
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("user-100")
        try:
            client = create_csrf_client()
            response = client.get("/api/my/connections")
            assert response.status_code == 200
            data = response.json()

            assert "connections" in data
            assert len(data["connections"]) == 1
            conn = data["connections"][0]

            assert conn["server_name"] == "Stockholm Node"
            assert conn["server_status"] == "online"
            assert conn["server_reachable"] is True

            # Assert no IP in entire JSON
            raw_json = json.dumps(data)
            assert "198.51.100.77" not in raw_json
            assert "198.51.100.88" not in raw_json
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.connections.get_ssh")
    @patch("app.routers.connections.get_protocol_manager")
    @patch("app.routers.connections.get_db")
    @patch(
        "app.services.background_orchestrator.BackgroundTaskOrchestrator.get_cached_server_reachability"
    )
    def test_api_my_add_connection_enriches_health(
        self, mock_reach, mock_get_db, mock_pm, mock_ssh
    ):
        """POST /api/my/connections/add must enrich response with sanitized server_name and health."""
        import app

        mock_get_db.return_value = self.db
        mock_reach.return_value = {
            self.server1_id: {
                "reachable": True,
                "latency_ms": 19,
                "last_checked": "2026-08-23T12:00:00",
            }
        }

        # Mock SSH & Manager
        mock_ssh_inst = MagicMock()
        mock_ssh.return_value = mock_ssh_inst
        mock_mgr_inst = MagicMock()
        mock_mgr_inst.add_client.return_value = {
            "client_id": "new-client-456",
            "config": "[Interface]\nPrivateKey = priv\n[Peer]\nPublicKey = pub",
        }
        mock_pm.return_value = mock_mgr_inst

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("user-100")
        try:
            client = create_csrf_client()
            response = client.post(
                "/api/my/connections/add",
                json={
                    "server_id": self.server1_id,
                    "protocol": "awg",
                    "name": "New Laptop Conn",
                },
            )
            assert response.status_code == 200
            data = response.json()
            assert data["status"] == "success"
            conn = data["connection"]
            assert conn["server_name"] == "Stockholm Node"
            assert conn["server_status"] == "online"
            assert conn["server_reachable"] is True
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.servers.get_db")
    @patch(
        "app.services.background_orchestrator.BackgroundTaskOrchestrator.get_cached_server_reachability"
    )
    def test_api_servers_list_sanitized_for_user_role(self, mock_reach, mock_get_db):
        """GET /api/servers must strip host/ssh_port/username when called by role 'user'."""
        import app

        mock_get_db.return_value = self.db
        mock_reach.return_value = {
            self.server1_id: {"reachable": True, "latency_ms": 20},
        }

        # Regular user
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("user-100")
        try:
            client = create_csrf_client()
            response = client.get("/api/servers")
            assert response.status_code == 200
            data = response.json()

            for srv in data:
                assert srv["host"] == ""
                assert srv["ssh_port"] == 0
                assert srv["username"] == ""
                assert "password" not in srv
                assert "private_key" not in srv

            assert data[0]["name"] == "Stockholm Node"
            assert data[1]["name"] == f"Server #{self.server2_id}"
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.servers.get_db")
    @patch(
        "app.services.background_orchestrator.BackgroundTaskOrchestrator.get_cached_server_reachability"
    )
    def test_api_server_reachability_sanitized_for_user_role(self, mock_reach, mock_get_db):
        """GET /api/servers/{id}/reachability returns simplified health without IP for regular user."""
        import app

        mock_get_db.return_value = self.db
        mock_reach.return_value = {
            self.server1_id: {
                "reachable": True,
                "latency_ms": 20,
                "last_checked": "2026-08-23T12:00:00",
                "host": "198.51.100.77",
                "port": 55424,
            },
        }

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("user-100")
        try:
            client = create_csrf_client()
            response = client.get(f"/api/servers/{self.server1_id}/reachability")
            assert response.status_code == 200
            data = response.json()
            assert data["status"] == "success"
            reach = data["reachability"]
            assert reach["status"] == "online"
            assert reach["latency_ms"] == 20
            assert reach["last_checked"] == "2026-08-23T12:00:00"
            assert reach["reachable"] is True

            raw = json.dumps(data)
            assert "198.51.100.77" not in raw
            assert "55424" not in raw
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.connections.get_db")
    @patch(
        "app.services.background_orchestrator.BackgroundTaskOrchestrator.get_cached_server_reachability"
    )
    def test_my_connections_with_unknown_server(self, mock_reach, mock_get_db):
        """GET /api/my/connections handles deleted/unknown servers gracefully without IP leak."""
        import app

        self.db.create_connection(
            {
                "id": "conn-orphan",
                "user_id": "user-100",
                "server_id": 9999,
                "protocol": "awg",
                "client_id": "client-9999",
                "name": "Orphan Conn",
            }
        )

        mock_get_db.return_value = self.db
        mock_reach.return_value = {}

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("user-100")
        try:
            client = create_csrf_client()
            response = client.get("/api/my/connections")
            assert response.status_code == 200
            data = response.json()
            orphan = [c for c in data["connections"] if c["id"] == "conn-orphan"][0]
            assert orphan["server_name"] == "Server #9999"
            assert orphan["server_status"] == "unknown"
            assert orphan["server_reachable"] is False
        finally:
            app.app.dependency_overrides.clear()


class TestTranslationsCoverage:
    """Verifies that all required health check translation keys exist across all languages."""

    LANGUAGES = ["en", "ru", "fr", "fa", "zh"]
    REQUIRED_KEYS = [
        "status_online",
        "status_degraded",
        "status_offline",
        "status_pending",
        "network_health",
        "network_health_desc",
        "server_operational_desc",
        "server_degraded_desc",
        "server_offline_desc",
        "server_pending_desc",
    ]

    def test_all_translations_present(self):
        project_root = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
        for lang in self.LANGUAGES:
            path = os.path.join(project_root, "translations", f"{lang}.json")
            assert os.path.exists(path), f"Translation file {path} not found"
            with open(path, "r", encoding="utf-8") as f:
                data = json.load(f)
            for key in self.REQUIRED_KEYS:
                assert key in data, f"Key '{key}' missing from translations/{lang}.json"
                assert (
                    len(data[key].strip()) > 0
                ), f"Key '{key}' is empty in translations/{lang}.json"
