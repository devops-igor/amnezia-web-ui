# Database Specification (`03-database.md`)

> **Target Package:** `internal/database`  
> **Source Python Files:** `app/core/database.py`, `app/core/schema.sql`, `migrate_to_sqlite.py`  
> **Driver:** `modernc.org/sqlite` (Pure-Go SQLite, CGO_ENABLED=0)  
> **Status:** Ground Truth Specification for Go Rewrite

---

## 1. Schema DDL (`schema.sql`)

### 1.1 PRAGMAs & Initialization

```sql
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;
PRAGMA synchronous=NORMAL;
```

### 1.2 Table Definitions (Existing + New VPN Tables)

```sql
-- 1. Servers Table
CREATE TABLE IF NOT EXISTS servers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    host TEXT NOT NULL,
    ssh_user TEXT,
    ssh_port INTEGER DEFAULT 22,
    ssh_pass TEXT,       -- Fernet encrypted (gAAAAA...)
    ssh_key TEXT,        -- Fernet encrypted (gAAAAA...)
    protocols TEXT,      -- JSON blob: {"awg": {...}, "telemt": {...}, ...}
    created_at TEXT
);

-- 2. Users Table
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,                       -- UUID v4
    username TEXT NOT NULL,                    -- Lowercase unique
    email TEXT,
    telegramId TEXT,
    description TEXT,
    password_hash TEXT,                        -- bcrypt hash
    role TEXT NOT NULL DEFAULT 'user',         -- 'admin' | 'user'
    enabled INTEGER NOT NULL DEFAULT 1,        -- 1 = true, 0 = false
    traffic_limit INTEGER DEFAULT 0,           -- Limit in bytes (0 = unlimited)
    traffic_used INTEGER DEFAULT 0,            -- Current period bytes
    traffic_total INTEGER DEFAULT 0,           -- Lifetime total bytes
    traffic_total_rx INTEGER DEFAULT 0,
    traffic_total_tx INTEGER DEFAULT 0,
    monthly_rx INTEGER DEFAULT 0,
    monthly_tx INTEGER DEFAULT 0,
    monthly_reset_at TEXT,                     -- ISO timestamp
    traffic_reset_strategy TEXT DEFAULT 'never',-- 'never' | 'monthly' | 'daily'
    share_enabled INTEGER DEFAULT 0,
    share_token TEXT,                          -- Unique share URL token
    share_password_hash TEXT,                  -- Optional bcrypt hash for share link
    remnawave_uuid TEXT,                       -- RemnaWave external user ID
    created_at TEXT,                           -- ISO timestamp
    last_reset_at TEXT,                        -- ISO timestamp
    expiration_date TEXT,                      -- Human date or ISO
    expires_at TEXT,                           -- ISO timestamp for automatic expiration
    awg_mimicry TEXT DEFAULT 'auto',           -- 'auto'|'tls'|'dns'|'sip'|'quic'
    password_change_required INTEGER NOT NULL DEFAULT 0,
    limits TEXT                                -- JSON blob: per-user overrides
);

-- 3. User Connections Table
CREATE TABLE IF NOT EXISTS user_connections (
    id TEXT PRIMARY KEY,                       -- UUID v4
    user_id TEXT NOT NULL,                     -- FK -> users(id)
    server_id INTEGER NOT NULL,                -- FK -> servers(id)
    protocol TEXT NOT NULL,                    -- 'awg' | 'telemt' | 'dns'
    client_id TEXT,                            -- Protocol-specific ID (AWG pubkey / telemt secret)
    name TEXT,
    awg_mimicry TEXT DEFAULT 'auto',
    last_rx INTEGER DEFAULT 0,
    last_tx INTEGER DEFAULT 0,
    traffic_delta_rx INTEGER DEFAULT 0,
    traffic_delta_tx INTEGER DEFAULT 0,
    traffic_total_rx INTEGER DEFAULT 0,
    traffic_total_tx INTEGER DEFAULT 0,
    traffic_total INTEGER DEFAULT 0,
    created_at TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- 4. Connection Creation Rate Limiting Log
CREATE TABLE IF NOT EXISTS connection_creation_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    created_at TEXT NOT NULL                   -- ISO timestamp
);

-- 5. Dynamic Application Settings
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT                                 -- JSON blob
);

-- 6. Migration Flags
CREATE TABLE IF NOT EXISTS migration_flags (
    key TEXT PRIMARY KEY,
    value TEXT
);

-- 7. Known SSH Host Key Fingerprints
CREATE TABLE IF NOT EXISTS known_hosts (
    server_id INTEGER PRIMARY KEY,
    fingerprint TEXT NOT NULL,                 -- SHA256 hex or standard format
    first_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
);

-- 8. Historical Leaderboard Snapshots
CREATE TABLE IF NOT EXISTS leaderboard_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    year INTEGER NOT NULL,
    month INTEGER NOT NULL,
    username TEXT NOT NULL,
    rank INTEGER NOT NULL,
    download INTEGER NOT NULL,
    upload INTEGER NOT NULL,
    total INTEGER NOT NULL,
    snapshot_at TEXT NOT NULL,
    UNIQUE(year, month, username)
);

-- 9. Backend AWG Tunnels (NEW - Phase 4E)
CREATE TABLE IF NOT EXISTS backend_tunnels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id INTEGER NOT NULL,                -- FK -> servers(id)
    interface_name TEXT NOT NULL UNIQUE,       -- e.g. 'awg-be-1'
    public_key TEXT NOT NULL,
    private_key TEXT NOT NULL,                 -- Fernet encrypted
    endpoint TEXT NOT NULL,                    -- host:port
    status TEXT NOT NULL DEFAULT 'connecting', -- 'connecting'|'active'|'degraded'|'disabled'
    last_health_check TEXT,                    -- ISO timestamp
    latency_ms INTEGER DEFAULT 0,
    active_connections INTEGER DEFAULT 0,
    created_at TEXT NOT NULL,
    FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
);

-- 10. Active VPN Sessions (NEW - Phase 4E)
CREATE TABLE IF NOT EXISTS vpn_sessions (
    id TEXT PRIMARY KEY,                       -- UUID v4 session ID
    user_id TEXT NOT NULL,                     -- FK -> users(id)
    backend_tunnel_id INTEGER NOT NULL,        -- FK -> backend_tunnels(id)
    peer_public_key TEXT NOT NULL UNIQUE,      -- User AWG client public key
    assigned_ip TEXT NOT NULL UNIQUE,          -- e.g. 10.100.0.2
    connected_at TEXT NOT NULL,                -- ISO timestamp
    last_seen TEXT NOT NULL,                   -- ISO timestamp
    rx_bytes INTEGER DEFAULT 0,
    tx_bytes INTEGER DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'connected',  -- 'connected'|'disconnected'|'draining'
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (backend_tunnel_id) REFERENCES backend_tunnels(id) ON DELETE CASCADE
);
```

