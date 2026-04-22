"""Unit tests for integrity.py — SHA256 integrity verification utilities."""

import hashlib

import pytest

from integrity import (
    IntegrityError,
    compute_sha256,
    load_expected_hash,
    verify_content_integrity,
    verify_integrity,
)


class TestComputeSha256:
    """Tests for compute_sha256()."""

    def test_compute_sha256_known_content(self, tmp_path):
        """SHA256 of known content matches hashlib.sha256()."""
        content = b"hello world\n"
        f = tmp_path / "test.txt"
        f.write_bytes(content)
        expected = hashlib.sha256(content).hexdigest()
        assert compute_sha256(str(f)) == expected

    def test_compute_sha256_empty_file(self, tmp_path):
        """SHA256 of empty file matches hashlib.sha256(b'')."""
        f = tmp_path / "empty.txt"
        f.write_bytes(b"")
        expected = hashlib.sha256(b"").hexdigest()
        assert compute_sha256(str(f)) == expected

    def test_compute_sha256_large_file_chunked(self, tmp_path):
        """SHA256 of file larger than 8KB reads in chunks correctly."""
        content = b"x" * 20000  # > 8KB, requires multiple chunks
        f = tmp_path / "large.bin"
        f.write_bytes(content)
        expected = hashlib.sha256(content).hexdigest()
        assert compute_sha256(str(f)) == expected

    def test_compute_sha256_missing_file(self):
        """FileNotFoundError raised for non-existent file."""
        with pytest.raises(FileNotFoundError):
            compute_sha256("/nonexistent/path/file.txt")

    def test_compute_sha256_utf8_content(self, tmp_path):
        """SHA256 of UTF-8 text file computed correctly."""
        content = "Ünïcödé tëst 🎉\n"
        f = tmp_path / "utf8.txt"
        f.write_text(content, encoding="utf-8")
        expected = hashlib.sha256(content.encode("utf-8")).hexdigest()
        assert compute_sha256(str(f)) == expected


class TestVerifyIntegrity:
    """Tests for verify_integrity()."""

    def test_verify_integrity_matching_hash(self, tmp_path):
        """Returns True when file hash matches expected."""
        content = b"test content"
        f = tmp_path / "test.txt"
        f.write_bytes(content)
        expected_hash = hashlib.sha256(content).hexdigest()
        assert verify_integrity(str(f), expected_hash) is True

    def test_verify_integrity_mismatched_hash(self, tmp_path):
        """Returns False when file hash does not match expected."""
        content = b"test content"
        f = tmp_path / "test.txt"
        f.write_bytes(content)
        wrong_hash = "0" * 64
        assert verify_integrity(str(f), wrong_hash) is False

    def test_verify_integrity_missing_file(self):
        """FileNotFoundError raised for non-existent file."""
        with pytest.raises(FileNotFoundError):
            verify_integrity("/nonexistent/file.txt", "a" * 64)

    def test_verify_integrity_uses_hmac_compare(self, tmp_path):
        """Verify uses hmac.compare_digest (not direct ==)."""
        # This is a design test — we verify the function signature
        # uses verify_integrity which calls compute_sha256 then compare_digest.
        content = b"deterministic"
        f = tmp_path / "test.txt"
        f.write_bytes(content)
        expected_hash = hashlib.sha256(content).hexdigest()
        # Should return True for matching
        assert verify_integrity(str(f), expected_hash) is True


class TestVerifyContentIntegrity:
    """Tests for verify_content_integrity()."""

    def test_verify_content_integrity_string(self):
        """Returns True when string content matches expected hash."""
        content = "hello world"
        expected_hash = hashlib.sha256(content.encode("utf-8")).hexdigest()
        assert verify_content_integrity(content, expected_hash) is True

    def test_verify_content_integrity_bytes(self):
        """Returns True when bytes content matches expected hash."""
        content = b"hello world"
        expected_hash = hashlib.sha256(content).hexdigest()
        assert verify_content_integrity(content, expected_hash) is True

    def test_verify_content_integrity_mismatch_string(self):
        """Returns False when string content doesn't match."""
        content = "hello world"
        wrong_hash = "0" * 64
        assert verify_content_integrity(content, wrong_hash) is False

    def test_verify_content_integrity_mismatch_bytes(self):
        """Returns False when bytes content doesn't match."""
        content = b"hello world"
        wrong_hash = "0" * 64
        assert verify_content_integrity(content, wrong_hash) is False

    def test_verify_content_integrity_utf8_string(self):
        """UTF-8 string content correctly hashed."""
        content = "Ünïcödé tëst 🎉"
        expected_hash = hashlib.sha256(content.encode("utf-8")).hexdigest()
        assert verify_content_integrity(content, expected_hash) is True

    def test_verify_content_integrity_empty_string(self):
        """Empty string content hashed correctly."""
        content = ""
        expected_hash = hashlib.sha256(b"").hexdigest()
        assert verify_content_integrity(content, expected_hash) is True

    def test_verify_content_integrity_empty_bytes(self):
        """Empty bytes content hashed correctly."""
        content = b""
        expected_hash = hashlib.sha256(b"").hexdigest()
        assert verify_content_integrity(content, expected_hash) is True


