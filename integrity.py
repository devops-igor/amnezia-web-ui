"""SHA256 integrity verification for template files.

Prevents supply-chain attacks by verifying that local config templates
match their expected hashes before uploading to remote servers.
"""

from __future__ import annotations

import hashlib
import hmac
from pathlib import Path


class IntegrityError(Exception):
    """Raised when file integrity verification fails."""

    pass


def compute_sha256(file_path: str) -> str:
    """Compute SHA256 hex digest of a file.

    Reads the file in 8KB chunks to avoid loading the entire file
    into memory.

    Args:
        file_path: Path to the file to hash.

    Returns:
        64-character lowercase hex string.

    Raises:
        FileNotFoundError: If the file does not exist.
    """
    sha256 = hashlib.sha256()
    with open(file_path, "rb") as f:
        while True:
            chunk = f.read(8192)
            if not chunk:
                break
            sha256.update(chunk)
    return sha256.hexdigest()


def verify_integrity(file_path: str, expected_hash: str) -> bool:
    """Verify that a file's SHA256 matches an expected hash.

    Uses hmac.compare_digest for timing-safe comparison.

    Args:
        file_path: Path to the file to verify.
        expected_hash: Expected 64-character hex SHA256 digest.

    Returns:
        True if the file's hash matches, False otherwise.

    Raises:
        FileNotFoundError: If the file does not exist.
    """
    actual_hash = compute_sha256(file_path)
    return hmac.compare_digest(actual_hash, expected_hash)


def verify_content_integrity(content: str | bytes, expected_hash: str) -> bool:
    """Verify that in-memory content matches an expected SHA256 hash.

    Uses hmac.compare_digest for timing-safe comparison.

    Args:
        content: The content to hash. Can be str (UTF-8 encoded) or bytes.
        expected_hash: Expected 64-character hex SHA256 digest.

    Returns:
        True if the content's hash matches, False otherwise.
    """
    if isinstance(content, str):
        content = content.encode("utf-8")
    actual_hash = hashlib.sha256(content).hexdigest()
    return hmac.compare_digest(actual_hash, expected_hash)


def load_expected_hash(hash_file_path: str) -> str:
    """Load an expected SHA256 hash from a .sha256 file.

    Strips whitespace and newlines from the hash.

    Args:
        hash_file_path: Path to the .sha256 file.

    Returns:
        The 64-character hex hash string.

    Raises:
        FileNotFoundError: If the hash file does not exist.
        IntegrityError: If the hash file is empty or contains
            an invalid hash.
    """
    path = Path(hash_file_path)
    if not path.exists():
        raise FileNotFoundError(f"Hash file not found: {hash_file_path}")

    hash_value = path.read_text().strip()

    if not hash_value:
        raise IntegrityError(f"Hash file is empty: {hash_file_path}")

    if len(hash_value) != 64 or not all(c in "0123456789abcdef" for c in hash_value):
        raise IntegrityError(f"Invalid SHA256 hash in {hash_file_path}: {hash_value!r}")

    return hash_value