### 1.3 Indexes

```sql
CREATE INDEX IF NOT EXISTS idx_user_connections_user_id ON user_connections(user_id);
CREATE INDEX IF NOT EXISTS idx_user_connections_server_id ON user_connections(server_id);
CREATE INDEX IF NOT EXISTS idx_user_connections_client_id ON user_connections(client_id);
CREATE INDEX IF NOT EXISTS idx_creation_log_user_time ON connection_creation_log(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_share_token ON users(share_token);
CREATE INDEX IF NOT EXISTS idx_users_remnawave_uuid ON users(remnawave_uuid);
CREATE INDEX IF NOT EXISTS idx_backend_tunnels_server_id ON backend_tunnels(server_id);
CREATE INDEX IF NOT EXISTS idx_vpn_sessions_user_id ON vpn_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_vpn_sessions_peer ON vpn_sessions(peer_public_key);
```

---

## 2. Go Database Layer Architecture

### 2.1 Concurrency Model (`internal/database/db.go`)

SQLite with WAL mode supports concurrent readers but only one active writer. To prevent `SQLITE_BUSY` errors:
1. Open the database using `sql.Open("sqlite", dsn)`.
2. DSN: `file:panel.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)`.
3. Wrap write transactions in a dedicated mutex (`writeMu sync.Mutex`) or set max open connections properly.

```go
package database

import (
	"database/sql"
	"sync"
	_ "modernc.org/sqlite"
)

type DB struct {
	db        *sql.DB
	secretKey string
	writeMu   sync.Mutex
}

func Open(dbPath, secretKey string) (*DB, error) {
	dsn := "file:" + dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	instance := &DB{
		db:        db,
		secretKey: secretKey,
	}
	if err := instance.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return instance, nil
}
```

---

## 3. Database Methods Catalog (All 67 Methods + VPN Additions)

### 3.1 Server Management Methods

