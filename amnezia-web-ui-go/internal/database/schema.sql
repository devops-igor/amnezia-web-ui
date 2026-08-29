-- PRAGMAs & Initialization
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;
PRAGMA synchronous=NORMAL;

-- 1. Servers Table
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
);

-- 2. Users Table
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL,
    email TEXT,
    telegramId TEXT,
    description TEXT,
    password_hash TEXT,
    role TEXT NOT NULL DEFAULT 'user',
    enabled INTEGER NOT NULL DEFAULT 1,
    traffic_limit INTEGER DEFAULT 0,
    traffic_used INTEGER DEFAULT 0,
    traffic_total INTEGER DEFAULT 0,
    traffic_total_rx INTEGER DEFAULT 0,
    traffic_total_tx INTEGER DEFAULT 0,
    monthly_rx INTEGER DEFAULT 0,
    monthly_tx INTEGER DEFAULT 0,
    monthly_reset_at TEXT,
    traffic_reset_strategy TEXT DEFAULT 'never',
    share_enabled INTEGER DEFAULT 0,
    share_token TEXT,
    share_password_hash TEXT,
    remnawave_uuid TEXT,
    created_at TEXT,
    last_reset_at TEXT,
    expiration_date TEXT,
    expires_at TEXT,
    awg_mimicry TEXT DEFAULT 'auto',
    password_change_required INTEGER NOT NULL DEFAULT 0,
    limits TEXT
);

-- 3. User Connections Table
CREATE TABLE IF NOT EXISTS user_connections (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    server_id INTEGER NOT NULL,
    protocol TEXT NOT NULL,
    client_id TEXT,
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
    created_at TEXT NOT NULL
);

-- 5. Dynamic Application Settings
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT
);

-- 6. Migration Flags
CREATE TABLE IF NOT EXISTS migration_flags (
    key TEXT PRIMARY KEY,
    value TEXT
);

-- 7. Known SSH Host Key Fingerprints
CREATE TABLE IF NOT EXISTS known_hosts (
    server_id INTEGER PRIMARY KEY,
    fingerprint TEXT NOT NULL,
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

-- 9. Backend AWG Tunnels
CREATE TABLE IF NOT EXISTS backend_tunnels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id INTEGER NOT NULL,
    interface_name TEXT NOT NULL UNIQUE,
    public_key TEXT NOT NULL,
    private_key TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'connecting',
    last_health_check TEXT,
    latency_ms INTEGER DEFAULT 0,
    active_connections INTEGER DEFAULT 0,
    created_at TEXT NOT NULL,
    FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
);

-- 10. Active VPN Sessions
CREATE TABLE IF NOT EXISTS vpn_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    backend_tunnel_id INTEGER NOT NULL,
    peer_public_key TEXT NOT NULL UNIQUE,
    assigned_ip TEXT NOT NULL UNIQUE,
    connected_at TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    rx_bytes INTEGER DEFAULT 0,
    tx_bytes INTEGER DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'connected',
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (backend_tunnel_id) REFERENCES backend_tunnels(id) ON DELETE CASCADE
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_user_connections_user_id ON user_connections(user_id);
CREATE INDEX IF NOT EXISTS idx_user_connections_server_id ON user_connections(server_id);
CREATE INDEX IF NOT EXISTS idx_user_connections_client_id ON user_connections(client_id);
CREATE INDEX IF NOT EXISTS idx_creation_log_user_time ON connection_creation_log(user_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_share_token ON users(share_token);
CREATE INDEX IF NOT EXISTS idx_users_remnawave_uuid ON users(remnawave_uuid);
CREATE INDEX IF NOT EXISTS idx_backend_tunnels_server_id ON backend_tunnels(server_id);
CREATE INDEX IF NOT EXISTS idx_vpn_sessions_user_id ON vpn_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_vpn_sessions_peer ON vpn_sessions(peer_public_key);
