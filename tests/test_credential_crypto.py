"""Tests for credential_crypto module — Fernet encryption/decryption helpers."""

from __future__ import annotations

import os
import sqlite3
import tempfile

import pytest

import credential_crypto
from credential_crypto import (
    _init_fernet,
    _looks_like_fernet_token,
    _get_fernet,
    decrypt_credential,
    encrypt_credential,
    encrypt_existing_plaintext,
    strip_sensitive_protocol_fields,
)

# ----------------------------------------------------------------
# Fixtures
# ----------------------------------------------------------------


@pytest.fixture(autouse=True)
def reset_fernet():
    """Reset the module-level Fernet instance before each test."""
    credential_crypto._fernet = None
    yield
    credential_crypto._fernet = None


@pytest.fixture
def secret_key():
    """A stable test secret key."""
    return "test-secret-key-for-credential-encryption-01"


@pytest.fixture
def other_secret_key():
    """A different secret key for invalidation tests."""
    return "completely-different-secret-key-for-testing"


@pytest.fixture
def fernet_init(secret_key):
    """Initialise Fernet with the test secret key."""
    return _init_fernet(secret_key)


# ----------------------------------------------------------------
# _init_fernet tests
# ----------------------------------------------------------------


class TestInitFernet:
    def test_produces_fernet_instance(self, secret_key):
        f = _init_fernet(secret_key)
        from cryptography.fernet import Fernet

        assert isinstance(f, Fernet)

    def test_deterministic_same_key_same_result(self, secret_key):
        f1 = _init_fernet(secret_key)
        credential_crypto._fernet = None
        f2 = _init_fernet(secret_key)
        # Same key → same Fernet instance → can decrypt each other's tokens
        token = f1.encrypt(b"test")
        assert f2.decrypt(token) == b"test"

    def test_sets_module_level_instance(self, secret_key):
        _init_fernet(secret_key)
        assert credential_crypto._fernet is not None


class TestGetFernet:
    def test_raises_if_not_initialised(self):
        with pytest.raises(RuntimeError, match="not initialised"):
            _get_fernet()

    def test_returns_instance_after_init(self, fernet_init):
        f = _get_fernet()
        from cryptography.fernet import Fernet

        assert isinstance(f, Fernet)


# ----------------------------------------------------------------
# encrypt_credential / decrypt_credential tests
# ----------------------------------------------------------------


class TestEncryptDecrypt:
    def test_round_trip(self, fernet_init):
        plaintext = "my-secret-ssh-password"
        encrypted = encrypt_credential(plaintext)
        assert encrypted != plaintext
        assert decrypt_credential(encrypted) == plaintext

    def test_round_trip_private_key(self, fernet_init):
        key = "-----BEGIN OPENSSH PRIVATE KEY-----\nabc123\n-----END OPENSSH PRIVATE KEY-----"
        encrypted = encrypt_credential(key)
        assert decrypt_credential(encrypted) == key

    def test_empty_string_passthrough(self, fernet_init):
        assert encrypt_credential("") == ""
        assert decrypt_credential("") == ""

    def test_none_as_empty(self, fernet_init):
        assert encrypt_credential(None) == ""
        assert decrypt_credential(None) == ""

    def test_ciphertext_starts_with_g(self, fernet_init):
        encrypted = encrypt_credential("test")
        assert encrypted.startswith("g")

    def test_different_plaintexts_different_ciphertexts(self, fernet_init):
        e1 = encrypt_credential("password1")
        e2 = encrypt_credential("password2")
        # Fernet includes IV, so even same plaintext → different ciphertext
        # But here the plaintexts are different anyway
        assert e1 != e2

    def test_same_plaintext_different_ciphertexts(self, fernet_init):
        """Fernet uses random IV, so same plaintext encrypts to different tokens."""
        e1 = encrypt_credential("same-password")
        e2 = encrypt_credential("same-password")
        # Both decrypt to same plaintext but ciphertexts differ
        assert e1 != e2
        assert decrypt_credential(e1) == decrypt_credential(e2)

    def test_secret_key_change_invalidates(self, secret_key, other_secret_key):
        _init_fernet(secret_key)
        encrypted = encrypt_credential("my-credential")
        # Now change the SECRET_KEY
        _init_fernet(other_secret_key)
        with pytest.raises(ValueError, match="SECRET_KEY may have changed"):
            decrypt_credential(encrypted)

    def test_corrupt_token_raises(self, fernet_init):
        with pytest.raises(ValueError, match="SECRET_KEY may have changed"):
            decrypt_credential("gAAAAABnotavalidtokenXXXXXXXXXXXXXXXXXXX==")


# ----------------------------------------------------------------
# _looks_like_fernet_token tests
# ----------------------------------------------------------------


class TestLooksLikeFernetToken:
    def test_empty_string(self):
        assert _looks_like_fernet_token("") is False

    def test_none(self):
        assert _looks_like_fernet_token(None) is False

    def test_plaintext(self):
        assert _looks_like_fernet_token("my-password") is False

    def test_fernet_token(self, fernet_init):
        token = encrypt_credential("test")
        assert _looks_like_fernet_token(token) is True

    def test_short_g_string(self):
        assert _looks_like_fernet_token("gAB") is False


# ----------------------------------------------------------------
# encrypt_existing_plaintext (migration) tests
# ----------------------------------------------------------------