| Method Name | Go Signature | SQL Query Pattern | Notes |
|-------------|--------------|-------------------|-------|
| `GetAllServers` | `(db *DB) GetAllServers(ctx context.Context) ([]models.Server, error)` | `SELECT id, name, host, ssh_user, ssh_port, ssh_pass, ssh_key, protocols, created_at FROM servers ORDER BY id` | Decrypts `ssh_pass` & `ssh_key`; parses `protocols` JSON |
| `GetServerByID` | `(db *DB) GetServerByID(ctx context.Context, id int) (*models.Server, error)` | `SELECT ... FROM servers WHERE id = ?` | Decrypts credentials; returns `nil, nil` if not found |
| `GetServerCount` | `(db *DB) GetServerCount(ctx context.Context) (int, error)` | `SELECT COUNT(*) FROM servers` | Scalar scan |
| `CreateServer` | `(db *DB) CreateServer(ctx context.Context, s *models.Server) (int, error)` | `INSERT INTO servers (name, host, ssh_user, ssh_port, ssh_pass, ssh_key, protocols, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)` | Encrypts `ssh_pass` & `ssh_key`; marshals `protocols` JSON |
| `UpdateServer` | `(db *DB) UpdateServer(ctx context.Context, id int, updates map[string]interface{}) error` | Dynamic `UPDATE servers SET k1=?, k2=? WHERE id=?` | Handles credential encryption if updated |
| `UpdateServerProtocols` | `(db *DB) UpdateServerProtocols(ctx context.Context, id int, protocols map[string]interface{}) error` | `UPDATE servers SET protocols = ? WHERE id = ?` | Marshals map to JSON |
| `DeleteServer` | `(db *DB) DeleteServer(ctx context.Context, id int) (bool, error)` | `DELETE FROM servers WHERE id = ?` | Cascades to `known_hosts` and `user_connections` |
| `GetKnownHostFingerprint` | `(db *DB) GetKnownHostFingerprint(ctx context.Context, serverID int) (string, error)` | `SELECT fingerprint FROM known_hosts WHERE server_id = ?` | Returns `""` if not found |
| `SaveKnownHostFingerprint` | `(db *DB) SaveKnownHostFingerprint(ctx context.Context, serverID int, fp string) error` | `INSERT INTO known_hosts (server_id, fingerprint) VALUES (?, ?) ON CONFLICT(server_id) DO UPDATE SET fingerprint = excluded.fingerprint` | Upsert |
| `DeleteKnownHost` | `(db *DB) DeleteKnownHost(ctx context.Context, serverID int) (bool, error)` | `DELETE FROM known_hosts WHERE server_id = ?` | |

---

### 3.2 User Management Methods

| Method Name | Go Signature | SQL Query Pattern | Notes |
|-------------|--------------|-------------------|-------|
| `GetAllUsers` | `(db *DB) GetAllUsers(ctx context.Context) ([]models.User, error)` | `SELECT id, username, email, telegramId, description, password_hash, role, enabled, traffic_limit, traffic_used, traffic_total, traffic_total_rx, traffic_total_tx, monthly_rx, monthly_tx, monthly_reset_at, traffic_reset_strategy, share_enabled, share_token, share_password_hash, remnawave_uuid, created_at, last_reset_at, expiration_date, expires_at, awg_mimicry, password_change_required, limits FROM users ORDER BY created_at DESC` | Parses `limits` JSON |
| `GetUserByID` | `(db *DB) GetUserByID(ctx context.Context, id string) (*models.User, error)` | `SELECT ... FROM users WHERE id = ?` | |
| `GetUserByUsername` | `(db *DB) GetUserByUsername(ctx context.Context, username string) (*models.User, error)` | `SELECT ... FROM users WHERE LOWER(username) = LOWER(?)` | Case-insensitive |
| `GetUserByShareToken` | `(db *DB) GetUserByShareToken(ctx context.Context, token string) (*models.User, error)` | `SELECT ... FROM users WHERE share_token = ?` | |
| `GetUserByRemnaWaveUUID`| `(db *DB) GetUserByRemnaWaveUUID(ctx context.Context, uuid string) (*models.User, error)` | `SELECT ... FROM users WHERE remnawave_uuid = ?` | |
| `CreateUser` | `(db *DB) CreateUser(ctx context.Context, u *models.User) (string, error)` | `INSERT INTO users (...) VALUES (...)` | Generates UUID v4 if empty; hashes password |
| `UpdateUser` | `(db *DB) UpdateUser(ctx context.Context, id string, updates map[string]interface{}) (bool, error)` | Dynamic `UPDATE users SET ... WHERE id = ?` | Marshals `limits` JSON if present |
| `DeleteUser` | `(db *DB) DeleteUser(ctx context.Context, id string) (bool, error)` | `DELETE FROM users WHERE id = ?` | Cascades to connections |
| `GetLeaderboard` | `(db *DB) GetLeaderboard(ctx context.Context, period string) ([]models.LeaderboardEntry, error)` | `SELECT username, monthly_rx+monthly_tx AS total ... FROM users WHERE enabled=1 ORDER BY total DESC` (or lifetime `traffic_total`) | Sorts and computes ranks |
| `SaveLeaderboardSnapshot`| `(db *DB) SaveLeaderboardSnapshot(ctx context.Context, year, month int) (int, error)` | `INSERT OR REPLACE INTO leaderboard_snapshots ...` | Snapshots monthly leaderboard |
| `GetLeaderboardSnapshot`| `(db *DB) GetLeaderboardSnapshot(ctx context.Context, year, month int) ([]models.LeaderboardEntry, error)` | `SELECT rank, username, download, upload, total FROM leaderboard_snapshots WHERE year=? AND month=? ORDER BY rank` | |

