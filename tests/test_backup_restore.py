"""Tests for backup/restore endpoints (settings.py).

Covers GET /api/settings/backup/download and POST /api/settings/backup/restore.
"""

import json
import os
import tempfile
from unittest.mock import patch

from app.utils.helpers import hash_password
from database import Database
from dependencies import get_current_user
from tests.conftest import create_csrf_client

TEST_SECRET_KEY = "test-backup-restore-secret-key!!"


class TestBackupDownload:
    """Tests for GET /api/settings/backup/download."""

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
                "password": "secret-pass",
                "private_key": "secret-key-data",
                "ssh_port": 22,
                "protocols": {},
            }
        )

    def teardown_method(self):
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    def _login_and_get_client(self):
        import app

        client = create_csrf_client()
        resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        assert resp.status_code == 200
        for hv in resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        return client

    @patch("app.routers.auth.get_db")
    @patch("app.routers.settings.get_db")
    def test_backup_download_filename(self, mock_settings_db, mock_auth_db):
        """GET /api/settings/backup/download returns correct filename header."""
        mock_auth_db.return_value = self.db
        mock_settings_db.return_value = self.db

        import app

        client = self._login_and_get_client()
        try:
            resp = client.get("/api/settings/backup/download")
            assert resp.status_code == 200
            cd = resp.headers.get("content-disposition", "")
            assert "filename=amnezia-backup.json" in cd
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.settings.get_db")
    def test_backup_download_content_type(self, mock_settings_db, mock_auth_db):
        """GET /api/settings/backup/download returns application/json."""
        mock_auth_db.return_value = self.db
        mock_settings_db.return_value = self.db

        import app

        client = self._login_and_get_client()
        try:
            resp = client.get("/api/settings/backup/download")
            assert resp.status_code == 200
            assert resp.headers["content-type"] == "application/json"
            # Verify the body is valid JSON
            data = json.loads(resp.content)
            assert isinstance(data, dict)
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.settings.get_db")
    def test_backup_download_requires_admin(self, mock_settings_db, mock_auth_db):
        """Non-admin gets 403 from backup download."""
        mock_auth_db.return_value = self.db
        mock_settings_db.return_value = self.db

        # Create a non-admin user
        self.db.create_user(
            {
                "id": "user-1",
                "username": "regular",
                "password_hash": hash_password("UserPass123"),
                "enabled": True,
                "traffic_limit": 0,
                "traffic_used": 0,
                "role": "user",
                "limits": {},
            }
        )

        import app

        client = create_csrf_client()
        resp = client.post(
            "/api/auth/login",
            json={"username": "regular", "password": "UserPass123"},
        )
        assert resp.status_code == 200
        for hv in resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("user-1")
        try:
            resp = client.get("/api/settings/backup/download")
            assert resp.status_code == 403
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.settings.get_db")
    def test_backup_download_strips_credentials(self, mock_settings_db, mock_auth_db):
        """Backup JSON has credentials_excluded=true and servers lack password/private_key."""
        mock_auth_db.return_value = self.db
        mock_settings_db.return_value = self.db

        import app

        client = self._login_and_get_client()
        try:
            resp = client.get("/api/settings/backup/download")
            assert resp.status_code == 200
            data = json.loads(resp.content)
            assert data.get("credentials_excluded") is True
            for srv in data.get("servers", []):
                assert "password" not in srv
                assert "private_key" not in srv
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.settings.get_db")
    def test_backup_download_includes_all_tables(self, mock_settings_db, mock_auth_db):
        """Backup JSON includes all 5 table keys."""
        mock_auth_db.return_value = self.db
        mock_settings_db.return_value = self.db

        import app

        client = self._login_and_get_client()
        try:
            resp = client.get("/api/settings/backup/download")
            assert resp.status_code == 200
            data = json.loads(resp.content)
            assert "servers" in data
            assert "users" in data
            assert "user_connections" in data
            assert "connection_creation_log" in data
            assert "settings" in data
        finally:
            app.app.dependency_overrides.clear()


