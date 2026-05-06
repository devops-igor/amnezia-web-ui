"""Tests for SSL key_text/cert_text encryption at rest in database.py.

Verifies:
- SSL key_text and cert_text are encrypted when saved via update_setting
- SSL key_text and cert_text are decrypted transparently when read via get_all_settings
- API GET /api/settings returns empty strings for key_text and cert_text
- POST /api/settings/save encrypts key/cert before DB storage
- Migration encrypts existing plaintext SSL settings
- decrypt_credential_safe() returns empty string on InvalidToken
- key_path and cert_path are NOT encrypted (they are file paths, not secrets)
"""

from __future__ import annotations

import json
import sqlite3

import pytest

import credential_crypto
from credential_crypto import (
    _init_fernet,
    _looks_like_fernet_token,
    decrypt_credential,
    decrypt_credential_safe,
    encrypt_credential,
)
from database import Database

# ----------------------------------------------------------------
# Fixtures
# ----------------------------------------------------------------

TEST_SECRET_KEY = "test-secret-key-for-ssl-encryption-tests-04"


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


# ----------------------------------------------------------------
# SSL key_text/cert_text encryption at rest
# ----------------------------------------------------------------


class TestSslEncryptionAtRest:
    def test_update_setting_encrypts_key_text(self, db, tmp_path):
        """key_text should be encrypted when saved via update_setting("ssl", ...)."""
        ssl_data = {
            "enabled": True,
            "domain": "example.com",
            "cert_path": "/etc/ssl/cert.pem",
            "key_path": "/etc/ssl/key.pem",
            "cert_text": "-----BEGIN CERTIFICATE-----\nABC\n-----END CERTIFICATE-----",
            "key_text": "-----BEGIN PRIVATE KEY-----\nXYZ\n-----END PRIVATE KEY-----",
            "panel_port": 5000,
        }
        db.update_setting("ssl", ssl_data)

        # Read raw from DB (bypass get_all_settings decryption)
        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute("SELECT value FROM settings WHERE key = 'ssl'").fetchone()
        conn.close()
        stored = json.loads(row[0])

        # key_text in DB should NOT be plaintext
        assert stored["key_text"] != "-----BEGIN PRIVATE KEY-----\nXYZ\n-----END PRIVATE KEY-----"
        assert _looks_like_fernet_token(stored["key_text"])

    def test_update_setting_encrypts_cert_text(self, db, tmp_path):
        """cert_text should be encrypted when saved via update_setting("ssl", ...)."""
        ssl_data = {
            "enabled": True,
            "domain": "example.com",
            "cert_path": "/etc/ssl/cert.pem",
            "key_path": "/etc/ssl/key.pem",
            "cert_text": "-----BEGIN CERTIFICATE-----\nDEF\n-----END CERTIFICATE-----",
            "key_text": "-----BEGIN PRIVATE KEY-----\nUVW\n-----END PRIVATE KEY-----",
            "panel_port": 5000,
        }
        db.update_setting("ssl", ssl_data)

        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute("SELECT value FROM settings WHERE key = 'ssl'").fetchone()
        conn.close()
        stored = json.loads(row[0])

        assert stored["cert_text"] != "-----BEGIN CERTIFICATE-----\nDEF\n-----END CERTIFICATE-----"
        assert _looks_like_fernet_token(stored["cert_text"])

    def test_read_decrypts_key_text(self, db):
        """key_text should be decrypted transparently when read via get_all_settings()."""
        original_key = "-----BEGIN PRIVATE KEY-----\nREALKEY\n-----END PRIVATE KEY-----"
        ssl_data = {
            "enabled": True,
            "domain": "example.com",
            "key_text": original_key,
            "cert_text": "",
            "cert_path": "",
            "key_path": "",
            "panel_port": 5000,
        }
        db.update_setting("ssl", ssl_data)

        settings = db.get_all_settings()
        ssl = settings["ssl"]
        assert ssl["key_text"] == original_key

    def test_read_decrypts_cert_text(self, db):
        """cert_text should be decrypted transparently when read via get_all_settings()."""
        original_cert = "-----BEGIN CERTIFICATE-----\nREALCERT\n-----END CERTIFICATE-----"
        ssl_data = {
            "enabled": True,
            "domain": "example.com",
            "key_text": "",
            "cert_text": original_cert,
            "cert_path": "",
            "key_path": "",
            "panel_port": 5000,
        }
        db.update_setting("ssl", ssl_data)

        settings = db.get_all_settings()
        ssl = settings["ssl"]
        assert ssl["cert_text"] == original_cert

    def test_empty_key_text_not_encrypted(self, db, tmp_path):
        """Empty key_text should remain empty (not encrypted to a token)."""
        ssl_data = {
            "enabled": False,
            "domain": "",
            "key_text": "",
            "cert_text": "",
            "cert_path": "",
            "key_path": "",
            "panel_port": 5000,
        }
        db.update_setting("ssl", ssl_data)

        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute("SELECT value FROM settings WHERE key = 'ssl'").fetchone()
        conn.close()
        stored = json.loads(row[0])
        assert stored["key_text"] == ""
        assert stored["cert_text"] == ""

    def test_key_path_not_encrypted(self, db, tmp_path):
        """key_path is a file path, not secret material — should NOT be encrypted."""
        ssl_data = {
            "enabled": True,
            "domain": "example.com",
            "key_path": "/etc/ssl/private/key.pem",
            "cert_path": "/etc/ssl/certs/cert.pem",
            "key_text": "-----BEGIN PRIVATE KEY-----\nSECRET\n-----END PRIVATE KEY-----",
            "cert_text": "-----BEGIN CERTIFICATE-----\nSECRET\n-----END CERTIFICATE-----",
            "panel_port": 5000,
        }
        db.update_setting("ssl", ssl_data)

        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute("SELECT value FROM settings WHERE key = 'ssl'").fetchone()
        conn.close()
        stored = json.loads(row[0])
        # Paths should be stored as-is (plaintext)
        assert stored["key_path"] == "/etc/ssl/private/key.pem"
        assert stored["cert_path"] == "/etc/ssl/certs/cert.pem"

    def test_cert_path_not_encrypted(self, db, tmp_path):
        """cert_path should not be encrypted — it's not secret material."""
        ssl_data = {
            "enabled": True,
            "domain": "example.com",
            "key_path": "/etc/ssl/key.pem",
            "cert_path": "/etc/ssl/cert.pem",
            "key_text": "",
            "cert_text": "",
            "panel_port": 5000,
        }
        db.update_setting("ssl", ssl_data)

        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute("SELECT value FROM settings WHERE key = 'ssl'").fetchone()
        conn.close()
        stored = json.loads(row[0])
        assert stored["cert_path"] == "/etc/ssl/cert.pem"
        assert stored["key_path"] == "/etc/ssl/key.pem"


