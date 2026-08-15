"""Fernet-based credential encryption for SSH passwords and private keys.

Derives a Fernet key from the application's SECRET_KEY via HKDF-SHA256,
providing deterministic key derivation (same SECRET_KEY → same Fernet key).
All SSH credentials (ssh_pass, ssh_key) are encrypted at rest in the
database and decrypted transparently on read.

Changing SECRET_KEY invalidates all encrypted credentials — decrypt will
raise InvalidToken, which is caught as a clear error rather than silent
corruption.
"""

from __future__ import annotations

import base64
import logging
import re
import sqlite3
from typing import Optional

from cryptography.fernet import Fernet, InvalidToken
from cryptography.hazmat.primitives.hashes import SHA256
from cryptography.hazmat.primitives.kdf.hkdf import HKDF

logger = logging.getLogger(__name__)

# ----------------------------------------------------------------
# Sensitive protocol fields that must NEVER be stored in the DB
# or returned via API responses.
# ----------------------------------------------------------------
SENSITIVE_PROTOCOL_FIELDS: list[str] = ["reality_private_key"]

# Module-level Fernet instance; initialised once at DB init time.
_fernet: Optional[Fernet] = None

# Fernet tokens always start with 'gAAAAA' in base64 — use this to
# distinguish already-encrypted values from plaintext.
_FERNET_TOKEN_RE = re.compile(r"^g[A-Za-z0-9+/=_-]{20,}$")


def _init_fernet(secret_key: str) -> Fernet:
    """Derive a Fernet key from *secret_key* using HKDF-SHA256.

    This is deterministic: the same *secret_key* always yields the same
    Fernet key, which means existing encrypted credentials remain valid
    across restarts.
    """
    hkdf = HKDF(
        algorithm=SHA256(),
        length=32,
        salt=b"amnezia-panel-credential-encryption",
        info=b"fernet-credential-key",
    )
    raw_key = hkdf.derive(secret_key.encode("utf-8"))
    fernet_key = base64.urlsafe_b64encode(raw_key)
    f = Fernet(fernet_key)
    global _fernet
    _fernet = f
    return f


def _get_fernet() -> Fernet:
    """Return the initialised Fernet instance or raise if not initialised."""
    if _fernet is None:
        raise RuntimeError("credential_crypto not initialised — call _init_fernet() first")
    return _fernet


def encrypt_credential(value: str) -> str:
    """Encrypt a plaintext credential string.

    Returns the Fernet ciphertext as a string. Empty/None values are
    returned as empty strings (never encrypt nothing).
    """
    if not value:
        return ""
    f = _get_fernet()
    return f.encrypt(value.encode("utf-8")).decode("utf-8")


def decrypt_credential(value: str) -> str:
    """Decrypt a Fernet ciphertext back to plaintext.

    Raises ValueError with a clear message if the token is invalid
    (e.g. SECRET_KEY changed). Empty/None values return empty string.
    """
    if not value:
        return ""
    f = _get_fernet()
    try:
        return f.decrypt(value.encode("utf-8")).decode("utf-8")
    except InvalidToken:
        raise ValueError(
            "Failed to decrypt credential — SECRET_KEY may have changed "
            "or data is corrupt. Re-enter credentials after verifying SECRET_KEY."
        )


def decrypt_credential_safe(value: str) -> str:
    """Decrypt a credential, returning empty string on failure instead of raising.

    Use this for SSL key/cert decryption where SECRET_KEY rotation should not
    crash the app — keys can be re-entered by the admin.
    """
    try:
        return decrypt_credential(value)
    except (ValueError, InvalidToken):
        logger.warning("Failed to decrypt credential — returning empty string")
        return ""


def _looks_like_fernet_token(value: str) -> bool:
    """Heuristic: does *value* look like a Fernet ciphertext?

    Fernet tokens are base64-encoded and always start with the version
    byte ``0x80`` which encodes as ``g`` in urlsafe base64, followed by
    at least the timestamp (8 bytes = ~11 base64 chars).  Total minimum
    length is ~24 base64 chars (version + timestamp + IV + 0-length
    ciphertext + HMAC).
    """
    if not value:
        return False
    return bool(_FERNET_TOKEN_RE.match(value))


def encrypt_existing_plaintext(db_path: str, secret_key: str) -> None:
    """Migration: encrypt any plaintext ssh_pass / ssh_key values in-place.

    Opens the DB directly, finds rows where ssh_pass or ssh_key are not
    yet Fernet tokens, encrypts them, and writes them back.
    After this, sets the ``credentials_encrypted`` migration flag.
    """
    _init_fernet(secret_key)
    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    try:
        rows = conn.execute("SELECT id, ssh_pass, ssh_key FROM servers").fetchall()
        for row in rows:
            sid = row["id"]
            ssh_pass = row["ssh_pass"] or ""
            ssh_key = row["ssh_key"] or ""
            updates: dict[str, str] = {}
            if ssh_pass and not _looks_like_fernet_token(ssh_pass):
                updates["ssh_pass"] = encrypt_credential(ssh_pass)
            if ssh_key and not _looks_like_fernet_token(ssh_key):
                updates["ssh_key"] = encrypt_credential(ssh_key)
            if updates:
                set_clauses = ", ".join(f"{col} = ?" for col in updates)
                values = list(updates.values()) + [sid]
                conn.execute(f"UPDATE servers SET {set_clauses} WHERE id = ?", values)
                logger.info(
                    "Migration: encrypted credentials for server id=%d (fields: %s)",
                    sid,
                    ", ".join(updates.keys()),
                )
        conn.commit()
        logger.info("Migration: plaintext credential encryption complete")
    except Exception:
        conn.rollback()
        raise
    finally:
        conn.close()


def strip_sensitive_protocol_fields(protocols: dict) -> dict:
    """Return a copy of *protocols* with sensitive fields removed.

    This is used both before storing to DB (defense-in-depth) and before
    returning via API responses.
    """
    if not isinstance(protocols, dict):
        return protocols
    cleaned = {}
    for key, value in protocols.items():
        if isinstance(value, dict):
            # Strip sensitive fields from nested protocol dicts
            inner = {k: v for k, v in value.items() if k not in SENSITIVE_PROTOCOL_FIELDS}
            cleaned[key] = inner
        else:
            cleaned[key] = value
    return cleaned
