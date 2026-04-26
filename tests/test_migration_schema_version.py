"""Tests for schema version tracking and migration input validation."""

import json
import os
import shutil
import tempfile

import pytest

from database import Database
from migrate_to_sqlite import _validate_data, migrate_data_json_to_sqlite, migrate_if_needed


class TestSchemaVersion:
    """Tests for Database.SCHEMA_VERSION and get/set_schema_version."""

    def setup_method(self):
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()

    def teardown_method(self):
        try:
            os.unlink(self.tmp_db_path)
        except FileNotFoundError:
            pass

    def test_new_database_has_schema_version(self):
        """Fresh database gets SCHEMA_VERSION set in settings."""
        db = Database(self.tmp_db_path)
        assert db.get_schema_version() == Database.SCHEMA_VERSION

    def test_schema_version_persists(self):
        """Schema version survives re-opening the database."""
        db = Database(self.tmp_db_path)
        assert db.get_schema_version() == 1

        db2 = Database(self.tmp_db_path)
        assert db2.get_schema_version() == 1

    def test_set_schema_version_overrides(self):
        """set_schema_version can bump the version."""
        db = Database(self.tmp_db_path)
        db.set_schema_version(42)
        assert db.get_schema_version() == 42

    def test_get_schema_version_zero_when_missing(self):
        """If schema_version row is absent, get_schema_version returns 0."""
        db = Database(self.tmp_db_path)
        # Remove the setting
        conn = db._get_conn()
        conn.execute("DELETE FROM settings WHERE key = 'schema_version'")
        conn.commit()
        assert db.get_schema_version() == 0

    def test_get_schema_version_zero_on_bad_value(self):
        """Non-integer value in settings returns 0."""
        db = Database(self.tmp_db_path)
        conn = db._get_conn()
        conn.execute(
            "INSERT OR REPLACE INTO settings (key, value) VALUES ('schema_version', ?)",
            ("not-a-number",),
        )
        conn.commit()
        assert db.get_schema_version() == 0

    def test_schema_version_constant_is_int(self):
        """SCHEMA_VERSION is a positive int."""
        assert isinstance(Database.SCHEMA_VERSION, int)
        assert Database.SCHEMA_VERSION >= 1


class TestValidateData:
    """Tests for _validate_data."""

    def test_valid_dict_returns_empty(self):
        """A well-formed data dict produces no errors."""
        data = {
            "servers": [{"name": "srv1", "host": "1.2.3.4"}],
            "users": [{"id": "u1", "username": "alice"}],
        }
        assert _validate_data(data) == []

    def test_missing_server_key(self):
        """Server missing name or host is flagged."""
        data = {
            "servers": [{"name": "srv1"}],
            "users": [{"id": "u1", "username": "alice"}],
        }
        errors = _validate_data(data)
        assert any("missing keys" in e for e in errors)

    def test_server_not_a_dict(self):
        """Non-dict server entry is flagged."""
        data = {
            "servers": ["not-a-dict"],
            "users": [{"id": "u1", "username": "alice"}],
        }
        errors = _validate_data(data)
        assert any("servers[0] is not a dict" in e for e in errors)

    def test_missing_user_key(self):
        """User missing id or username is flagged."""
        data = {
            "servers": [{"name": "srv1", "host": "1.2.3.4"}],
            "users": [{"id": "u1"}],
        }
        errors = _validate_data(data)
        assert any("missing keys" in e for e in errors)

    def test_user_not_a_dict(self):
        """Non-dict user entry is flagged."""
        data = {
            "servers": [{"name": "srv1", "host": "1.2.3.4"}],
            "users": [42],
        }
        errors = _validate_data(data)
        assert any("users[0] is not a dict" in e for e in errors)

    def test_data_not_a_dict(self):
        """Top-level non-dict data is flagged."""
        errors = _validate_data("not-a-dict")
        assert errors == ["data is not a dict"]

    def test_empty_lists_are_valid(self):
        """Empty servers/users lists are fine."""
        data = {"servers": [], "users": []}
        assert _validate_data(data) == []


