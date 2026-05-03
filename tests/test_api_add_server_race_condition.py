"""Tests for api_add_server race condition fix — Issue #130.

Also updated for the two-phase fingerprint confirmation flow (Issue #128).
Verifies that api_add_server returns the fingerprint for admin confirmation
and that api_confirm_server_fingerprint persists the server with `lastrowid`.
"""

import os
import tempfile
from unittest.mock import MagicMock, patch

from app.utils.helpers import hash_password
from database import Database
from dependencies import get_current_user
from tests.conftest import create_csrf_client

TEST_SECRET_KEY = "test-integration-server-key"


class TestApiAddServerRaceCondition:
    """Verify api_add_server returns fingerprint + confirm endpoint uses lastrowid."""

    def setup_method(self):
        """Set up temporary database with an admin user, no servers."""
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

    def teardown_method(self):
        """Clean up temporary database."""
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    def _login(self, client):
        """Login as admin, extract session cookie, set dependency override."""
        import app  # noqa: F811

        login_resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        assert login_resp.status_code == 200, f"Login failed: {login_resp.status_code}"

        for hv in login_resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break

        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")

    def _cleanup_overrides(self):
        import app

        app.app.dependency_overrides.clear()

    def _build_mock_ssh(self):
        """Return a mock SSHManager with transport/fingerprint wired up."""
        mock_ssh = MagicMock()
        mock_ssh.connect.return_value = None
        mock_ssh.test_connection.return_value = "Ubuntu 22.04 x86_64\\nPRETTY_NAME=Ubuntu"
        mock_ssh.disconnect.return_value = None

        # Wire up transport for fingerprint extraction in api_add_server
        mock_host_key = MagicMock()
        mock_host_key.get_fingerprint.return_value = b"\xde\xad\xbe\xef" * 8

        mock_transport = MagicMock()
        mock_transport.get_remote_server_key.return_value = mock_host_key

        mock_client = MagicMock()
        mock_client.get_transport.return_value = mock_transport

        mock_ssh.client = mock_client

        return mock_ssh

    # ------------------------------------------------------------------
    # Test 1: api_add_server returns fingerprint, NOT status=success
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_add_server_returns_pending_fingerprint_confirmation(
        self, mock_servers_db, mock_auth_db
    ):
        """api_add_server must return status=pending_fingerprint_confirmation."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        client = create_csrf_client()
        mock_ssh = self._build_mock_ssh()

        self._login(client)
        try:
            with patch("app.routers.servers.SSHManager", return_value=mock_ssh):
                add_resp = client.post(
                    "/api/servers/add",
                    json={
                        "host": "10.0.0.1",
                        "username": "root",
                        "password": "pass123",
                        "ssh_port": 22,
                        "name": "Server One",
                    },
                )

            assert add_resp.status_code == 200, f"Add failed: {add_resp.status_code}"
            data = add_resp.json()
            assert data["status"] == "pending_fingerprint_confirmation"
            assert "fingerprint" in data
            assert isinstance(data["fingerprint"], str)
            assert len(data["fingerprint"]) > 0
            assert "server_info" in data
        finally:
            self._cleanup_overrides()

    # ------------------------------------------------------------------
    # Test 2: confirm-fingerprint saves server with correct lastrowid
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_confirm_fingerprint_saves_server_with_correct_id(self, mock_servers_db, mock_auth_db):
        """api_confirm_server_fingerprint returns status=success with server_id=1."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        client = create_csrf_client()
        self._login(client)
        try:
            confirm_resp = client.post(
                "/api/servers/confirm-fingerprint",
                json={
                    "host": "10.0.0.1",
                    "username": "root",
                    "password": "pass123",
                    "ssh_port": 22,
                    "name": "Server One",
                    "server_info": "Ubuntu 22.04",
                    "fingerprint": "deadbeef" * 8,
                },
            )

            assert confirm_resp.status_code == 200, f"Confirm failed: {confirm_resp.status_code}"
            data = confirm_resp.json()
            assert data["status"] == "success"
            assert data["server_id"] == 1, (
                f"Expected server_id=1, got {data['server_id']}. " "Race condition fix not applied."
            )

            # Verify fingerprint stored in known_hosts
            stored_fp = self.db.get_known_host_fingerprint(1)
            assert stored_fp == "deadbeef" * 8
        finally:
            self._cleanup_overrides()

    # ------------------------------------------------------------------
    # Test 3: Returned server_id matches get_server_by_id lookup
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_returned_id_matches_get_server_by_id(self, mock_servers_db, mock_auth_db):
        """After confirm-fingerprint, get_server_by_id(returned_id) must work."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        client = create_csrf_client()
        self._login(client)
        try:
            confirm_resp = client.post(
                "/api/servers/confirm-fingerprint",
                json={
                    "host": "10.0.0.2",
                    "username": "root",
                    "password": "pass456",
                    "ssh_port": 2222,
                    "name": "Server Two",
                    "server_info": "Debian 12",
                    "fingerprint": "ab" * 32,
                },
            )
            assert confirm_resp.status_code == 200
            server_id = confirm_resp.json()["server_id"]

            server = self.db.get_server_by_id(server_id)
            assert server is not None, f"Server id={server_id} not found"
            assert server["name"] == "Server Two"
            assert server["host"] == "10.0.0.2"
        finally:
            self._cleanup_overrides()

    # ------------------------------------------------------------------
    # Test 4: Two sequential confirms get correct, different IDs
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_two_sequential_confirms_get_different_ids(self, mock_servers_db, mock_auth_db):
        """Two confirms must receive distinct auto-increment IDs."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        client1 = create_csrf_client()
        self._login(client1)
        try:
            resp1 = client1.post(
                "/api/servers/confirm-fingerprint",
                json={
                    "host": "10.0.0.3",
                    "username": "root",
                    "password": "pw",
                    "ssh_port": 22,
                    "name": "Server Three",
                    "server_info": "",
                    "fingerprint": "ff" * 32,
                },
            )
            resp2 = client1.post(
                "/api/servers/confirm-fingerprint",
                json={
                    "host": "10.0.0.4",
                    "username": "root",
                    "password": "pw",
                    "ssh_port": 22,
                    "name": "Server Four",
                    "server_info": "",
                    "fingerprint": "ee" * 32,
                },
            )

            assert resp1.status_code == 200
            assert resp2.status_code == 200
            id1 = resp1.json()["server_id"]
            id2 = resp2.json()["server_id"]

            assert id1 != id2, f"IDs must differ: {id1}, {id2}"
            assert id1 == 1
            assert id2 == 2

            srv1 = self.db.get_server_by_id(id1)
            srv2 = self.db.get_server_by_id(id2)
            assert srv1["name"] == "Server Three"
            assert srv2["name"] == "Server Four"
        finally:
            self._cleanup_overrides()
