"""Tests for database credential encryption and protocol field stripping.

These tests verify that:
- SSH credentials are encrypted at rest in the DB
- Credentials decrypt transparently on read
- Backup excludes credentials
- Restore works without credentials (credentials_excluded flag)
- Protocol storage strips sensitive fields
"""

from __future__ import annotations

import json
import sqlite3

import pytest

import credential_crypto
from credential_crypto import (
    _init_fernet,
    decrypt_credential,
)
from database import Database

# ----------------------------------------------------------------
# Fixtures
# ----------------------------------------------------------------

TEST_SECRET_KEY = "test-secret-key-for-db-credential-tests-02"


@pytest.fixture(autouse=True)
def reset_global_fernet():
    """Reset module-level Fernet before each test."""
    credential_crypto._fernet = None
    yield
    credential_crypto._fernet = None


@pytest.fixture
def db(tmp_path):
    """Create a temporary Database with Fernet encryption initialised."""
    db_path = str(tmp_path / "test.db")
    _init_fernet(TEST_SECRET_KEY)
    database = Database(db_path, secret_key=TEST_SECRET_KEY)
    yield database
    # Cleanup


# ----------------------------------------------------------------
# Credential encryption at rest tests
# ----------------------------------------------------------------


class TestCredentialsEncryptedAtRest:
    def test_create_server_encrypts_password(self, db, tmp_path):
        server_id = db.create_server(
            {
                "name": "test-srv",
                "host": "1.2.3.4",
                "username": "root",
                "ssh_port": 22,
                "password": "my-secret-password",
                "private_key": "",
            }
        )
        # Read raw from DB (bypass _server_row_to_dict)
        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute("SELECT ssh_pass FROM servers WHERE id = ?", (server_id,)).fetchone()
        conn.close()
        # ssh_pass in DB should NOT be plaintext
        assert row[0] != "my-secret-password"
        # It should be a Fernet token
        assert row[0].startswith("g")

    def test_create_server_encrypts_private_key(self, db, tmp_path):
        server_id = db.create_server(
            {
                "name": "test-srv",
                "host": "1.2.3.4",
                "username": "root",
                "ssh_port": 22,
                "password": "",
                "private_key": "-----BEGIN KEY-----\nxyz\n-----END KEY-----",
            }
        )
        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute("SELECT ssh_key FROM servers WHERE id = ?", (server_id,)).fetchone()
        conn.close()
        assert row[0] != "-----BEGIN KEY-----\nxyz\n-----END KEY-----"
        assert row[0].startswith("g")

    def test_read_decrypts_password(self, db):
        db.create_server(
            {
                "name": "test-srv",
                "host": "1.2.3.4",
                "username": "root",
                "password": "my-secret-password",
                "private_key": "my-secret-key",
            }
        )
        servers = db.get_all_servers()
        assert servers[0]["password"] == "my-secret-password"
        assert servers[0]["private_key"] == "my-secret-key"

    def test_empty_credentials_stay_empty(self, db, tmp_path):
        """Empty strings should not be encrypted."""
        server_id = db.create_server(
            {
                "name": "test-srv",
                "host": "1.2.3.4",
                "username": "root",
                "password": "",
                "private_key": "",
            }
        )
        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute(
            "SELECT ssh_pass, ssh_key FROM servers WHERE id = ?", (server_id,)
        ).fetchone()
        conn.close()
        assert row[0] == ""
        assert row[1] == ""

    def test_update_server_encrypts_credentials(self, db, tmp_path):
        server_id = db.create_server(
            {
                "name": "test-srv",
                "host": "1.2.3.4",
                "username": "root",
                "password": "",
                "private_key": "",
            }
        )
        db.update_server(
            server_id,
            {
                "password": "updated-password",
                "private_key": "updated-key",
            },
        )
        # Raw DB read
        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute(
            "SELECT ssh_pass, ssh_key FROM servers WHERE id = ?", (server_id,)
        ).fetchone()
        conn.close()
        assert row[0] != "updated-password"
        assert row[1] != "updated-key"
        assert row[0].startswith("g")
        # Decrypt round-trip
        assert decrypt_credential(row[0]) == "updated-password"

    def test_update_server_decrypts_credentials(self, db):
        server_id = db.create_server(
            {
                "name": "test-srv",
                "host": "1.2.3.4",
                "username": "root",
                "password": "",
                "private_key": "",
            }
        )
        db.update_server(server_id, {"password": "round-trip-pass"})
        server = db.get_server_by_id(server_id)
        assert server["password"] == "round-trip-pass"


# ----------------------------------------------------------------
# Backup / Restore tests
# ----------------------------------------------------------------


