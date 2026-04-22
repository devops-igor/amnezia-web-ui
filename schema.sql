PRAGMA journal_mode=WAL;

CREATE TABLE IF NOT EXISTS servers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    host TEXT NOT NULL,
    ssh_user TEXT,
    ssh_port INTEGER DEFAULT 22,
    ssh_pass TEXT,
    ssh_key TEXT,
    protocols TEXT,  -- JSON blob (complex nested dict)
    created_at TEXT
);

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL,
    email TEXT,
    telegramId TEXT,
    description TEXT,
    password_hash TEXT,
    role TEXT NOT NULL DEFAULT 'user',
    enabled INTEGER NOT NULL DEFAULT 1,
    traffic_limit INTEGER,
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
    password_change_required INTEGER NOT NULL DEFAULT 0,
    limits TEXT  -- JSON blob for per-user limits override
);

CREATE TABLE IF NOT EXISTS user_connections (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    server_id INTEGER NOT NULL,
    protocol TEXT NOT NULL,
    client_id TEXT,
    name TEXT,
    last_rx INTEGER DEFAULT 0,
    last_tx INTEGER DEFAULT 0,
    traffic_delta_rx INTEGER DEFAULT 0,
    traffic_delta_tx INTEGER DEFAULT 0,
    created_at TEXT,
    FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS connection_creation_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT  -- JSON blob
);

CREATE TABLE IF NOT EXISTS migration_flags (
    key TEXT PRIMARY KEY,
    value TEXT
);

CREATE INDEX IF NOT EXISTS idx_user_connections_user_id ON user_connections(user_id);
CREATE INDEX IF NOT EXISTS idx_user_connections_server_id ON user_connections(server_id);
CREATE INDEX IF NOT EXISTS idx_creation_log_user_time ON connection_creation_log(user_id, created_at);

CREATE TABLE IF NOT EXISTS known_hosts (
    server_id INTEGER PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    first_seen TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (server_id) REFERENCES servers(id)
);