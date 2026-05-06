"""Tests for connection pool in database.py.

Verifies:
- Database() init creates a queue.Queue pool, not thread-local
- Connections are returned to the pool after use
- Broken connections are discarded, not returned to pool
- Pool doesn't grow beyond POOL_SIZE
- Concurrent access doesn't cause "database is locked" errors
- Existing database tests continue to work
"""

from __future__ import annotations

import queue
import sqlite3
import threading

import pytest

from database import Database

# ----------------------------------------------------------------
# Fixtures
# ----------------------------------------------------------------


@pytest.fixture
def db_no_secret(tmp_path):
    """Create a temporary Database without encryption (simpler for pool tests)."""
    db_path = str(tmp_path / "pool_test.db")
    database = Database(db_path)
    yield database


def _drain_pool(db):
    """Close and remove all connections from the pool so tests start clean."""
    while True:
        try:
            conn = db._pool.get_nowait()
            conn.close()
        except queue.Empty:
            break


# ----------------------------------------------------------------
# Pool creation tests
# ----------------------------------------------------------------


class TestPoolCreation:
    def test_pool_is_queue_not_thread_local(self, db_no_secret):
        """Database.__init__ should create a queue.Queue, not threading.local."""
        assert isinstance(db_no_secret._pool, queue.Queue)
        assert not hasattr(db_no_secret, "_local")

    def test_pool_has_maxsize(self, db_no_secret):
        """Pool should have a maxsize matching POOL_SIZE."""
        assert db_no_secret._pool.maxsize == Database.POOL_SIZE

    def test_pool_starts_empty_after_drain(self, db_no_secret):
        """After draining init-time connections, pool should be empty."""
        _drain_pool(db_no_secret)
        assert db_no_secret._pool.qsize() == 0


# ----------------------------------------------------------------
# Connection acquisition and return
# ----------------------------------------------------------------


class TestConnectionAcquisition:
    def test_get_conn_returns_sqlite_connection(self, db_no_secret):
        """_get_conn() should return a sqlite3.Connection."""
        _drain_pool(db_no_secret)
        conn = db_no_secret._get_conn()
        assert isinstance(conn, sqlite3.Connection)
        db_no_secret._return_conn(conn)

    def test_connection_is_reused(self, db_no_secret):
        """Same connection object should be returned after return to pool."""
        _drain_pool(db_no_secret)
        conn1 = db_no_secret._get_conn()
        db_no_secret._return_conn(conn1)

        conn2 = db_no_secret._get_conn()
        assert conn2 is conn1  # Same object identity
        db_no_secret._return_conn(conn2)

    def test_multiple_connections_round_robin(self, db_no_secret):
        """Two connections acquired and returned are correctly reused."""
        _drain_pool(db_no_secret)
        conn_a = db_no_secret._get_conn()
        conn_b = db_no_secret._get_conn()
        assert conn_a is not conn_b

        db_no_secret._return_conn(conn_a)
        db_no_secret._return_conn(conn_b)

        conn_c = db_no_secret._get_conn()
        conn_d = db_no_secret._get_conn()

        # Should be the same two connections (FIFO order: a returned first, a comes first)
        assert conn_c is conn_a
        assert conn_d is conn_b

        db_no_secret._return_conn(conn_c)
        db_no_secret._return_conn(conn_d)

    def test_pool_respects_max_size(self, db_no_secret):
        """Pool should not hold more than POOL_SIZE connections."""
        _drain_pool(db_no_secret)

        conns = []
        for _ in range(Database.POOL_SIZE):
            c = db_no_secret._get_conn()
            conns.append(c)

        # Return them all
        for c in conns:
            db_no_secret._return_conn(c)

        # Pool should be full
        assert db_no_secret._pool.qsize() == Database.POOL_SIZE

        # Returning one more should discard it (pool.full())
        extra = sqlite3.connect(db_no_secret.db_path)
        extra.row_factory = sqlite3.Row
        db_no_secret._return_conn(extra)

        # Pool stays at POOL_SIZE (extra was closed, not added)
        assert db_no_secret._pool.qsize() == Database.POOL_SIZE

        # Clean up — drain pool
        _drain_pool(db_no_secret)


# ----------------------------------------------------------------
# Broken connection handling
# ----------------------------------------------------------------


class TestBrokenConnectionHandling:
    def test_context_manager_discards_on_exception(self, db_no_secret):
        """_connection() context manager should close connection on exception."""
        _drain_pool(db_no_secret)
        with pytest.raises(ValueError):
            with db_no_secret._connection() as conn:
                raise ValueError("simulated error")

        # Should not be in pool (discarded)
        assert db_no_secret._pool.qsize() == 0

    def test_context_manager_returns_on_success(self, db_no_secret):
        """_connection() should return connection to pool on normal exit."""
        _drain_pool(db_no_secret)
        with db_no_secret._connection() as conn:
            conn.execute("SELECT 1")

        assert db_no_secret._pool.qsize() == 1

        # Clean up
        _drain_pool(db_no_secret)