class TestBackupRestore:
    def test_save_data_encrypts_credentials(self, db, tmp_path):
        data = {
            "servers": [
                {
                    "name": "srv1",
                    "host": "1.2.3.4",
                    "username": "root",
                    "ssh_port": 22,
                    "password": "cred-pass",
                    "private_key": "cred-key",
                    "protocols": {},
                }
            ],
            "users": [],
            "user_connections": [],
            "connection_creation_log": [],
            "settings": {},
        }
        db.save_data(data)
        # Verify raw DB
        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute("SELECT ssh_pass FROM servers WHERE id = 1").fetchone()
        conn.close()
        assert row[0] != "cred-pass"
        assert row[0].startswith("g")

    def test_save_data_strips_sensitive_protocol_fields(self, db, tmp_path):
        data = {
            "servers": [
                {
                    "name": "srv1",
                    "host": "1.2.3.4",
                    "username": "root",
                    "ssh_port": 22,
                    "password": "",
                    "private_key": "",
                    "protocols": {
                        "xray": {
                            "installed": True,
                            "port": 443,
                            "reality_private_key": "should-be-stripped",
                            "public_key": "should-remain",
                        }
                    },
                }
            ],
            "users": [],
            "user_connections": [],
            "connection_creation_log": [],
            "settings": {},
        }
        db.save_data(data)
        # Verify protocols JSON in DB
        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute("SELECT protocols FROM servers WHERE id = 1").fetchone()
        conn.close()
        protocols = json.loads(row[0])
        assert "reality_private_key" not in protocols.get("xray", {})
        assert protocols["xray"]["public_key"] == "should-remain"

    def test_load_data_returns_decrypted_credentials(self, db):
        db.create_server(
            {
                "name": "srv1",
                "host": "1.2.3.4",
                "username": "root",
                "password": "secret-pass",
                "private_key": "secret-key",
            }
        )
        data = db.load_data()
        server = data["servers"][0]
        assert server["password"] == "secret-pass"
        assert server["private_key"] == "secret-key"


# ----------------------------------------------------------------
# Protocol field stripping tests
# ----------------------------------------------------------------


class TestProtocolFieldStripping:
    def test_update_server_protocols_strips_sensitive(self, db, tmp_path):
        server_id = db.create_server(
            {
                "name": "srv1",
                "host": "1.2.3.4",
                "username": "root",
                "protocols": {},
            }
        )
        db.update_server_protocols(
            server_id,
            {
                "xray": {
                    "installed": True,
                    "reality_private_key": "must-be-removed",
                    "port": 443,
                }
            },
        )
        # Read raw from DB
        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute("SELECT protocols FROM servers WHERE id = ?", (server_id,)).fetchone()
        conn.close()
        protocols = json.loads(row[0])
        assert "reality_private_key" not in protocols["xray"]
        assert protocols["xray"]["installed"] is True

    def test_server_row_to_dict_strips_sensitive(self, db):
        server_id = db.create_server(
            {
                "name": "srv1",
                "host": "1.2.3.4",
                "username": "root",
                "protocols": {
                    "xray": {
                        "installed": True,
                        "reality_private_key": "leaked-key",
                        "public_key": "pub-key",
                    }
                },
            }
        )
        # Even if somehow a sensitive field ends up in DB, _server_row_to_dict strips it
        server = db.get_server_by_id(server_id)
        assert "reality_private_key" not in server.get("protocols", {}).get("xray", {})
        assert server["protocols"]["xray"]["public_key"] == "pub-key"

    def test_create_server_strips_sensitive_protocol_fields(self, db, tmp_path):
        """Sensitive protocol fields must be stripped at write time in create_server().

        This verifies the raw DB, not the read path (_server_row_to_dict).
        """
        server_id = db.create_server(
            {
                "name": "srv-strip-test",
                "host": "9.8.7.6",
                "username": "root",
                "protocols": {
                    "xray": {
                        "installed": True,
                        "port": 443,
                        "reality_private_key": "must-not-be-in-db",
                        "public_key": "should-remain",
                    },
                    "shadowsocks": {
                        "installed": True,
                        "port": 8388,
                    },
                },
            }
        )
        # Read raw protocols JSON directly from SQLite (bypass _server_row_to_dict)
        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute("SELECT protocols FROM servers WHERE id = ?", (server_id,)).fetchone()
        conn.close()
        protocols = json.loads(row[0])
        # Sensitive field must NOT be in the raw DB
        assert "reality_private_key" not in protocols.get("xray", {})
        # Non-sensitive fields must survive
        assert protocols["xray"]["public_key"] == "should-remain"
        assert protocols["xray"]["port"] == 443
        assert protocols["xray"]["installed"] is True
        assert protocols["shadowsocks"]["port"] == 8388


