"""SQLite database wrapper for Amnezia Web Panel.

Replaces data.json with ACID-compliant, concurrent-safe storage using
SQLite in WAL mode. Provides typed CRUD methods matching the original
data.json access patterns, with indexed queries for O(1) lookups.

Server indexing: servers are looked up by their SQLite PRIMARY KEY id,
so frontend srv.id maps directly to db.get_server_by_id(server_id).
"""

import json
import logging
import os
import sqlite3
import threading
from datetime import datetime
from typing import Any, Dict, List, Optional

import credential_crypto

logger = logging.getLogger(__name__)

SCHEMA_PATH = os.path.join(os.path.dirname(__file__), "schema.sql")


def _row_to_dict(row: Optional[sqlite3.Row]) -> Optional[Dict[str, Any]]:
    """Convert a sqlite3.Row to a plain dict, returning None if row is None."""
    if row is None:
        return None
    return dict(row)


def _rows_to_dicts(rows: List[sqlite3.Row]) -> List[Dict[str, Any]]:
    """Convert a list of sqlite3.Row objects to plain dicts."""
    return [dict(r) for r in rows]


class Database:
    """Thread-safe SQLite wrapper with WAL mode and typed CRUD methods."""

    # ----------------------------------------------------------------
    # Column allowlists for update methods (SQL injection prevention)
    # See: tasks/sql-injection-column-names/spec.md
    # ----------------------------------------------------------------
    ALLOWED_SERVER_COLUMNS = frozenset(
        {
            "name",
            "host",
            "ssh_user",
            "ssh_port",
            "ssh_pass",
            "ssh_key",
            "protocols",
            "created_at",
        }
    )

    ALLOWED_USER_COLUMNS = frozenset(
        {
            "username",
            "email",
            "telegramId",
            "description",
            "password_hash",
            "role",
            "enabled",
            "traffic_limit",
            "traffic_used",
            "traffic_total",
            "traffic_total_rx",
            "traffic_total_tx",
            "monthly_rx",
            "monthly_tx",
            "monthly_reset_at",
            "traffic_reset_strategy",
            "share_enabled",
            "share_token",
            "share_password_hash",
            "remnawave_uuid",
            "created_at",
            "last_reset_at",
            "expiration_date",
            "password_change_required",
            "limits",
        }
    )

    ALLOWED_CONNECTION_COLUMNS = frozenset(
        {
            "user_id",
            "server_id",
            "protocol",
            "client_id",
            "name",
            "last_rx",
            "last_tx",
            "traffic_delta_rx",
            "traffic_delta_tx",
            "created_at",
        }
    )

    SCHEMA_VERSION = 1  # Increment when schema changes

    def __init__(self, db_path: str, secret_key: Optional[str] = None) -> None:
        self.db_path = db_path
        self._secret_key = secret_key or ""
        self._local = threading.local()
        self._init_db()
        # Initialise Fernet encryption for credentials at DB init time.
        # The secret_key must be provided — typically the app's SECRET_KEY.
        if secret_key:
            credential_crypto._init_fernet(secret_key)

    def _get_conn(self) -> sqlite3.Connection:
        """Get a thread-local connection with WAL mode and Row factory."""
        conn = getattr(self._local, "conn", None)
        if conn is None:
            conn = sqlite3.connect(self.db_path, check_same_thread=False)
            conn.row_factory = sqlite3.Row
            conn.execute("PRAGMA journal_mode=WAL")
            conn.execute("PRAGMA foreign_keys=ON")
            self._local.conn = conn
        return conn

    def _init_db(self) -> None:
        """Initialize schema from schema.sql if tables don't exist yet."""
        conn = self._get_conn()
        if os.path.exists(SCHEMA_PATH):
            with open(SCHEMA_PATH, "r", encoding="utf-8") as f:
                schema_sql = f.read()
            conn.executescript(schema_sql)
        else:
            logger.error("schema.sql not found at %s", SCHEMA_PATH)
            raise FileNotFoundError(f"schema.sql not found at {SCHEMA_PATH}")
        conn.commit()

        # Run schema migrations for existing databases
        self._run_migrations(conn)

        # Populate default settings on fresh installs
        self._ensure_default_settings()

        self._ensure_indexes()

        # Set schema version if not already set (new databases)
        if self.get_schema_version() == 0:
            self.set_schema_version(self.SCHEMA_VERSION)

        logger.info("Database initialized: %s", self.db_path)

    def _run_migrations(self, conn: sqlite3.Connection) -> None:
        """Run schema migrations for existing databases that may lack newer columns."""
        # Migration: add password_change_required column to users table
        try:
            conn.execute("SELECT password_change_required FROM users LIMIT 1")
        except sqlite3.OperationalError:
            logger.info("Migrating users table: adding password_change_required column")
            conn.execute(
                "ALTER TABLE users ADD COLUMN password_change_required "
                "INTEGER NOT NULL DEFAULT 0"
            )
            conn.commit()

        # Migration: encrypt existing plaintext ssh_pass / ssh_key values
        if self.get_migration_flag("credentials_encrypted") is None:
            logger.info("Migration: encrypting plaintext ssh_pass/ssh_key values")
            credential_crypto.encrypt_existing_plaintext(self.db_path, self._secret_key)
            self.set_migration_flag("credentials_encrypted", "1")
            logger.info("Migration: credentials_encrypted complete")

        # Migration: strip reality_private_key from protocols JSON in DB
        if self.get_migration_flag("xray_private_keys_cleared") is None:
            logger.info("Migration: stripping reality_private_key from protocols")
            rows = conn.execute("SELECT id, protocols FROM servers").fetchall()
            for row in rows:
                sid = row["id"]
                try:
                    protocols = json.loads(row["protocols"] or "{}")
                except (json.JSONDecodeError, TypeError):
                    continue
                if not isinstance(protocols, dict):
                    continue
                dirty = False
                for proto_key in protocols:
                    if isinstance(protocols[proto_key], dict):
                        for field in credential_crypto.SENSITIVE_PROTOCOL_FIELDS:
                            if field in protocols[proto_key]:
                                del protocols[proto_key][field]
                                dirty = True
                if dirty:
                    conn.execute(
                        "UPDATE servers SET protocols = ? WHERE id = ?",
                        (json.dumps(protocols), sid),
                    )
                    logger.info(
                        "Migration: cleared sensitive fields from " "server id=%d protocols",
                        sid,
                    )
            conn.commit()
            self.set_migration_flag("xray_private_keys_cleared", "1")
            logger.info("Migration: xray_private_keys_cleared complete")

    def _ensure_indexes(self) -> None:
        """Create missing indexes on existing databases.

        Called from __init__ to ensure indexes exist even on databases
        created before the indexes were added to schema.sql.
        Uses IF NOT EXISTS so it's idempotent.
        """
        indexes = [
            "CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)",
            "CREATE INDEX IF NOT EXISTS idx_users_share_token ON users(share_token)",
            "CREATE INDEX IF NOT EXISTS idx_users_remnawave_uuid ON users(remnawave_uuid)",
            "CREATE INDEX IF NOT EXISTS idx_user_connections_client_id ON user_connections(client_id)",
        ]
        conn = self._get_conn()
        for idx_sql in indexes:
            try:
                conn.execute(idx_sql)
            except Exception as e:
                logger.warning("Failed to create index: %s", e)
        conn.commit()

    # ----------------------------------------------------------------
    # Default settings
    # ----------------------------------------------------------------

    DEFAULT_SETTINGS = {
        "appearance": {
            "title": "Amnezia",
            "logo": "\u2764\ufe0f",
            "subtitle": "Web Panel",
        },
        "sync": {
            "remnawave_url": "",
            "remnawave_api_key": "",
            "remnawave_sync": False,
            "remnawave_sync_users": False,
            "remnawave_create_conns": False,
            "remnawave_server_id": 0,
            "remnawave_protocol": "awg",
        },
        "limits": {
            "max_connections_per_user": 10,
            "connection_rate_limit_count": 5,
            "connection_rate_limit_window": 60,
        },
        "protocol_paths": {
            "telemt_config_dir": "/opt/amnezia/telemt",
        },
    }

    def _ensure_default_settings(self) -> None:
        """Populate default settings if the settings table is empty (fresh install)."""
        conn = self._get_conn()
        count = conn.execute("SELECT COUNT(*) FROM settings").fetchone()[0]
        if count == 0:
            for key, value in self.DEFAULT_SETTINGS.items():
                conn.execute(
                    "INSERT INTO settings (key, value) VALUES (?, ?)",
                    (key, json.dumps(value)),
                )
            conn.commit()
            logger.info("Populated default settings for fresh install")

    # ----------------------------------------------------------------

    def execute_transaction(self, func, *args, **kwargs):
        """Execute a function inside a DB transaction.

        ``func`` receives the connection as its first argument.
        Commits on success, rolls back on exception.
        """
        conn = self._get_conn()
        try:
            conn.execute("BEGIN")
            result = func(conn, *args, **kwargs)
            conn.commit()
            return result
        except Exception:
            conn.rollback()
            raise

    # ----------------------------------------------------------------
    # Servers
    # ----------------------------------------------------------------

    def get_all_servers(self) -> List[Dict[str, Any]]:
        """Return all servers ordered by id (matches array-index ordering)."""
        conn = self._get_conn()
        rows = conn.execute("SELECT * FROM servers ORDER BY id").fetchall()
        return self._server_rows_to_dicts(rows)

    def get_server_by_id(self, server_id: int) -> Optional[Dict[str, Any]]:
        """Return a server by its database PRIMARY KEY id, or None if not found."""
        conn = self._get_conn()
        row = conn.execute("SELECT * FROM servers WHERE id = ?", (server_id,)).fetchone()
        if row is None:
            return None
        return self._server_row_to_dict(row)

    def get_server_count(self) -> int:
        """Return the number of servers."""
        conn = self._get_conn()
        row = conn.execute("SELECT COUNT(*) FROM servers").fetchone()
        return row[0]

    def _insert_server(self, conn, server: Dict[str, Any]) -> int:
        """Insert a server row. Shared by create_server() and save_data().

        Handles credential encryption internally. Returns lastrowid.
        """
        protocols_raw = server.get("protocols", {})
        if isinstance(protocols_raw, dict):
            protocols_raw = credential_crypto.strip_sensitive_protocol_fields(protocols_raw)
        protocols_json = json.dumps(protocols_raw)
        raw_pass = server.get("password") or server.get("ssh_pass", "")
        raw_key = server.get("private_key") or server.get("ssh_key", "")
        encrypted_pass = credential_crypto.encrypt_credential(raw_pass)
        encrypted_key = credential_crypto.encrypt_credential(raw_key)
        cur = conn.execute(
            """INSERT INTO servers (name, host, ssh_user, ssh_port, ssh_pass, ssh_key,
               protocols, created_at)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?)""",
            (
                server.get("name", ""),
                server.get("host", ""),
                server.get("username") or server.get("ssh_user", ""),
                server.get("ssh_port", 22),
                encrypted_pass,
                encrypted_key,
                protocols_json,
                server.get("created_at", datetime.now().isoformat()),
            ),
        )
        return cur.lastrowid

    def create_server(self, server: Dict[str, Any]) -> int:
        """Insert a server and return its database id."""
        conn = self._get_conn()
        lastrowid = self._insert_server(conn, server)
        conn.commit()
        return lastrowid

    def update_server(self, server_id: int, updates: Dict[str, Any]) -> None:
        """Update a server by its database id."""
        # Map common field names to DB column names BEFORE allowlist validation
        # so that both API-friendly names (password, private_key) and DB column
        # names (ssh_pass, ssh_key) are accepted.
        field_map = {
            "name": "name",
            "host": "host",
            "username": "ssh_user",
            "ssh_user": "ssh_user",
            "ssh_port": "ssh_port",
            "password": "ssh_pass",
            "ssh_pass": "ssh_pass",
            "private_key": "ssh_key",
            "ssh_key": "ssh_key",
            "protocols": "protocols",
        }
        mapped_updates = {}
        for key, value in updates.items():
            col = field_map.get(key, key)
            mapped_updates[col] = value

        # Validate mapped DB column names against allowlist to prevent SQL injection
        unknown = set(mapped_updates.keys()) - self.ALLOWED_SERVER_COLUMNS
        if unknown:
            raise ValueError(f"Unknown server columns: {', '.join(sorted(unknown))}")

        conn = self._get_conn()
        set_clauses = []
        values = []
        for col, value in mapped_updates.items():
            if col == "protocols" and isinstance(value, dict):
                value = json.dumps(value)
            # Encrypt credential fields before storing
            if col in ("ssh_pass", "ssh_key"):
                value = credential_crypto.encrypt_credential(str(value) if value else "")
            set_clauses.append(f"{col} = ?")
            values.append(value)
        if not set_clauses:
            return
        values.append(server_id)
        conn.execute(f"UPDATE servers SET {', '.join(set_clauses)} WHERE id = ?", values)
        conn.commit()

    def update_server_protocols(self, server_id: int, protocols: Dict) -> None:
        """Update just the protocols JSON blob for a server by db id."""
        # Strip sensitive fields before storing (defense-in-depth)
        if isinstance(protocols, dict):
            protocols = credential_crypto.strip_sensitive_protocol_fields(protocols)
        conn = self._get_conn()
        conn.execute(
            "UPDATE servers SET protocols = ? WHERE id = ?",
            (json.dumps(protocols), server_id),
        )
        conn.commit()

    def delete_server(self, server_id: int) -> bool:
        """Delete a server by its ID. Returns True if deleted."""
        conn = self._get_conn()
        with conn:
            # Delete connections first
            conn.execute("DELETE FROM user_connections WHERE server_id = ?", (server_id,))
            # Delete known_hosts
            conn.execute("DELETE FROM known_hosts WHERE server_id = ?", (server_id,))
            # Delete server
            cur = conn.execute("DELETE FROM servers WHERE id = ?", (server_id,))
            return cur.rowcount > 0

    # ----------------------------------------------------------------
    # Known Hosts
    # ----------------------------------------------------------------

    def get_known_host_fingerprint(self, server_id: int) -> Optional[str]:
        """Return the stored fingerprint for a server, or None if unknown."""
        conn = self._get_conn()
        row = conn.execute(
            "SELECT fingerprint FROM known_hosts WHERE server_id = ?", (server_id,)
        ).fetchone()
        return row["fingerprint"] if row else None

    def save_known_host_fingerprint(self, server_id: int, fingerprint: str) -> None:
        """Store or update the host key fingerprint for a server."""
        conn = self._get_conn()
        conn.execute(
            """INSERT INTO known_hosts (server_id, fingerprint)
               VALUES (?, ?)
               ON CONFLICT(server_id) DO UPDATE SET fingerprint = excluded.fingerprint""",
            (server_id, fingerprint),
        )
        conn.commit()

    def delete_known_host(self, server_id: int) -> bool:
        """Delete the known host entry for a server. Returns True if deleted."""
        conn = self._get_conn()
        cur = conn.execute("DELETE FROM known_hosts WHERE server_id = ?", (server_id,))
        conn.commit()
        return cur.rowcount > 0

    def _server_row_to_dict(self, row: sqlite3.Row) -> Dict[str, Any]:
        """Convert a server row, deserializing JSON fields."""
        d = dict(row)
        # Map DB column names back to original JSON field names
        d["username"] = d.pop("ssh_user", "")
        # Decrypt credentials (transparent to callers like SSHManager)
        d["password"] = credential_crypto.decrypt_credential(d.pop("ssh_pass", ""))
        d["private_key"] = credential_crypto.decrypt_credential(d.pop("ssh_key", ""))
        if "protocols" in d and isinstance(d["protocols"], str):
            d["protocols"] = json.loads(d["protocols"])
        # Strip sensitive protocol fields (defense-in-depth)
        if isinstance(d.get("protocols"), dict):
            d["protocols"] = credential_crypto.strip_sensitive_protocol_fields(d["protocols"])
        return d

    def _server_rows_to_dicts(self, rows: List[sqlite3.Row]) -> List[Dict[str, Any]]:
        return [self._server_row_to_dict(r) for r in rows]

    # ----------------------------------------------------------------
    # Users
    # ----------------------------------------------------------------

    def get_all_users(self) -> List[Dict[str, Any]]:
        """Return all users."""
        conn = self._get_conn()
        rows = conn.execute("SELECT * FROM users").fetchall()
        return [self._user_row_to_dict(r) for r in rows]

    def get_user(self, user_id: str) -> Optional[Dict[str, Any]]:
        """Return a user by id, or None."""
        conn = self._get_conn()
        row = conn.execute("SELECT * FROM users WHERE id = ?", (user_id,)).fetchone()
        return self._user_row_to_dict(row) if row else None

    def get_user_by_username(self, username: str) -> Optional[Dict[str, Any]]:
        """Return a user by username, or None."""
        conn = self._get_conn()
        row = conn.execute("SELECT * FROM users WHERE username = ?", (username,)).fetchone()
        return self._user_row_to_dict(row) if row else None

    def get_user_by_share_token(self, token: str) -> Optional[Dict[str, Any]]:
        """Return a user by share_token, or None."""
        conn = self._get_conn()
        row = conn.execute("SELECT * FROM users WHERE share_token = ?", (token,)).fetchone()
        return self._user_row_to_dict(row) if row else None

    def get_user_by_remnawave_uuid(self, uuid: str) -> Optional[Dict[str, Any]]:
        """Return a user by remnawave_uuid, or None."""
        conn = self._get_conn()
        row = conn.execute("SELECT * FROM users WHERE remnawave_uuid = ?", (uuid,)).fetchone()
        return self._user_row_to_dict(row) if row else None

    def _insert_user(self, conn, user: Dict[str, Any]) -> None:
        """Insert a user row. Shared by create_user() and save_data().

        Assumes `user` dict has already been validated/hashed by the caller.
        """
        limits_json = json.dumps(user.get("limits", {}))
        conn.execute(
            """INSERT INTO users (id, username, email, telegramId, description,
               password_hash, role, enabled, traffic_limit, traffic_used,
               traffic_total, traffic_total_rx, traffic_total_tx,
               monthly_rx, monthly_tx, monthly_reset_at,
               traffic_reset_strategy, share_enabled, share_token,
               share_password_hash, remnawave_uuid, created_at,
               last_reset_at, expiration_date, password_change_required, limits)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
                       ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
                       ?, ?, ?, ?, ?, ?)""",
            (
                user.get("id", ""),
                user.get("username", ""),
                user.get("email"),
                user.get("telegramId"),
                user.get("description"),
                user.get("password_hash", ""),
                user.get("role", "user"),
                1 if user.get("enabled", True) else 0,
                user.get("traffic_limit", 0),
                user.get("traffic_used", 0),
                user.get("traffic_total", 0),
                user.get("traffic_total_rx", 0),
                user.get("traffic_total_tx", 0),
                user.get("monthly_rx", 0),
                user.get("monthly_tx", 0),
                user.get("monthly_reset_at", ""),
                user.get("traffic_reset_strategy", "never"),
                1 if user.get("share_enabled", False) else 0,
                user.get("share_token"),
                user.get("share_password_hash"),
                user.get("remnawave_uuid"),
                user.get("created_at", datetime.now().isoformat()),
                user.get("last_reset_at", datetime.now().isoformat()),
                user.get("expiration_date"),
                1 if user.get("password_change_required", False) else 0,
                limits_json,
            ),
        )

    def create_user(self, user: Dict[str, Any]) -> str:
        """Insert a user and return its id."""
        conn = self._get_conn()
        self._insert_user(conn, user)
        conn.commit()
        return user.get("id", "")

    def update_user(self, user_id: str, updates: Dict[str, Any]) -> bool:
        """Update a user by id with the given fields. Returns True if found."""
        # Validate column names against allowlist to prevent SQL injection
        unknown = set(updates.keys()) - self.ALLOWED_USER_COLUMNS
        if unknown:
            raise ValueError(f"Unknown user columns: {', '.join(sorted(unknown))}")

        conn = self._get_conn()
        if not conn.execute("SELECT 1 FROM users WHERE id = ?", (user_id,)).fetchone():
            return False

        bool_fields = {"enabled", "share_enabled", "password_change_required"}
        json_fields = {"limits"}

        set_clauses = []
        values = []
        for key, value in updates.items():
            if key == "id":
                continue
            if key in bool_fields and isinstance(value, bool):
                value = 1 if value else 0
            if key in json_fields and isinstance(value, dict):
                value = json.dumps(value)
            set_clauses.append(f"{key} = ?")
            values.append(value)

        if not set_clauses:
            return True
        values.append(user_id)
        conn.execute(f"UPDATE users SET {', '.join(set_clauses)} WHERE id = ?", values)
        conn.commit()
        return True

    def delete_user(self, user_id: str) -> bool:
        """Delete a user and all their connections. Returns True if found."""
        conn = self._get_conn()
        if not conn.execute("SELECT 1 FROM users WHERE id = ?", (user_id,)).fetchone():
            return False
        conn.execute("DELETE FROM user_connections WHERE user_id = ?", (user_id,))
        conn.execute("DELETE FROM users WHERE id = ?", (user_id,))
        conn.commit()
        return True

    def _user_row_to_dict(self, row: sqlite3.Row) -> Dict[str, Any]:
        """Convert a user row, deserializing JSON fields."""
        d = dict(row)
        # Convert SQLite integers back to Python bools
        if "enabled" in d:
            d["enabled"] = bool(d["enabled"])
        if "share_enabled" in d:
            d["share_enabled"] = bool(d["share_enabled"])
        if "password_change_required" in d:
            d["password_change_required"] = bool(d["password_change_required"])
        # Deserialize JSON fields
        if "limits" in d and isinstance(d["limits"], str):
            d["limits"] = json.loads(d["limits"])
        elif "limits" not in d:
            d["limits"] = {}
        # Default None fields
        for nullable in [
            "email",
            "telegramId",
            "description",
            "share_token",
            "share_password_hash",
            "remnawave_uuid",
            "expiration_date",
        ]:
            if nullable not in d:
                d[nullable] = None
        return d

    # ----------------------------------------------------------------
    # User Connections
    # ----------------------------------------------------------------

    def get_connections_by_user(self, user_id: str) -> List[Dict[str, Any]]:
        """Return all connections for a user."""
        conn = self._get_conn()
        rows = conn.execute(
            "SELECT * FROM user_connections WHERE user_id = ?", (user_id,)
        ).fetchall()
        return _rows_to_dicts(rows)

    def get_all_connections(self) -> List[Dict[str, Any]]:
        """Return all user connections."""
        conn = self._get_conn()
        rows = conn.execute("SELECT * FROM user_connections").fetchall()
        return _rows_to_dicts(rows)

    def get_connections_by_server_and_protocol(
        self, server_id: int, protocol: str
    ) -> List[Dict[str, Any]]:
        """Return connections for a server+protocol combo."""
        conn = self._get_conn()
        rows = conn.execute(
            "SELECT * FROM user_connections WHERE server_id = ? AND protocol = ?",
            (server_id, protocol),
        ).fetchall()
        return _rows_to_dicts(rows)

    def get_connection_by_id(self, conn_id: str) -> Optional[Dict[str, Any]]:
        """Return a connection by its id."""
        conn = self._get_conn()
        row = conn.execute("SELECT * FROM user_connections WHERE id = ?", (conn_id,)).fetchone()
        return _row_to_dict(row)

    def create_connection(self, connection: Dict[str, Any]) -> str:
        """Insert a connection and return its id."""
        conn = self._get_conn()
        conn.execute(
            """INSERT INTO user_connections
               (id, user_id, server_id, protocol, client_id, name,
                last_rx, last_tx, traffic_delta_rx, traffic_delta_tx, created_at)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (
                connection.get("id", ""),
                connection.get("user_id", ""),
                connection.get("server_id", 0),
                connection.get("protocol", ""),
                connection.get("client_id"),
                connection.get("name"),
                connection.get("last_rx", 0),
                connection.get("last_tx", 0),
                connection.get("traffic_delta_rx", 0),
                connection.get("traffic_delta_tx", 0),
                connection.get("created_at", datetime.now().isoformat()),
            ),
        )
        conn.commit()
        return connection.get("id", "")

    def update_connection(self, conn_id: str, updates: Dict[str, Any]) -> bool:
        """Update a connection by id with the given fields. Returns True if found."""
        # Validate column names against allowlist to prevent SQL injection
        unknown = set(updates.keys()) - self.ALLOWED_CONNECTION_COLUMNS
        if unknown:
            raise ValueError(f"Unknown connection columns: {', '.join(sorted(unknown))}")

        conn = self._get_conn()
        if not conn.execute("SELECT 1 FROM user_connections WHERE id = ?", (conn_id,)).fetchone():
            return False

        set_clauses = []
        values = []
        for key, value in updates.items():
            set_clauses.append(f"{key} = ?")
            values.append(value)
        if not set_clauses:
            return True
        values.append(conn_id)
        conn.execute(f"UPDATE user_connections SET {', '.join(set_clauses)} WHERE id = ?", values)
        conn.commit()
        return True

    def delete_connection(self, conn_id: str) -> bool:
        """Delete a connection by id. Returns True if found."""
        conn = self._get_conn()
        cur = conn.execute("DELETE FROM user_connections WHERE id = ?", (conn_id,))
        conn.commit()
        return cur.rowcount > 0

    def delete_connection_by_client_id(self, client_id: str, server_id: int) -> bool:
        """Delete connection(s) matching client_id and server_id. Returns True if any deleted."""
        conn = self._get_conn()
        cur = conn.execute(
            "DELETE FROM user_connections WHERE client_id = ? AND server_id = ?",
            (client_id, server_id),
        )
        conn.commit()
        return cur.rowcount > 0

    def delete_connections_by_user(self, user_id: str) -> int:
        """Delete all connections for a user. Returns count deleted."""
        conn = self._get_conn()
        cur = conn.execute("DELETE FROM user_connections WHERE user_id = ?", (user_id,))
        conn.commit()
        return cur.rowcount

    def delete_connections_by_server(self, server_id: int) -> int:
        """Delete all connections for a server. Returns count deleted."""
        conn = self._get_conn()
        cur = conn.execute("DELETE FROM user_connections WHERE server_id = ?", (server_id,))
        conn.commit()
        return cur.rowcount

    # ----------------------------------------------------------------
    # Connection Creation Log
    # ----------------------------------------------------------------

    def log_connection_creation(self, user_id: str) -> None:
        """Add an entry to the connection creation log."""
        conn = self._get_conn()
        conn.execute(
            "INSERT INTO connection_creation_log (user_id, created_at) VALUES (?, ?)",
            (user_id, datetime.now().isoformat()),
        )
        conn.commit()

    def get_recent_connections_log(self, user_id: str, window_seconds: int) -> List[Dict[str, Any]]:
        """Get connection creation log entries for a user within a time window."""
        conn = self._get_conn()
        cutoff = datetime.now().timestamp() - window_seconds
        rows = conn.execute(
            """SELECT * FROM connection_creation_log
               WHERE user_id = ? AND unixepoch(created_at) >= ?""",
            (user_id, cutoff),
        ).fetchall()
        return _rows_to_dicts(rows)

    def get_connections_log_by_user(self, user_id: str) -> List[Dict[str, Any]]:
        """Get all connection creation log entries for a user."""
        conn = self._get_conn()
        rows = conn.execute(
            "SELECT * FROM connection_creation_log WHERE user_id = ? ORDER BY created_at",
            (user_id,),
        ).fetchall()
        return _rows_to_dicts(rows)

    def prune_connection_log(self, max_entries: int = 1000) -> None:
        """Keep only the most recent max_entries in the creation log."""
        conn = self._get_conn()
        conn.execute(
            """DELETE FROM connection_creation_log
               WHERE id NOT IN (
                   SELECT id FROM connection_creation_log
                   ORDER BY created_at DESC LIMIT ?
               )""",
            (max_entries,),
        )
        conn.commit()

    # ----------------------------------------------------------------
    # Settings
    # ----------------------------------------------------------------

    def get_setting(self, key: str, default: Any = None) -> Any:
        """Get a setting value by key. JSON-deserializes stored values."""
        conn = self._get_conn()
        row = conn.execute("SELECT value FROM settings WHERE key = ?", (key,)).fetchone()
        if row is None:
            return default
        value = row["value"]
        if value is None:
            return default
        try:
            return json.loads(value)
        except (json.JSONDecodeError, TypeError):
            return value

    def get_all_settings(self) -> Dict[str, Any]:
        """Get all settings as a dict, deserializing JSON values."""
        conn = self._get_conn()
        rows = conn.execute("SELECT key, value FROM settings").fetchall()
        result = {}
        for row in rows:
            try:
                result[row["key"]] = json.loads(row["value"])
            except (json.JSONDecodeError, TypeError):
                result[row["key"]] = row["value"]
        return result

    def update_setting(self, key: str, value: Any) -> None:
        """Set a setting value. Serializes dicts/lists to JSON."""
        conn = self._get_conn()
        if isinstance(value, (dict, list)):
            value = json.dumps(value)
        conn.execute(
            """INSERT INTO settings (key, value) VALUES (?, ?)
               ON CONFLICT(key) DO UPDATE SET value = excluded.value""",
            (key, value),
        )
        conn.commit()

    def save_all_settings(self, settings_dict: Dict[str, Any]) -> None:
        """Batch-update all settings from a dict."""
        conn = self._get_conn()
        for key, value in settings_dict.items():
            if isinstance(value, (dict, list)):
                value = json.dumps(value)
            elif value is None:
                value = "null"
            conn.execute(
                """INSERT INTO settings (key, value) VALUES (?, ?)
                   ON CONFLICT(key) DO UPDATE SET value = excluded.value""",
                (key, value),
            )
        conn.commit()

    def get_schema_version(self) -> int:
        """Get the current database schema version. Returns 0 if not set."""
        conn = self._get_conn()
        row = conn.execute("SELECT value FROM settings WHERE key = 'schema_version'").fetchone()
        if row:
            try:
                return int(row["value"])
            except (ValueError, TypeError):
                return 0
        return 0

    def set_schema_version(self, version: int) -> None:
        """Set the database schema version."""
        conn = self._get_conn()
        conn.execute(
            "INSERT OR REPLACE INTO settings (key, value) VALUES ('schema_version', ?)",
            (str(version),),
        )
        conn.commit()

    # ----------------------------------------------------------------
    # Bulk / compatibility methods (mimic load_data/save_data interface)
    # ----------------------------------------------------------------

    def load_data(self) -> Dict[str, Any]:
        """Load all data from DB into a dict matching the old data.json structure.

        This is a compatibility method to ease the transition. Prefer
        targeted query methods for new code.
        """
        return {
            "servers": self.get_all_servers(),
            "users": self.get_all_users(),
            "user_connections": self.get_all_connections(),
            "connection_creation_log": self._get_all_creation_log(),
            "settings": self.get_all_settings(),
        }

    def save_data(self, data: Dict[str, Any]) -> None:
        """Save all data from a dict, replacing the entire DB contents.

        This is a compatibility method for the backup/restore flow.
        Prefer targeted update methods for new code.
        """
        conn = self._get_conn()
        conn.execute("BEGIN")

        try:
            # Clear existing data
            conn.execute("DELETE FROM user_connections")
            conn.execute("DELETE FROM users")
            conn.execute("DELETE FROM servers")
            conn.execute("DELETE FROM settings")
            conn.execute("DELETE FROM connection_creation_log")

            # Insert servers
            for srv in data.get("servers", []):
                self._insert_server(conn, srv)

            # Insert users
            for u in data.get("users", []):
                self._insert_user(conn, u)

            # Insert connections
            for c in data.get("user_connections", []):
                conn.execute(
                    """INSERT INTO user_connections
                       (id, user_id, server_id, protocol, client_id, name,
                        last_rx, last_tx, traffic_delta_rx, traffic_delta_tx, created_at)
                       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                    (
                        c.get("id", ""),
                        c.get("user_id", ""),
                        c.get("server_id", 0),
                        c.get("protocol", ""),
                        c.get("client_id"),
                        c.get("name"),
                        c.get("last_rx", 0),
                        c.get("last_tx", 0),
                        c.get("traffic_delta_rx", 0),
                        c.get("traffic_delta_tx", 0),
                        c.get("created_at", datetime.now().isoformat()),
                    ),
                )

            # Insert connection creation log
            for entry in data.get("connection_creation_log", []):
                conn.execute(
                    "INSERT INTO connection_creation_log (user_id, created_at) VALUES (?, ?)",
                    (entry.get("user_id", ""), entry.get("timestamp", "")),
                )

            # Insert settings
            for key, value in data.get("settings", {}).items():
                if isinstance(value, (dict, list)):
                    value = json.dumps(value)
                elif value is None:
                    value = "null"
                conn.execute(
                    """INSERT INTO settings (key, value) VALUES (?, ?)
                       ON CONFLICT(key) DO UPDATE SET value = excluded.value""",
                    (key, value),
                )

            conn.commit()
            logger.info("save_data: Full database save completed")
        except Exception:
            conn.rollback()
            raise

    def _get_all_creation_log(self) -> List[Dict[str, Any]]:
        """Return all connection creation log entries in old format."""
        conn = self._get_conn()
        rows = conn.execute(
            "SELECT user_id, created_at FROM connection_creation_log ORDER BY id"
        ).fetchall()
        return [{"user_id": row["user_id"], "timestamp": row["created_at"]} for row in rows]

    # ----------------------------------------------------------------
    # Migration flags
    # ----------------------------------------------------------------

    def get_migration_flag(self, key: str) -> Optional[str]:
        """Get a migration flag value."""
        conn = self._get_conn()
        row = conn.execute("SELECT value FROM migration_flags WHERE key = ?", (key,)).fetchone()
        return row["value"] if row else None

    def set_migration_flag(self, key: str, value: str) -> None:
        """Set a migration flag value."""
        conn = self._get_conn()
        conn.execute(
            """INSERT INTO migration_flags (key, value) VALUES (?, ?)
               ON CONFLICT(key) DO UPDATE SET value = excluded.value""",
            (key, value),
        )
        conn.commit()