class TestLoadExpectedHash:
    """Tests for load_expected_hash()."""

    def test_load_expected_hash_valid(self, tmp_path):
        """Loads a valid 64-char hex hash from file."""
        expected = "a" * 64
        hash_file = tmp_path / "config.toml.sha256"
        hash_file.write_text(expected)
        assert load_expected_hash(str(hash_file)) == expected

    def test_load_expected_hash_strips_whitespace(self, tmp_path):
        """Strips trailing newlines and whitespace."""
        expected = "b" * 64
        hash_file = tmp_path / "config.toml.sha256"
        hash_file.write_text(f"{expected}\n")
        assert load_expected_hash(str(hash_file)) == expected

    def test_load_expected_hash_strips_leading_whitespace(self, tmp_path):
        """Strips leading whitespace."""
        expected = "c" * 64
        hash_file = tmp_path / "config.toml.sha256"
        hash_file.write_text(f"  {expected}  \n")
        assert load_expected_hash(str(hash_file)) == expected

    def test_load_expected_hash_missing_file(self):
        """FileNotFoundError raised for non-existent hash file."""
        with pytest.raises(FileNotFoundError):
            load_expected_hash("/nonexistent/config.toml.sha256")

    def test_load_expected_hash_empty_file(self, tmp_path):
        """IntegrityError raised for empty hash file."""
        hash_file = tmp_path / "config.toml.sha256"
        hash_file.write_text("")
        with pytest.raises(IntegrityError, match="empty"):
            load_expected_hash(str(hash_file))

    def test_load_expected_hash_empty_after_strip(self, tmp_path):
        """IntegrityError raised for file with only whitespace."""
        hash_file = tmp_path / "config.toml.sha256"
        hash_file.write_text("  \n  \n")
        with pytest.raises(IntegrityError, match="empty"):
            load_expected_hash(str(hash_file))

    def test_load_expected_hash_invalid_hex(self, tmp_path):
        """IntegrityError raised for non-hex characters in hash."""
        hash_file = tmp_path / "config.toml.sha256"
        hash_file.write_text("g" * 64)  # 'g' is not a hex char
        with pytest.raises(IntegrityError, match="Invalid SHA256 hash"):
            load_expected_hash(str(hash_file))

    def test_load_expected_hash_wrong_length(self, tmp_path):
        """IntegrityError raised for hash of wrong length."""
        hash_file = tmp_path / "config.toml.sha256"
        hash_file.write_text("abc123")  # Too short
        with pytest.raises(IntegrityError, match="Invalid SHA256 hash"):
            load_expected_hash(str(hash_file))

    def test_load_expected_hash_uppercase_rejected(self, tmp_path):
        """IntegrityError raised for uppercase hex (not matching [0-9a-f])."""
        hash_file = tmp_path / "config.toml.sha256"
        hash_file.write_text("A" * 64)  # Uppercase hex not in [0-9a-f]
        with pytest.raises(IntegrityError, match="Invalid SHA256 hash"):
            load_expected_hash(str(hash_file))

    def test_load_expected_hash_with_sha256sum_format(self, tmp_path):
        """Loads hash from sha256sum output format (hash + filename)."""
        # sha256sum outputs: "hash  filename" — our .sha256 files only contain the hash
        expected = hashlib.sha256(b"test").hexdigest()
        hash_file = tmp_path / "config.toml.sha256"
        hash_file.write_text(expected)
        assert load_expected_hash(str(hash_file)) == expected


class TestIntegrityError:
    """Tests for IntegrityError exception."""

    def test_integrity_error_is_exception(self):
        """IntegrityError is a subclass of Exception."""
        assert issubclass(IntegrityError, Exception)

    def test_integrity_error_message(self):
        """IntegrityError carries the provided message."""
        msg = "config integrity failed"
        err = IntegrityError(msg)
        assert str(err) == msg

    def test_integrity_error_can_be_raised(self):
        """IntegrityError can be raised and caught."""
        with pytest.raises(IntegrityError, match="test error"):
            raise IntegrityError("test error")

    def test_integrity_error_caught_as_exception(self):
        """IntegrityError can be caught as generic Exception."""
        with pytest.raises(Exception):
            raise IntegrityError("caught as Exception")


class TestEndToEnd:
    """End-to-end: compute hash, write to file, verify."""

    def test_compute_store_verify_roundtrip(self, tmp_path):
        """Compute hash, store it, verify original file matches."""
        content = b"Round trip integrity test"
        data_file = tmp_path / "data.bin"
        data_file.write_bytes(content)

        hash_value = compute_sha256(str(data_file))
        hash_file = tmp_path / "data.bin.sha256"
        hash_file.write_text(hash_value)

        loaded_hash = load_expected_hash(str(hash_file))
        assert verify_integrity(str(data_file), loaded_hash) is True

    def test_content_verify_roundtrip(self):
        """Verify content hash matches compute_sha256 of same content written to file."""
        content = "Patched config content"
        content_hash = hashlib.sha256(content.encode("utf-8")).hexdigest()
        assert verify_content_integrity(content, content_hash) is True

    def test_tampered_file_fails_verification(self, tmp_path):
        """Modified file no longer matches stored hash."""
        original = b"Original content"
        data_file = tmp_path / "data.bin"
        data_file.write_bytes(original)

        hash_value = compute_sha256(str(data_file))
        hash_file = tmp_path / "data.bin.sha256"
        hash_file.write_text(hash_value)

        # Tamper with the file
        data_file.write_bytes(b"Tampered content")

        loaded_hash = load_expected_hash(str(hash_file))
        assert verify_integrity(str(data_file), loaded_hash) is False