class TestMigrateDataJsonToSqlite:
    """Tests for migrate_data_json_to_sqlite."""

    def setup_method(self):
        self.tmp_dir = tempfile.mkdtemp()
        self.data_file = os.path.join(self.tmp_dir, "data.json")
        self.db_path = os.path.join(self.tmp_dir, "panel.db")

    def teardown_method(self):
        for f in [self.data_file, self.db_path, self.data_file + ".bak"]:
            try:
                os.unlink(f)
            except FileNotFoundError:
                pass
        shutil.rmtree(self.tmp_dir, ignore_errors=True)

    def test_successful_migration(self):
        """Happy path: data.json → panel.db + backup."""
        data = {
            "servers": [{"name": "srv1", "host": "1.2.3.4", "protocols": {}}],
            "users": [{"id": "u1", "username": "alice", "role": "user"}],
        }
        with open(self.data_file, "w", encoding="utf-8") as f:
            json.dump(data, f)

        migrate_data_json_to_sqlite(self.data_file, self.db_path)

        assert os.path.exists(self.db_path)
        assert os.path.exists(self.data_file + ".bak")
        assert not os.path.exists(self.data_file)

        db = Database(self.db_path)
        assert db.get_schema_version() == Database.SCHEMA_VERSION
        assert len(db.get_all_servers()) == 1
        assert len(db.get_all_users()) == 1

    def test_migration_with_invalid_data_raises(self):
        """Invalid data.json raises ValueError and leaves DB untouched."""
        data = {
            "servers": [{"host": "1.2.3.4"}],  # missing "name"
            "users": [],
        }
        with open(self.data_file, "w", encoding="utf-8") as f:
            json.dump(data, f)

        with pytest.raises(ValueError, match="Invalid data.json"):
            migrate_data_json_to_sqlite(self.data_file, self.db_path)

        assert not os.path.exists(self.db_path)
        assert os.path.exists(self.data_file)  # untouched

    def test_partial_db_removed_before_migration(self):
        """Stale panel.db from a previous failed run is removed first."""
        # Create a stale DB
        open(self.db_path, "w").close()

        data = {
            "servers": [{"name": "srv1", "host": "1.2.3.4", "protocols": {}}],
            "users": [{"id": "u1", "username": "alice", "role": "user"}],
        }
        with open(self.data_file, "w", encoding="utf-8") as f:
            json.dump(data, f)

        migrate_data_json_to_sqlite(self.data_file, self.db_path)

        # Should be a valid DB now
        db = Database(self.db_path)
        assert db.get_schema_version() == Database.SCHEMA_VERSION

    def test_failed_migration_cleans_up_db(self):
        """If save_data raises, the partial DB is removed."""
        data = {
            "servers": [{"name": "srv1", "host": "1.2.3.4"}],
            "users": [{"id": "u1", "username": "alice", "role": "user"}],
        }
        with open(self.data_file, "w", encoding="utf-8") as f:
            json.dump(data, f)

        # Corrupt the file so JSON.load succeeds but something later breaks
        # We simulate by monkey-patching Database.save_data
        original_save_data = Database.save_data

        def bad_save_data(self, data):
            raise RuntimeError("boom")

        Database.save_data = bad_save_data
        try:
            with pytest.raises(RuntimeError, match="boom"):
                migrate_data_json_to_sqlite(self.data_file, self.db_path)
            assert not os.path.exists(self.db_path)
            assert os.path.exists(self.data_file)
        finally:
            Database.save_data = original_save_data

    def test_migrate_if_needed_skips_when_db_exists(self):
        """migrate_if_needed is a no-op when panel.db already exists."""
        open(self.db_path, "w").close()
        # Should not raise
        migrate_if_needed(self.tmp_dir)

    def test_migrate_if_needed_skips_when_no_data_json(self):
        """migrate_if_needed is a no-op when data.json is absent."""
        # Should not raise
        migrate_if_needed(self.tmp_dir)