# ----------------------------------------------------------------
# Migration tests
# ----------------------------------------------------------------


class TestMigrations:
    def test_credentials_encrypted_migration(self, tmp_path):
        """Simulate an existing DB with plaintext credentials."""
        db_path = str(tmp_path / "migrate.db")
        # Create a minimal DB directly
        conn = sqlite3.connect(db_path)
        conn.execute(
            """
            CREATE TABLE servers (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                host TEXT NOT NULL,
                ssh_user TEXT,
                ssh_port INTEGER DEFAULT 22,
                ssh_pass TEXT,
                ssh_key TEXT,
                protocols TEXT,
                created_at TEXT
            )
        """
        )
        conn.execute(
            """
            CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)
        """
        )
        conn.execute(
            """
            CREATE TABLE users (
                id TEXT PRIMARY KEY,
                username TEXT NOT NULL,
                email TEXT,
                telegramId TEXT,
                description TEXT,
                password_hash TEXT,
                role TEXT NOT NULL DEFAULT 'user',
                enabled INTEGER NOT NULL DEFAULT 1,
                traffic_limit INTEGER,
                traffic_used INTEGER DEFAULT 0,
                traffic_total INTEGER DEFAULT 0,
                traffic_total_rx INTEGER DEFAULT 0,
                traffic_total_tx INTEGER DEFAULT 0,
                monthly_rx INTEGER DEFAULT 0,
                monthly_tx INTEGER DEFAULT 0,
                monthly_reset_at TEXT,
                traffic_reset_strategy TEXT DEFAULT 'never',
                share_enabled INTEGER DEFAULT 0,
                share_token TEXT,
                share_password_hash TEXT,
                remnawave_uuid TEXT,
                created_at TEXT,
                last_reset_at TEXT,
                expiration_date TEXT,
                password_change_required INTEGER NOT NULL DEFAULT 0,
                limits TEXT
            )
        """
        )
        conn.execute(
            """
            CREATE TABLE user_connections (
                id TEXT PRIMARY KEY,
                user_id TEXT NOT NULL,
                server_id INTEGER NOT NULL,
                protocol TEXT NOT NULL,
                client_id TEXT,
                name TEXT,
                last_rx INTEGER DEFAULT 0,
                last_tx INTEGER DEFAULT 0,
                traffic_delta_rx INTEGER DEFAULT 0,
                traffic_delta_tx INTEGER DEFAULT 0,
                created_at TEXT
            )
        """
        )
        conn.execute(
            """
            CREATE TABLE connection_creation_log (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                user_id TEXT NOT NULL,
                created_at TEXT NOT NULL
            )
        """
        )
        conn.execute(
            """
            CREATE TABLE migration_flags (key TEXT PRIMARY KEY, value TEXT)
        """
        )
        conn.execute(
            """
            CREATE TABLE known_hosts (
                server_id INTEGER PRIMARY KEY,
                fingerprint TEXT NOT NULL,
                first_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP
            )
        """
        )
        # Insert plaintext credentials
        conn.execute(
            "INSERT INTO servers (name, host, ssh_pass, ssh_key) VALUES (?, ?, ?, ?)",
            ("srv1", "1.2.3.4", "plaintext-pass", "plaintext-key"),
        )
        conn.execute(
            "INSERT INTO servers (name, host, ssh_pass, ssh_key, protocols) VALUES (?, ?, ?, ?, ?)",
            (
                "srv2",
                "5.6.7.8",
                "",
                "",
                json.dumps({"xray": {"reality_private_key": "leaked-key", "port": 443}}),
            ),
        )
        conn.commit()
        conn.close()

        # Now initialise Database (which runs migrations)
        db = Database(db_path, secret_key=TEST_SECRET_KEY)

        # Verify credentials are now encrypted
        conn = sqlite3.connect(db_path)
        row = conn.execute("SELECT ssh_pass, ssh_key FROM servers WHERE id = 1").fetchone()
        assert row[0] != "plaintext-pass"
        assert decrypt_credential(row[0]) == "plaintext-pass"
        assert decrypt_credential(row[1]) == "plaintext-key"

        # Verify sensitive protocol fields were cleared
        row2 = conn.execute("SELECT protocols FROM servers WHERE id = 2").fetchone()
        protocols = json.loads(row2[0])
        conn.close()
        assert "reality_private_key" not in protocols.get("xray", {})
        assert protocols["xray"]["port"] == 443

        # Verify migration flags are set
        assert db.get_migration_flag("credentials_encrypted") == "1"
        assert db.get_migration_flag("xray_private_keys_cleared") == "1"
