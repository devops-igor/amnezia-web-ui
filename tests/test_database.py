"""
Quick tests for database delete_server fix.
See: tasks/batch-2c-critical-bugs
"""

import tempfile
import os
from app.core.database import Database


class TestDeleteServer:
    """Tests for delete_server (replaces delete_server_by_index)."""

    def setup_method(self):
        self.tmp_db = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        self.tmp_db_path = self.tmp_db.name
        self.tmp_db.close()
        self.db = Database(self.tmp_db_path)

    def teardown_method(self):
        self.db._get_conn().close()
        os.unlink(self.tmp_db_path)

    def test_delete_middle_server_preserves_ids(self):
        """Create 3 servers, delete middle one, verify remaining IDs unchanged."""
        self.db.create_server({"name": "Server 1", "host": "host1.example.com", "protocols": {}})
        self.db.create_server({"name": "Server 2", "host": "host2.example.com", "protocols": {}})
        self.db.create_server({"name": "Server 3", "host": "host3.example.com", "protocols": {}})

        servers = self.db.get_all_servers()
        assert len(servers) == 3
        ids = [s["id"] for s in servers]

        # Delete the middle server by ID
        result = self.db.delete_server(ids[1])
        assert result is True

        remaining = self.db.get_all_servers()
        assert len(remaining) == 2
        remaining_ids = [s["id"] for s in remaining]
        assert ids[0] in remaining_ids
        assert ids[1] not in remaining_ids
        assert ids[2] in remaining_ids

    def test_delete_nonexistent_server_returns_false(self):
        """Deleting a non-existent server should return False."""
        result = self.db.delete_server(99999)
        assert result is False


class TestExpiresAtMigration:
    """Tests for the expires_at column migration."""

    def test_migration_without_expiration_date_column(self):
        """Old database without expiration_date column migrates cleanly without errors."""
        import sqlite3

        tmp = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        db_path = tmp.name
        tmp.close()
        try:
            conn = sqlite3.connect(db_path)
            conn.execute("""CREATE TABLE users (
                    id TEXT PRIMARY KEY,
                    username TEXT NOT NULL,
                    password_hash TEXT,
                    role TEXT NOT NULL DEFAULT 'user',
                    enabled INTEGER NOT NULL DEFAULT 1
                )""")
            conn.execute("INSERT INTO users (id, username) VALUES ('u1', 'alice')")
            conn.commit()
            conn.close()

            db = Database(db_path)
            user = db.get_user("u1")
            assert user is not None
            assert user.get("expires_at") is None
            conn = db._get_conn()
            conn.close()
        finally:
            if os.path.exists(db_path):
                os.unlink(db_path)

    def test_migration_with_expiration_date_column(self):
        """Database with expiration_date column copies values to expires_at."""
        import sqlite3

        tmp = tempfile.NamedTemporaryFile(suffix=".db", delete=False)
        db_path = tmp.name
        tmp.close()
        try:
            conn = sqlite3.connect(db_path)
            conn.execute("""CREATE TABLE users (
                    id TEXT PRIMARY KEY,
                    username TEXT NOT NULL,
                    expiration_date TEXT,
                    role TEXT NOT NULL DEFAULT 'user',
                    enabled INTEGER NOT NULL DEFAULT 1
                )""")
            conn.execute(
                "INSERT INTO users (id, username, expiration_date) VALUES ('u2', 'bob', '2026-12-31T23:59:59')"
            )
            conn.commit()
            conn.close()

            db = Database(db_path)
            user = db.get_user("u2")
            assert user is not None
            assert user.get("expires_at") == "2026-12-31T23:59:59"
            conn = db._get_conn()
            conn.close()
        finally:
            if os.path.exists(db_path):
                os.unlink(db_path)