# ----------------------------------------------------------------
# Singleton helper
# ----------------------------------------------------------------

_db_instance: Optional[Database] = None


def get_db(db_path: Optional[str] = None, secret_key: Optional[str] = None) -> Database:
    """Get or create the singleton Database instance.

    If db_path is None, uses the default path next to app.py.
    If secret_key is None, credentials will not be encrypted/decrypted.
    """
    global _db_instance
    if _db_instance is None:
        if db_path is None:
            if getattr(__import__("sys"), "frozen", False):
                app_path = os.path.dirname(__import__("sys").executable)
            else:
                app_path = os.path.dirname(os.path.abspath(__file__))
            db_path = os.path.join(app_path, "panel.db")
        _db_instance = Database(db_path, secret_key=secret_key)
    return _db_instance


def reset_db(db_path: Optional[str] = None, secret_key: Optional[str] = None) -> Database:
    """Create a fresh Database instance (for testing or reinitialization)."""
    global _db_instance
    if db_path is None:
        if getattr(__import__("sys"), "frozen", False):
            app_path = os.path.dirname(__import__("sys").executable)
        else:
            app_path = os.path.dirname(os.path.abspath(__file__))
        db_path = os.path.join(app_path, "panel.db")
    _db_instance = Database(db_path, secret_key=secret_key)
    return _db_instance