---

### 3.3 Connection Management Methods

| Method Name | Go Signature | SQL Query Pattern | Notes |
|-------------|--------------|-------------------|-------|
| `GetConnectionsByUser` | `(db *DB) GetConnectionsByUser(ctx context.Context, userID string) ([]models.UserConnection, error)` | `SELECT ... FROM user_connections WHERE user_id = ? ORDER BY created_at` | |
| `GetAllConnections` | `(db *DB) GetAllConnections(ctx context.Context) ([]models.UserConnection, error)` | `SELECT ... FROM user_connections ORDER BY created_at` | |
| `GetConnectionsByServerAndProtocol` | `(db *DB) GetConnectionsByServerAndProtocol(ctx context.Context, serverID int, proto string) ([]models.UserConnection, error)` | `SELECT ... FROM user_connections WHERE server_id = ? AND protocol = ?` | Protocol normalized |
| `GetConnectionByID` | `(db *DB) GetConnectionByID(ctx context.Context, id string) (*models.UserConnection, error)` | `SELECT ... FROM user_connections WHERE id = ?` | |
| `CreateConnection` | `(db *DB) CreateConnection(ctx context.Context, c *models.UserConnection) (string, error)` | `INSERT INTO user_connections (...) VALUES (...)` | Generates UUID v4 if empty |
| `UpdateConnection` | `(db *DB) UpdateConnection(ctx context.Context, id string, updates map[string]interface{}) (bool, error)` | Dynamic `UPDATE user_connections SET ... WHERE id = ?` | |
| `DeleteConnection` | `(db *DB) DeleteConnection(ctx context.Context, id string) (bool, error)` | `DELETE FROM user_connections WHERE id = ?` | |
| `DeleteConnectionByClientID` | `(db *DB) DeleteConnectionByClientID(ctx context.Context, clientID string, serverID int) (bool, error)` | `DELETE FROM user_connections WHERE client_id = ? AND server_id = ?` | |
| `DeleteConnectionsByUser` | `(db *DB) DeleteConnectionsByUser(ctx context.Context, userID string) (int, error)` | `DELETE FROM user_connections WHERE user_id = ?` | Returns deleted count |
| `DeleteConnectionsByServer` | `(db *DB) DeleteConnectionsByServer(ctx context.Context, serverID int) (int, error)` | `DELETE FROM user_connections WHERE server_id = ?` | Returns deleted count |
| `DeleteConnectionsByServerAndProtocol` | `(db *DB) DeleteConnectionsByServerAndProtocol(ctx context.Context, serverID int, proto string) (int, error)` | `DELETE FROM user_connections WHERE server_id = ? AND protocol = ?` | |
| `LogConnectionCreation` | `(db *DB) LogConnectionCreation(ctx context.Context, userID string) error` | `INSERT INTO connection_creation_log (user_id, created_at) VALUES (?, ?)` | |
| `GetRecentConnectionsLog` | `(db *DB) GetRecentConnectionsLog(ctx context.Context, userID string, windowSec int) ([]models.ConnectionLogEntry, error)` | `SELECT id, user_id, created_at FROM connection_creation_log WHERE user_id = ? AND unixepoch(created_at) >= ?` | Rate limit checks |
| `PruneConnectionLog` | `(db *DB) PruneConnectionLog(ctx context.Context, maxEntries int) error` | `DELETE FROM connection_creation_log WHERE id NOT IN (SELECT id FROM connection_creation_log ORDER BY created_at DESC LIMIT ?)` | Housekeeping |

---

### 3.4 Settings & Migration Methods