# ----------------------------------------------------------------
# Context manager usage in actual methods
# ----------------------------------------------------------------


class TestContextManagerInMethods:
    def test_get_all_servers_returns_connection(self, db_no_secret):
        """After get_all_servers(), connection should be back in pool."""
        _drain_pool(db_no_secret)
        servers = db_no_secret.get_all_servers()
        assert isinstance(servers, list)
        assert db_no_secret._pool.qsize() == 1
        _drain_pool(db_no_secret)

    def test_get_setting_returns_connection(self, db_no_secret):
        """After get_setting(), connection should be back in pool."""
        _drain_pool(db_no_secret)
        result = db_no_secret.get_setting("nonexistent", default=42)
        assert result == 42
        assert db_no_secret._pool.qsize() == 1
        _drain_pool(db_no_secret)

    def test_update_setting_returns_connection(self, db_no_secret):
        """After update_setting(), connection should be back in pool."""
        _drain_pool(db_no_secret)
        db_no_secret.update_setting("test_key", "test_value")
        assert db_no_secret._pool.qsize() == 1
        _drain_pool(db_no_secret)

    def test_create_server_returns_connection(self, db_no_secret):
        """After create_server(), connection should be back in pool."""
        _drain_pool(db_no_secret)
        db_no_secret.create_server({"name": "srv", "host": "1.2.3.4", "protocols": {}})
        assert db_no_secret._pool.qsize() == 1
        _drain_pool(db_no_secret)


# ----------------------------------------------------------------
# Concurrent access tests
# ----------------------------------------------------------------


class TestConcurrentAccess:
    def test_concurrent_reads_no_exceptions(self, db_no_secret):
        """Multiple threads reading simultaneously should not error."""
        # Insert some data first
        db_no_secret.create_server(
            {"name": "srv", "host": "1.2.3.4", "username": "root", "protocols": {}}
        )

        errors = []
        results = []

        def reader():
            try:
                for _ in range(10):
                    servers = db_no_secret.get_all_servers()
                    assert len(servers) >= 1
                    results.append(1)
            except Exception as e:
                errors.append(e)

        threads = [threading.Thread(target=reader) for _ in range(5)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert len(errors) == 0, f"Errors: {errors}"
        assert len(results) == 50  # 5 threads x 10 iterations

    def test_concurrent_write_no_lock_errors(self, db_no_secret):
        """Multiple threads writing should not cause 'database is locked'."""
        errors = []

        def writer(i):
            try:
                db_no_secret.update_setting(f"key_{i}_{threading.get_ident()}", f"value_{i}")
            except sqlite3.OperationalError as e:
                if "locked" in str(e).lower():
                    errors.append(e)

        threads = [threading.Thread(target=writer, args=(i,)) for i in range(10)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert len(errors) == 0, f"Lock errors: {errors}"


# ----------------------------------------------------------------
# Existing tests compatibility
# ----------------------------------------------------------------


class TestExistingDatabaseOperations:
    """Verify that basic DB operations still work with the pool."""

    def test_create_and_read_server(self, db_no_secret):
        """Full server CRUD cycle works."""
        srv_id = db_no_secret.create_server(
            {
                "name": "Test Server",
                "host": "test.example.com",
                "username": "admin",
                "ssh_port": 22,
                "protocols": {"awg": {"installed": True, "port": 51820}},
            }
        )
        servers = db_no_secret.get_all_servers()
        assert len(servers) == 1
        assert servers[0]["name"] == "Test Server"
        assert servers[0]["id"] == srv_id

    def test_delete_server(self, db_no_secret):
        """Delete server works."""
        srv_id = db_no_secret.create_server(
            {"name": "ToDelete", "host": "del.example.com", "protocols": {}}
        )
        assert db_no_secret.delete_server(srv_id) is True
        assert db_no_secret.delete_server(99999) is False

    def test_create_and_read_user(self, db_no_secret):
        """User CRUD works."""
        user_id = "test-user-001"
        db_no_secret.create_user(
            {
                "id": user_id,
                "username": "testuser",
                "password_hash": "hash123",
                "role": "user",
            }
        )
        user = db_no_secret.get_user(user_id)
        assert user is not None
        assert user["username"] == "testuser"

    def test_create_and_read_connection(self, db_no_secret):
        """Connection CRUD works when user and server reference exist."""
        # Need a server and user first for FK constraints
        srv_id = db_no_secret.create_server(
            {"name": "fk_server", "host": "fk.example.com", "protocols": {}}
        )
        user_id = "conn-user-001"
        db_no_secret.create_user(
            {
                "id": user_id,
                "username": "connuser",
                "password_hash": "hash",
                "role": "user",
            }
        )

        conn_id = "conn-test-001"
        db_no_secret.create_connection(
            {
                "id": conn_id,
                "user_id": user_id,
                "server_id": srv_id,
                "protocol": "awg",
            }
        )
        conn = db_no_secret.get_connection_by_id(conn_id)
        assert conn is not None
        assert conn["protocol"] == "awg"
