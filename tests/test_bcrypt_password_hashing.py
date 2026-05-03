"""Tests for bcrypt password hashing (Issue #131 + #145).

Covers:
- New hash_password() produces bcrypt hashes ($2b$...)
- New verify_password() works with bcrypt hashes
- Legacy PBKDF2 hashes still verify correctly (backward compat)
- Wrong passwords rejected for both formats
- Malformed hashes don't crash
"""

import hashlib
import secrets

from app.utils.helpers import hash_password, verify_password

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_legacy_hash(password: str, iterations: int = 100000) -> str:
    """Build a legacy PBKDF2-SHA256 hash for backcompat testing."""
    salt = secrets.token_hex(16)
    h = hashlib.pbkdf2_hmac("sha256", password.encode(), salt.encode(), iterations)
    return f"{salt}${h.hex()}"


# ---------------------------------------------------------------------------
# hash_password tests
# ---------------------------------------------------------------------------


class TestHashPassword:
    def test_produces_bcrypt_format(self):
        """hash_password returns a bcrypt hash starting with $2."""
        h = hash_password("mypassword")
        assert h.startswith("$2"), f"Expected bcrypt format, got: {h[:20]}..."

    def test_bcrypt_hash_verifies_correctly(self):
        """A bcrypt hash produced by hash_password can be verified."""
        h = hash_password("secure123")
        assert verify_password("secure123", h) is True

    def test_bcrypt_hash_rejects_wrong_password(self):
        """A bcrypt hash rejects an incorrect password."""
        h = hash_password("secure123")
        assert verify_password("wrongpass", h) is False

    def test_different_passwords_produce_different_hashes(self):
        """Different passwords get different salts and different hashes."""
        h1 = hash_password("alpha")
        h2 = hash_password("beta")
        assert h1 != h2

    def test_same_password_produces_unique_hashes(self):
        """Same password produces unique hashes (different salts)."""
        h1 = hash_password("samepass")
        h2 = hash_password("samepass")
        assert h1 != h2
        # Both must verify
        assert verify_password("samepass", h1) is True
        assert verify_password("samepass", h2) is True


# ---------------------------------------------------------------------------
# verify_password — bcrypt path
# ---------------------------------------------------------------------------


class TestVerifyPasswordBcrypt:
    def test_correct_password_verifies(self):
        h = hash_password("test")
        assert verify_password("test", h) is True

    def test_wrong_password_rejected(self):
        h = hash_password("test")
        assert verify_password("wrong", h) is False

    def test_empty_password_rejected_when_hash_has_value(self):
        h = hash_password("real")
        assert verify_password("", h) is False

    def test_malformed_bcrypt_hash_returns_false(self):
        """A hash that looks like bcrypt but is garbage does not crash."""
        assert verify_password("test", "$2b$12$notavalidhashstring") is False

    def test_completely_invalid_hash_returns_false(self):
        """Totally bogus hash returns False, not an exception."""
        assert verify_password("test", "notahash") is False


# ---------------------------------------------------------------------------
# verify_password — legacy PBKDF2 path (backward compatibility)
# ---------------------------------------------------------------------------


class TestVerifyPasswordLegacy:
    def test_legacy_hash_verifies_correct_password(self):
        legacy = _make_legacy_hash("oldpassword")
        assert verify_password("oldpassword", legacy) is True

    def test_legacy_hash_rejects_wrong_password(self):
        legacy = _make_legacy_hash("oldpassword")
        assert verify_password("wrongpassword", legacy) is False

    def test_legacy_hash_rejects_empty_password(self):
        legacy = _make_legacy_hash("oldpassword")
        assert verify_password("", legacy) is False

    def test_legacy_hash_with_long_salt_and_hash_works(self):
        """Legacy hashes with unusual but valid salt/hex lengths verify correctly."""
        salt = secrets.token_hex(32)  # 64-char salt
        h = hashlib.pbkdf2_hmac("sha256", b"pwd", salt.encode(), 100000)
        legacy = f"{salt}${h.hex()}"
        assert verify_password("pwd", legacy) is True
        assert verify_password("wrong", legacy) is False

    def test_corrupt_legacy_hash_no_dollar(self):
        """A hash without a $ separator returns False, not an exception."""
        assert verify_password("pwd", "abcdef1234567890") is False

    def test_corrupt_legacy_hash_empty_components(self):
        """A hash with $ but empty components returns False."""
        assert verify_password("pwd", "$") is False


# ---------------------------------------------------------------------------
# Edge cases
# ---------------------------------------------------------------------------


class TestEdgeCases:
    def test_unicode_passwords_work(self):
        """Unicode passwords like emoji work with bcrypt."""
        h = hash_password("пароль🔥")
        assert verify_password("пароль🔥", h) is True
        assert verify_password("пароль", h) is False