| Method Name | Go Signature | SQL Query Pattern | Notes |
|-------------|--------------|-------------------|-------|
| `GetSetting` | `(db *DB) GetSetting(ctx context.Context, key string, target interface{}) error` | `SELECT value FROM settings WHERE key = ?` | JSON unmarshals and decrypts SSL fields |
| `GetAllSettings` | `(db *DB) GetAllSettings(ctx context.Context) (map[string]interface{}, error)` | `SELECT key, value FROM settings` | Deserializes JSON and decrypts SSL fields |
| `UpdateSetting` | `(db *DB) UpdateSetting(ctx context.Context, key string, value interface{}) error` | `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value` | Encrypts SSL fields before serialization |
| `SaveAllSettings` | `(db *DB) SaveAllSettings(ctx context.Context, settingsMap map[string]interface{}) error` | Executed in a single transaction | Encrypts SSL fields |
| `GetSchemaVersion` | `(db *DB) GetSchemaVersion(ctx context.Context) (int, error)` | `SELECT value FROM settings WHERE key = 'schema_version'` | |
| `SetSchemaVersion` | `(db *DB) SetSchemaVersion(ctx context.Context, version int) error` | `INSERT OR REPLACE INTO settings (key, value) VALUES ('schema_version', ?)` | |
| `GetMigrationFlag` | `(db *DB) GetMigrationFlag(ctx context.Context, key string) (string, error)` | `SELECT value FROM migration_flags WHERE key = ?` | |
| `SetMigrationFlag` | `(db *DB) SetMigrationFlag(ctx context.Context, key, val string) error` | `INSERT INTO migration_flags (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value` | |

---

### 3.5 Backup / Restore & Transaction Methods

| Method Name | Go Signature | Description |
|-------------|--------------|-------------|
| `ExecuteTransaction` | `(db *DB) ExecuteTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error` | Wraps `fn` in a `BEGIN` / `COMMIT` / `ROLLBACK` block with write mutex locking |
| `LoadData` | `(db *DB) LoadData(ctx context.Context) (*models.BackupData, error)` | Dumps entire DB into JSON export structure (identical to `data.json`) |
| `SaveData` | `(db *DB) SaveData(ctx context.Context, data *models.BackupData) error` | Replaces entire DB within a single transaction in correct FK order |

---

### 3.6 New VPN Subsystem Methods (Phase 4E)

| Method Name | Go Signature | SQL Query Pattern |
|-------------|--------------|-------------------|
| `GetAllBackendTunnels` | `(db *DB) GetAllBackendTunnels(ctx context.Context) ([]models.BackendTunnel, error)` | `SELECT id, server_id, interface_name, public_key, private_key, endpoint, status, last_health_check, latency_ms, active_connections, created_at FROM backend_tunnels` |
| `GetBackendTunnelByID` | `(db *DB) GetBackendTunnelByID(ctx context.Context, id int) (*models.BackendTunnel, error)` | `SELECT ... FROM backend_tunnels WHERE id = ?` |
| `CreateBackendTunnel` | `(db *DB) CreateBackendTunnel(ctx context.Context, t *models.BackendTunnel) (int, error)` | `INSERT INTO backend_tunnels (...) VALUES (...)` (encrypts `private_key`) |
| `UpdateBackendTunnel` | `(db *DB) UpdateBackendTunnel(ctx context.Context, id int, updates map[string]interface{}) error` | Dynamic update |
| `DeleteBackendTunnel` | `(db *DB) DeleteBackendTunnel(ctx context.Context, id int) error` | `DELETE FROM backend_tunnels WHERE id = ?` |
| `GetVPNSessionByPeerKey`| `(db *DB) GetVPNSessionByPeerKey(ctx context.Context, key string) (*models.VPNSession, error)` | `SELECT ... FROM vpn_sessions WHERE peer_public_key = ?` |
| `CreateVPNSession` | `(db *DB) CreateVPNSession(ctx context.Context, s *models.VPNSession) error` | `INSERT INTO vpn_sessions (...) VALUES (...)` |
| `UpdateVPNSessionTraffic` | `(db *DB) UpdateVPNSessionTraffic(ctx context.Context, sessionID string, rx, tx int64) error` | `UPDATE vpn_sessions SET rx_bytes=?, tx_bytes=?, last_seen=? WHERE id=?` |
| `DeleteVPNSession` | `(db *DB) DeleteVPNSession(ctx context.Context, sessionID string) error` | `DELETE FROM vpn_sessions WHERE id = ?` |

---

## 4. `data.json` → SQLite Migration Specification

When the Go panel starts for the first time:
1. Check if `panel.db` exists in `DATA_DIR`.
2. If `panel.db` does NOT exist, but `data.json` DOES exist in `DATA_DIR`:
   - Initialize `panel.db` schema via `schema.sql`.
   - Read and parse `data.json`.
   - Execute `SaveData()` within a transaction.
   - Rename `data.json` to `data.json.bak` (`os.Rename`).
   - Log INFO: "Successfully migrated legacy data.json to SQLite panel.db".