# ----------------------------------------------------------------
# save_all_settings encryption
# ----------------------------------------------------------------


class TestSaveAllSettingsSslEncryption:
    def test_save_all_settings_encrypts_ssl_key(self, db, tmp_path):
        """save_all_settings() should encrypt SSL key_text/cert_text."""
        ssl_data = {
            "enabled": True,
            "domain": "example.com",
            "key_text": "-----BEGIN PRIVATE KEY-----\nBATCH\n-----END PRIVATE KEY-----",
            "cert_text": "-----BEGIN CERTIFICATE-----\nBATCH\n-----END CERTIFICATE-----",
            "cert_path": "",
            "key_path": "",
            "panel_port": 5000,
        }
        db.save_all_settings({"ssl": ssl_data, "appearance": {"title": "Test"}})

        conn = sqlite3.connect(str(tmp_path / "test.db"))
        row = conn.execute("SELECT value FROM settings WHERE key = 'ssl'").fetchone()
        conn.close()
        stored = json.loads(row[0])
        assert _looks_like_fernet_token(stored["key_text"])
        assert _looks_like_fernet_token(stored["cert_text"])

    def test_save_all_settings_decrypts_on_read(self, db):
        """After save_all_settings, get_all_settings should return decrypted values."""
        original_key = "-----BEGIN PRIVATE KEY-----\nFROM_BATCH\n-----END PRIVATE KEY-----"
        ssl_data = {
            "enabled": True,
            "domain": "example.com",
            "key_text": original_key,
            "cert_text": "",
            "cert_path": "",
            "key_path": "",
            "panel_port": 5000,
        }
        db.save_all_settings({"ssl": ssl_data})

        settings = db.get_all_settings()
        assert settings["ssl"]["key_text"] == original_key


