"""Tests for SSH host key fingerprint confirmation flow — Issue #128.

Covers:
- api_add_server returns pending_fingerprint_confirmation with fingerprint
- api_confirm_server_fingerprint saves server + fingerprint in known_hosts
- Subsequent connection to confirmed server uses stored fingerprint (RejectPolicy)
- Missing fingerprint in confirm request returns 422 (pydantic validation)
- SSHHostKeyError during add returns error (not pending)
"""

import os
import tempfile
from unittest.mock import MagicMock, patch

from app.utils.helpers import hash_password
from database import Database
from dependencies import get_current_user
from app.managers import SSHHostKeyError
from tests.conftest import create_csrf_client

TEST_SECRET_KEY = "test-fingerprint-conf-key"


class TestSshFingerprintConfirmation:
    """Integration tests for the two-phase server addition fingerprint flow."""

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

    def teardown_method(self):
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    def _login(self, client):
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

    def _cleanup(self):
        import app

        app.app.dependency_overrides.clear()

    def _add_and_confirm(self, client, host="10.0.0.1", name="Test Server"):
        """Full two-phase flow: add → get fingerprint → confirm → return server_id."""
        from unittest.mock import patch

        # Phase 1 — wire instance mock, not just the class mock
        mock_instance = MagicMock()
        mock_instance.connect.return_value = None
        mock_instance.test_connection.return_value = "Ubuntu 22.04"
        mock_instance.disconnect.return_value = None
        self._wire_transport(mock_instance, b"\x01" * 32)

        with patch("app.routers.servers.SSHManager", return_value=mock_instance):
            add_resp = client.post(
                "/api/servers/add",
                json={
                    "host": host,
                    "username": "root",
                    "password": "testpass",
                    "ssh_port": 22,
                    "name": name,
                },
            )
        assert add_resp.status_code == 200
        add_data = add_resp.json()
        assert add_data["status"] == "pending_fingerprint_confirmation"
        fingerprint = add_data["fingerprint"]

        # Phase 2
        confirm_resp = client.post(
            "/api/servers/confirm-fingerprint",
            json={
                "host": host,
                "username": "root",
                "password": "testpass",
                "ssh_port": 22,
                "name": name,
                "server_info": add_data["server_info"],
                "fingerprint": fingerprint,
            },
        )
        assert confirm_resp.status_code == 200
        assert confirm_resp.json()["status"] == "success"
        return confirm_resp.json()["server_id"]

    @staticmethod
    def _mock_ssh_patch():
        return patch("app.routers.servers.SSHManager")

    @staticmethod
    def _wire_transport(mock_ssh, raw_fingerprint):
        """Wire mock_ssh.client.get_transport().get_remote_server_key().get_fingerprint()."""
        mock_host_key = MagicMock()
        mock_host_key.get_fingerprint.return_value = raw_fingerprint
        mock_transport = MagicMock()
        mock_transport.get_remote_server_key.return_value = mock_host_key
        mock_client = MagicMock()
        mock_client.get_transport.return_value = mock_transport
        mock_ssh.client = mock_client

    # ------------------------------------------------------------------
    # Test 1: api_add_server returns pending_fingerprint_confirmation
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_add_server_returns_fingerprint(self, mock_servers_db, mock_auth_db):
        """api_add_server returns fingerprint for admin approval."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        client = create_csrf_client()
        self._login(client)
        try:
            with self._mock_ssh_patch() as mock_ssh_class:
                mock_ssh = MagicMock()
                mock_ssh.connect.return_value = None
                mock_ssh.test_connection.return_value = "Ubuntu 22.04"
                mock_ssh.disconnect.return_value = None
                self._wire_transport(mock_ssh, b"\xab\xcd\xef\x01" * 8)
                mock_ssh_class.return_value = mock_ssh

                add_resp = client.post(
                    "/api/servers/add",
                    json={
                        "host": "10.0.0.10",
                        "username": "root",
                        "password": "pass",
                        "ssh_port": 22,
                        "name": "TOFU Server",
                    },
                )

            assert add_resp.status_code == 200
            data = add_resp.json()
            assert data["status"] == "pending_fingerprint_confirmation"
            assert data["fingerprint"] == (b"\xab\xcd\xef\x01" * 8).hex()
            assert "server_info" in data

            # Server must NOT be in the database yet
            servers = self.db.get_all_servers()
            assert len(servers) == 0, "Server was saved before fingerprint confirmation"
        finally:
            self._cleanup()

    # ------------------------------------------------------------------
    # Test 2: api_confirm_server_fingerprint saves server + fingerprint
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_confirm_fingerprint_saves_server_and_fingerprint(self, mock_servers_db, mock_auth_db):
        """Confirm endpoint persists server and fingerprint in known_hosts."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        client = create_csrf_client()
        self._login(client)
        try:
            server_id = self._add_and_confirm(client, "10.0.0.11", "Confirmed Server")

            # Verify server exists
            server = self.db.get_server_by_id(server_id)
            assert server is not None
            assert server["name"] == "Confirmed Server"
            assert server["host"] == "10.0.0.11"

            # Verify fingerprint stored
            stored_fp = self.db.get_known_host_fingerprint(server_id)
            assert stored_fp is not None
            assert stored_fp == (b"\x01" * 32).hex()
        finally:
            self._cleanup()

    # ------------------------------------------------------------------
    # Test 3: confirmed server has fingerprint in known_hosts table
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_known_hosts_entry_exists_after_confirmation(self, mock_servers_db, mock_auth_db):
        """After confirm, known_hosts row exists with correct server_id."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        client = create_csrf_client()
        self._login(client)
        try:
            server_id = self._add_and_confirm(client, "10.0.0.12", "FP Server")
            stored_fp = self.db.get_known_host_fingerprint(server_id)
            assert stored_fp is not None
            assert isinstance(stored_fp, str)
            assert len(stored_fp) > 0
        finally:
            self._cleanup()

    # ------------------------------------------------------------------
    # Test 4: subsequent connection uses stored fingerprint (RejectPolicy)
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_subsequent_connection_uses_stored_fingerprint(self, mock_servers_db, mock_auth_db):
        """get_ssh() with db parameter passes database + server_id to SSHManager."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        client = create_csrf_client()
        self._login(client)
        try:
            server_id = self._add_and_confirm(client, "10.0.0.13", "RejectPolicy Server")

            # Now test get_ssh with db — it should pass database and server_id
            from app.utils.helpers import get_ssh

            server = self.db.get_server_by_id(server_id)
            ssh = get_ssh(server, db=self.db)
            assert ssh._database is self.db
            assert ssh._server_id == server_id
        finally:
            self._cleanup()

    # ------------------------------------------------------------------
    # Test 5: missing fingerprint returns validation error (422)
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_missing_fingerprint_returns_422(self, mock_servers_db, mock_auth_db):
        """POST to confirm-fingerprint without fingerprint returns 422."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        client = create_csrf_client()
        self._login(client)
        try:
            resp = client.post(
                "/api/servers/confirm-fingerprint",
                json={
                    "host": "10.0.0.14",
                    "username": "root",
                    "password": "pass",
                    "ssh_port": 22,
                    "name": "No FP",
                    "server_info": "",
                    # fingerprint intentionally omitted
                },
            )
            assert (
                resp.status_code == 422
            ), f"Expected 422 for missing fingerprint, got {resp.status_code}"
        finally:
            self._cleanup()

    # ------------------------------------------------------------------
    # Test 6: confirm with empty fingerprint returns 422
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_empty_fingerprint_returns_422(self, mock_servers_db, mock_auth_db):
        """Pydantic min_length=1 rejects empty fingerprint string."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        client = create_csrf_client()
        self._login(client)
        try:
            resp = client.post(
                "/api/servers/confirm-fingerprint",
                json={
                    "host": "10.0.0.15",
                    "username": "root",
                    "password": "pass",
                    "ssh_port": 22,
                    "name": "Empty FP",
                    "server_info": "",
                    "fingerprint": "",
                },
            )
            assert (
                resp.status_code == 422
            ), f"Expected 422 for empty fingerprint, got {resp.status_code}"
        finally:
            self._cleanup()

    # ------------------------------------------------------------------
    # Test 7: SSHHostKeyError during add returns error, not pending
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_ssh_host_key_error_during_add(self, mock_servers_db, mock_auth_db):
        """If connect raises SSHHostKeyError, api_add_server returns 400 error."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        client = create_csrf_client()
        self._login(client)
        try:
            with self._mock_ssh_patch() as mock_ssh_class:
                mock_ssh = MagicMock()
                mock_ssh.connect.side_effect = SSHHostKeyError("Host key mismatch for 10.0.0.99")
                mock_ssh_class.return_value = mock_ssh

                add_resp = client.post(
                    "/api/servers/add",
                    json={
                        "host": "10.0.0.99",
                        "username": "root",
                        "password": "pass",
                        "ssh_port": 22,
                        "name": "Mismatch Server",
                    },
                )

            assert add_resp.status_code == 400
            data = add_resp.json()
            assert "error" in data
            # Must NOT return pending_fingerprint_confirmation
            assert data.get("status") != "pending_fingerprint_confirmation"
        finally:
            self._cleanup()

    # ------------------------------------------------------------------
    # Test 8: Server NOT saved before fingerprint confirmation
    # ------------------------------------------------------------------

    @patch("app.routers.auth.get_db")
    @patch("app.routers.servers.get_db")
    def test_server_not_saved_before_confirm(self, mock_servers_db, mock_auth_db):
        """After api_add_server returns fingerprint, server is NOT in the database."""
        mock_auth_db.return_value = self.db
        mock_servers_db.return_value = self.db

        client = create_csrf_client()
        self._login(client)
        try:
            with self._mock_ssh_patch() as mock_ssh_class:
                mock_ssh = MagicMock()
                mock_ssh.connect.return_value = None
                mock_ssh.test_connection.return_value = "Ubuntu"
                mock_ssh.disconnect.return_value = None
                self._wire_transport(mock_ssh, b"\xcc" * 32)
                mock_ssh_class.return_value = mock_ssh

                client.post(
                    "/api/servers/add",
                    json={
                        "host": "10.0.0.16",
                        "username": "root",
                        "password": "pass",
                        "ssh_port": 22,
                        "name": "Unsaved Server",
                    },
                )

            # Server count must be 0
            servers = self.db.get_all_servers()
            assert len(servers) == 0, f"Expected 0 servers before confirm, got {len(servers)}"
        finally:
            self._cleanup()