class TestBackupRestore:
    """Tests for POST /api/settings/backup/restore."""

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
                "name": "Original Server",
                "host": "10.0.0.1",
                "username": "root",
                "password": "old-pass",
                "ssh_port": 22,
                "protocols": {},
            }
        )

    def teardown_method(self):
        conn = self.db._get_conn()
        conn.close()
        os.unlink(self.tmp_db_path)

    def _login_and_get_client(self):
        import app

        client = create_csrf_client()
        resp = client.post(
            "/api/auth/login",
            json={"username": "admin", "password": "AdminPass123"},
        )
        assert resp.status_code == 200
        for hv in resp.headers.get_list("set-cookie"):
            if hv.startswith("session="):
                client.cookies.set("session", hv.split("session=")[1].split(";")[0])
                break
        app.app.dependency_overrides[get_current_user] = lambda: self.db.get_user("admin-1")
        return client

    @patch("app.routers.auth.get_db")
    @patch("app.routers.settings.get_db")
    def test_restore_valid_backup(self, mock_settings_db, mock_auth_db):
        """POST /api/settings/backup/restore with valid JSON returns success."""
        mock_auth_db.return_value = self.db
        mock_settings_db.return_value = self.db

        backup = {
            "servers": [
                {
                    "name": "Restored Server",
                    "host": "10.0.0.99",
                    "username": "root",
                    "password": "",
                    "ssh_port": 22,
                    "protocols": {},
                }
            ],
            "users": [
                {
                    "id": "user-r1",
                    "username": "restored_user",
                    "password_hash": hash_password("pass"),
                    "enabled": True,
                    "traffic_limit": 0,
                    "traffic_used": 0,
                    "role": "user",
                    "limits": {},
                }
            ],
            "user_connections": [],
            "connection_creation_log": [],
            "settings": {},
        }

        import app

        client = self._login_and_get_client()
        try:
            body = json.dumps(backup).encode("utf-8")
            resp = client.post(
                "/api/settings/backup/restore",
                files={"file": ("backup.json", body, "application/json")},
            )
            assert resp.status_code == 200
            data = resp.json()
            assert data["status"] == "success"
            # Verify the server was actually replaced
            servers = self.db.get_all_servers()
            assert len(servers) == 1
            assert servers[0]["name"] == "Restored Server"
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.settings.get_db")
    def test_restore_empty_file(self, mock_settings_db, mock_auth_db):
        """POST with empty file returns 400."""
        mock_auth_db.return_value = self.db
        mock_settings_db.return_value = self.db

        import app

        client = self._login_and_get_client()
        try:
            resp = client.post(
                "/api/settings/backup/restore",
                files={"file": ("empty.json", b"", "application/json")},
            )
            assert resp.status_code == 400
            assert "Empty" in resp.json()["error"]
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.settings.get_db")
    def test_restore_invalid_json(self, mock_settings_db, mock_auth_db):
        """POST with non-JSON content returns 400."""
        mock_auth_db.return_value = self.db
        mock_settings_db.return_value = self.db

        import app

        client = self._login_and_get_client()
        try:
            resp = client.post(
                "/api/settings/backup/restore",
                files={"file": ("bad.json", b"this is not json at all", "application/json")},
            )
            assert resp.status_code == 400
            assert "Invalid JSON" in resp.json()["error"]
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.settings.get_db")
    def test_restore_missing_required_keys(self, mock_settings_db, mock_auth_db):
        """POST with JSON missing 'servers' key returns 400."""
        mock_auth_db.return_value = self.db
        mock_settings_db.return_value = self.db

        backup = {"users": [], "settings": {}}
        import app

        client = self._login_and_get_client()
        try:
            body = json.dumps(backup).encode("utf-8")
            resp = client.post(
                "/api/settings/backup/restore",
                files={"file": ("bad.json", body, "application/json")},
            )
            assert resp.status_code == 400
            assert "Missing keys" in resp.json()["error"]
            assert "servers" in resp.json()["error"]
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.settings.get_db")
    def test_restore_old_format_backward_compat(self, mock_settings_db, mock_auth_db):
        """POST with only servers+users (old data.json format) succeeds."""
        mock_auth_db.return_value = self.db
        mock_settings_db.return_value = self.db

        backup = {
            "servers": [
                {
                    "name": "Old Format Server",
                    "host": "10.0.0.77",
                    "username": "root",
                    "password": "",
                    "ssh_port": 22,
                    "protocols": {},
                }
            ],
            "users": [
                {
                    "id": "old-user",
                    "username": "old_user",
                    "password_hash": hash_password("pass"),
                    "enabled": True,
                    "traffic_limit": 0,
                    "traffic_used": 0,
                    "role": "user",
                    "limits": {},
                }
            ],
        }

        import app

        client = self._login_and_get_client()
        try:
            body = json.dumps(backup).encode("utf-8")
            resp = client.post(
                "/api/settings/backup/restore",
                files={"file": ("data.json", body, "application/json")},
            )
            assert resp.status_code == 200
            assert resp.json()["status"] == "success"
            servers = self.db.get_all_servers()
            assert servers[0]["name"] == "Old Format Server"
        finally:
            app.app.dependency_overrides.clear()

    @patch("app.routers.auth.get_db")
    @patch("app.routers.settings.get_db")
    def test_restore_credentials_excluded_flag(self, mock_settings_db, mock_auth_db):
        """When backup has credentials_excluded=true, servers get empty pass/key."""
        mock_auth_db.return_value = self.db
        mock_settings_db.return_value = self.db

        backup = {
            "credentials_excluded": True,
            "servers": [
                {
                    "name": "Creds Excluded Server",
                    "host": "10.0.0.55",
                    "username": "root",
                    "password": "",
                    "ssh_port": 22,
                    "protocols": {},
                }
            ],
            "users": [
                {
                    "id": "ce-user",
                    "username": "creds_user",
                    "password_hash": hash_password("pass"),
                    "enabled": True,
                    "traffic_limit": 0,
                    "traffic_used": 0,
                    "role": "user",
                    "limits": {},
                }
            ],
            "user_connections": [],
            "connection_creation_log": [],
            "settings": {},
        }

        import app

        client = self._login_and_get_client()
        try:
            body = json.dumps(backup).encode("utf-8")
            resp = client.post(
                "/api/settings/backup/restore",
                files={"file": ("backup.json", body, "application/json")},
            )
            assert resp.status_code == 200
            assert resp.json()["status"] == "success"
            servers = self.db.get_all_servers()
            assert servers[0]["name"] == "Creds Excluded Server"
        finally:
            app.app.dependency_overrides.clear()