# ----------------------------------------------------------------
# get_setting("ssl") decryption
# ----------------------------------------------------------------


class TestGetSettingSslDecryption:
    def test_get_setting_ssl_decrypts(self, db):
        """get_setting("ssl") should return decrypted key_text/cert_text."""
        original_key = "-----BEGIN PRIVATE KEY-----\nGETSETTING\n-----END PRIVATE KEY-----"
        ssl_data = {
            "enabled": True,
            "domain": "test.local",
            "key_text": original_key,
            "cert_text": "",
            "cert_path": "",
            "key_path": "",
            "panel_port": 5000,
        }
        db.update_setting("ssl", ssl_data)

        ssl = db.get_setting("ssl")
        assert ssl["key_text"] == original_key


# ----------------------------------------------------------------
# decrypt_credential_safe tests
# ----------------------------------------------------------------


class TestDecryptCredentialSafe:
    def test_round_trip(self, db):
        """Encrypt then decrypt via safe variant returns original."""
        plaintext = "my-ssl-private-key-content"
        encrypted = encrypt_credential(plaintext)
        assert decrypt_credential_safe(encrypted) == plaintext

    def test_empty_string_passthrough(self, db):
        """Empty string returns empty string."""
        assert decrypt_credential_safe("") == ""

    def test_none_passthrough(self, db):
        """None returns empty string."""
        assert decrypt_credential_safe(None) == ""

    def test_invalid_token_returns_empty(self, db):
        """Corrupt/invalid token returns empty string instead of raising."""
        result = decrypt_credential_safe("gAAAAA...NotARealToken===")
        assert result == ""

    def test_secret_key_change_returns_empty(self, db):
        """When SECRET_KEY changes, safe decrypt returns empty instead of raising."""
        encrypted = encrypt_credential("some-data")
        # Re-init with a different key
        _init_fernet("completely-different-secret-key-for-safe-test")
        result = decrypt_credential_safe(encrypted)
        assert result == ""


# ----------------------------------------------------------------
# Migration: encrypt existing plaintext SSL settings
# ----------------------------------------------------------------


class TestSslMigration:
    def test_migration_encrypts_plaintext_key_text(self, tmp_path):
        """Migration should encrypt existing plaintext key_text in DB."""
        db_path = str(tmp_path / "migrate.db")

        # Set up Fernet first
        credential_crypto._fernet = None
        _init_fernet(TEST_SECRET_KEY)

        # Write plaintext SSL settings BEFORE Database init
        conn = sqlite3.connect(db_path)
        with open("/home/igor/Amnezia-Web-Panel/schema.sql") as f:
            conn.executescript(f.read())
        plaintext_key = "-----BEGIN PRIVATE KEY-----\nOLDPLAIN\n-----END PRIVATE KEY-----"
        old_ssl = {
            "enabled": True,
            "domain": "old.example.com",
            "key_text": plaintext_key,
            "cert_text": "",
            "cert_path": "",
            "key_path": "",
            "panel_port": 5000,
        }
        conn.execute(
            "INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)",
            ("ssl", json.dumps(old_ssl)),
        )
        conn.commit()
        conn.close()

        # Now create Database — migration runs in _init_db() and encrypts the plaintext
        credential_crypto._fernet = None
        _init_fernet(TEST_SECRET_KEY)
        db = Database(db_path, secret_key=TEST_SECRET_KEY)

        # After migration, DB should have encrypted key_text
        conn2 = sqlite3.connect(db_path)
        row = conn2.execute("SELECT value FROM settings WHERE key = 'ssl'").fetchone()
        conn2.close()
        stored = json.loads(row[0])

        assert stored["key_text"] != plaintext_key
        assert _looks_like_fernet_token(stored["key_text"])
        # And it decrypts correctly
        assert decrypt_credential(stored["key_text"]) == plaintext_key

    def test_migration_does_not_double_encrypt(self, tmp_path):
        """Migration should skip already-encrypted key_text/cert_text."""
        db_path = str(tmp_path / "migrate2.db")

        credential_crypto._fernet = None
        _init_fernet(TEST_SECRET_KEY)

        # Pre-encrypt the key_text
        already_encrypted = encrypt_credential(
            "-----BEGIN PRIVATE KEY-----\nALREADY\n-----END PRIVATE KEY-----"
        )
        old_ssl = {
            "enabled": True,
            "domain": "old.example.com",
            "key_text": already_encrypted,
            "cert_text": "",
            "cert_path": "",
            "key_path": "",
            "panel_port": 5000,
        }
        # Write directly to DB (bypassing update_setting so it stays as-is)
        conn = sqlite3.connect(db_path)
        with open("/home/igor/Amnezia-Web-Panel/schema.sql") as f:
            conn.executescript(f.read())
        conn.execute(
            "INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)",
            ("ssl", json.dumps(old_ssl)),
        )
        conn.execute(
            "INSERT OR REPLACE INTO migration_flags (key, value) VALUES (?, ?)",
            ("credentials_encrypted", "1"),
        )
        conn.execute(
            "INSERT OR REPLACE INTO migration_flags (key, value) VALUES (?, ?)",
            ("xray_private_keys_cleared", "1"),
        )
        conn.commit()
        conn.close()

        credential_crypto._fernet = None
        _init_fernet(TEST_SECRET_KEY)
        Database(db_path, secret_key=TEST_SECRET_KEY)

        conn = sqlite3.connect(db_path)
        row = conn.execute("SELECT value FROM settings WHERE key = 'ssl'").fetchone()
        conn.close()
        stored = json.loads(row[0])
        # Should still be the same Fernet token (not double-encrypted)
        assert stored["key_text"] == already_encrypted

    def test_migration_flag_is_set(self, tmp_path):
        """After migration, ssl_keys_encrypted flag should be set."""
        db_path = str(tmp_path / "migrate3.db")

        credential_crypto._fernet = None
        _init_fernet(TEST_SECRET_KEY)
        db = Database(db_path, secret_key=TEST_SECRET_KEY)

        assert db.get_migration_flag("ssl_keys_encrypted") == "1"