class TestEncryptExistingPlaintext:
    def _create_test_db(self, db_path: str):
        """Create a minimal test DB with servers table."""
        conn = sqlite3.connect(db_path)
        conn.execute(
            """
            CREATE TABLE IF NOT EXISTS servers (
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
            CREATE TABLE IF NOT EXISTS migration_flags (
                key TEXT PRIMARY KEY,
                value TEXT
            )
        """
        )
        conn.commit()
        conn.close()

    def test_encrypts_plaintext_credentials(self, secret_key):
        with tempfile.NamedTemporaryFile(suffix=".db", delete=False) as f:
            db_path = f.name
        try:
            self._create_test_db(db_path)
            conn = sqlite3.connect(db_path)
            # Insert plaintext credentials
            conn.execute(
                "INSERT INTO servers (name, host, ssh_pass, ssh_key) VALUES (?, ?, ?, ?)",
                ("srv1", "1.2.3.4", "plain-password", "plain-key-data"),
            )
            conn.commit()
            conn.close()

            encrypt_existing_plaintext(db_path, secret_key)

            # Verify they are now encrypted
            conn = sqlite3.connect(db_path)
            row = conn.execute("SELECT ssh_pass, ssh_key FROM servers WHERE id = 1").fetchone()
            conn.close()
            assert row[0] != "plain-password"
            assert row[1] != "plain-key-data"
            assert _looks_like_fernet_token(row[0])
            assert _looks_like_fernet_token(row[1])

            # And they decrypt correctly
            _init_fernet(secret_key)
            assert decrypt_credential(row[0]) == "plain-password"
            assert decrypt_credential(row[1]) == "plain-key-data"
        finally:
            os.unlink(db_path)

    def test_skips_already_encrypted(self, secret_key):
        with tempfile.NamedTemporaryFile(suffix=".db", delete=False) as f:
            db_path = f.name
        try:
            self._create_test_db(db_path)
            _init_fernet(secret_key)
            already_encrypted = encrypt_credential("already-enc")
            conn = sqlite3.connect(db_path)
            conn.execute(
                "INSERT INTO servers (name, host, ssh_pass, ssh_key) VALUES (?, ?, ?, ?)",
                ("srv1", "1.2.3.4", already_encrypted, ""),
            )
            conn.commit()
            conn.close()

            # Reset and re-run migration — should not double-encrypt
            credential_crypto._fernet = None
            encrypt_existing_plaintext(db_path, secret_key)

            conn = sqlite3.connect(db_path)
            row = conn.execute("SELECT ssh_pass FROM servers WHERE id = 1").fetchone()
            conn.close()
            _init_fernet(secret_key)
            assert decrypt_credential(row[0]) == "already-enc"
        finally:
            os.unlink(db_path)

    def test_empty_values_not_encrypted(self, secret_key):
        with tempfile.NamedTemporaryFile(suffix=".db", delete=False) as f:
            db_path = f.name
        try:
            self._create_test_db(db_path)
            conn = sqlite3.connect(db_path)
            conn.execute(
                "INSERT INTO servers (name, host, ssh_pass, ssh_key) VALUES (?, ?, ?, ?)",
                ("srv1", "1.2.3.4", "", ""),
            )
            conn.commit()
            conn.close()

            encrypt_existing_plaintext(db_path, secret_key)

            conn = sqlite3.connect(db_path)
            row = conn.execute("SELECT ssh_pass, ssh_key FROM servers WHERE id = 1").fetchone()
            conn.close()
            # Empty values should remain empty (not encrypted to a token)
            assert row[0] == ""
            assert row[1] == ""
        finally:
            os.unlink(db_path)


# ----------------------------------------------------------------
# strip_sensitive_protocol_fields tests
# ----------------------------------------------------------------


class TestStripSensitiveProtocolFields:
    def test_strips_reality_private_key(self):
        protocols = {
            "xray": {
                "installed": True,
                "port": 443,
                "reality_private_key": "super-secret-key",
                "public_key": "pub-key-123",
            }
        }
        result = strip_sensitive_protocol_fields(protocols)
        assert "reality_private_key" not in result["xray"]
        assert result["xray"]["public_key"] == "pub-key-123"
        assert result["xray"]["installed"] is True

    def test_preserves_non_sensitive_fields(self):
        protocols = {
            "awg": {"installed": True, "port": 51820},
            "xray": {"installed": True, "port": 443, "public_key": "abc"},
        }
        result = strip_sensitive_protocol_fields(protocols)
        assert result == protocols

    def test_empty_dict(self):
        assert strip_sensitive_protocol_fields({}) == {}

    def test_non_dict_passthrough(self):
        assert strip_sensitive_protocol_fields("not a dict") == "not a dict"

    def test_does_not_mutate_original(self):
        original = {"xray": {"reality_private_key": "secret", "port": 443}}
        result = strip_sensitive_protocol_fields(original)
        assert "reality_private_key" in original["xray"]
        assert "reality_private_key" not in result["xray"]

    def test_multiple_sensitive_fields(self):
        """If SENSITIVE_PROTOCOL_FIELDS is expanded in the future."""
        protocols = {"xray": {"secret1": "a", "reality_private_key": "b", "port": 443}}
        result = strip_sensitive_protocol_fields(protocols)
        assert "reality_private_key" not in result["xray"]