class TestBackupRestoreExtendedData:
    """Tests for known_hosts, leaderboard_snapshots, and SSL encryption in save_data / load_data."""

    def test_backup_and_restore_preserves_extended_data(self):
        tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        tmp_db_path = tmp_db.name
        tmp_db.close()
        try:
            db = Database(tmp_db_path, secret_key=TEST_SECRET_KEY)
            db.save_known_host_fingerprint(1, "aa:bb:cc:dd")
            db.save_leaderboard_snapshot(2026, 7)

            data = db.load_data()
            assert "known_hosts" in data
            assert "leaderboard_snapshots" in data
            assert any(kh.get("fingerprint") == "aa:bb:cc:dd" for kh in data["known_hosts"])

            # Test save_data restores known_hosts and encrypts SSL
            data["settings"]["ssl"] = {
                "enabled": True,
                "key_text": "-----BEGIN PRIVATE KEY-----\nMIIE...\n-----END PRIVATE KEY-----",
                "cert_text": "-----BEGIN CERTIFICATE-----\nMIID...\n-----END CERTIFICATE-----",
            }
            db.save_data(data)

            # Check known hosts restored
            kh_restored = db._get_all_known_hosts()
            assert any(kh.get("fingerprint") == "aa:bb:cc:dd" for kh in kh_restored)

            # Check SSL was encrypted at rest
            ssl_setting = db.get_setting("ssl")
            assert ssl_setting["enabled"] is True
            assert (
                ssl_setting["key_text"]
                == "-----BEGIN PRIVATE KEY-----\nMIIE...\n-----END PRIVATE KEY-----"
            )

            # Direct raw DB check: the raw string stored in sqlite should NOT be plaintext private key
            raw_ssl = db.get_setting("ssl", decrypt_ssl=False)
            assert "-----BEGIN PRIVATE KEY-----" not in raw_ssl["key_text"]
        finally:
            conn = db._get_conn()
            conn.close()
            os.unlink(tmp_db_path)