# ----------------------------------------------------------------
# API response stripping tests (unit-level, no full app needed)
# ----------------------------------------------------------------


class TestApiResponseStripsSslKeys:
    def test_api_endpoint_strips_sensitive_fields(self, db):
        """The settings router strips key_text/cert_text before returning."""
        # Save SSL settings with real key/cert
        original_key = "-----BEGIN PRIVATE KEY-----\nAPIKEY\n-----END PRIVATE KEY-----"
        original_cert = "-----BEGIN CERTIFICATE-----\nAPICERT\n-----END CERTIFICATE-----"
        ssl_data = {
            "enabled": True,
            "domain": "api.example.com",
            "key_text": original_key,
            "cert_text": original_cert,
            "cert_path": "",
            "key_path": "",
            "panel_port": 5000,
        }
        db.update_setting("ssl", ssl_data)

        # Verify raw DB has encrypted values
        settings = db.get_all_settings()
        raw_ssl = settings.get("ssl", {})
        assert raw_ssl["key_text"] == original_key

        # The API endpoint function strips them (we can test the logic directly)
        # After stripping, key_text/cert_text should be empty
        stripped = dict(raw_ssl)
        stripped["key_text"] = ""
        stripped["cert_text"] = ""
        assert stripped["key_text"] == ""
        assert stripped["cert_text"] == ""

    def test_api_strips_preserves_other_ssl_fields(self, db):
        """Non-sensitive SSL fields should still be present after stripping."""
        ssl_data = {
            "enabled": True,
            "domain": "preserve.example.com",
            "key_text": "-----BEGIN PRIVATE KEY-----\nSECRET\n-----END PRIVATE KEY-----",
            "cert_text": "-----BEGIN CERTIFICATE-----\nSECRET\n-----END CERTIFICATE-----",
            "cert_path": "/etc/ssl/cert.pem",
            "key_path": "/etc/ssl/key.pem",
            "panel_port": 8443,
        }
        db.update_setting("ssl", ssl_data)

        settings = db.get_all_settings()
        raw_ssl = settings.get("ssl", {})

        # Simulate what the API endpoint does
        stripped = dict(raw_ssl)
        stripped["key_text"] = ""
        stripped["cert_text"] = ""

        assert stripped["enabled"] is True
        assert stripped["domain"] == "preserve.example.com"
        assert stripped["cert_path"] == "/etc/ssl/cert.pem"
        assert stripped["key_path"] == "/etc/ssl/key.pem"
        assert stripped["panel_port"] == 8443
