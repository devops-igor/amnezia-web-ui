# Amnezia Web Panel — Production Migration Runbook (Python to Go)

This runbook details the production cutover procedure from the legacy Python/FastAPI container to the pure-Go Amnezia Web Panel container.

---

## 1. Executive Summary & Architectural Shift

The Go rewrite replaces the entire Python/FastAPI/Paramiko stack with an idiomatic, single-binary pure-Go implementation.

| Characteristic | Legacy Python Container | Pure-Go Container | Production Benefit |
| :--- | :--- | :--- | :--- |
| **Image Size** | ~280 MB | **~38.6 MB** (Alpine 3.22) | 86% reduction in storage & transfer bandwidth |
| **Memory Footprint (RSS)** | ~120–180 MB | **~15 MB** | 90% reduction in runtime RAM usage |
| **Cold Startup Time** | ~3.5–5.0 seconds | **< 50 milliseconds** | Instant container restart and zero-downtime rolling deploys |
| **Security Model** | Root execution / relaxed caps | **Non-root `appuser` (UID 1000:1000)** | `read_only` rootfs, `cap_drop: [ALL]`, `cap_add: [NET_ADMIN]` |
| **Database Engine** | Python `sqlite3` | **Pure-Go SQLite (`modernc.org/sqlite`)** | Zero CGO, thread-safe single-writer WAL mode |
| **AmneziaWG Integration** | Subprocess `awg-quick` | **In-process userspace `amneziawg-go`** | Optional native load balancing & single entry point |

---

## 2. Pre-Migration Checklist & Safety Backup

> [!IMPORTANT]
> Always execute a full file backup before modifying container configurations or permissions.

### Step 2.1 — Freeze Traffic & Stop Legacy Container
```bash
cd /opt/amnezia-web-ui   # or your deployment directory
docker compose down
```

### Step 2.2 — Create Cold Backups of Data Directory and Secrets
```bash
# 1. Create a timestamped backup directory
BACKUP_DIR="/opt/amnezia-backup-$(date +%Y%m%d_%H%M%S)"
mkdir -p "$BACKUP_DIR"

# 2. Copy the full data directory (database, secret keys, backups)
cp -a ./data "$BACKUP_DIR/data_backup"

# 3. Copy environment configuration and compose files
cp .env "$BACKUP_DIR/.env.backup"
cp docker-compose.yml "$BACKUP_DIR/docker-compose.yml.backup"

# 4. Verify SQLite database integrity in the backup
sqlite3 "$BACKUP_DIR/data_backup/panel.db" "PRAGMA integrity_check;"
# Expected output: ok
```

---

## 3. Host Data Ownership & Permissions Migration

The legacy Python container typically operated with root permissions or default file ownership. The Go rewrite container strictly enforces a non-root security model, running as `appuser` with **UID 1000** and **GID 1000**.

### Step 3.1 — Update Directory Permissions
Run the following command on the host:
```bash
sudo chown -R 1000:1000 ./data
sudo chmod -R u+rwX,go-rwx ./data
```

### Step 3.2 — Startup Preflight Probe Assurance
The Go application includes an automated startup preflight probe (`internal/database/preflight.go`). If the `./data` directory or `panel.db` file is not writable by UID 1000, the container will exit immediately with an actionable error message rather than failing with cryptic SQLite I/O errors:
```
FATAL database preflight check failed: directory '/app/data' is not writable by appuser (UID 1000, GID 1000)
Remediation: please run 'sudo chown -R 1000:1000 ./data' on the Docker host.
```

---

## 4. Docker Compose & Environment Configuration Cutover

### Step 4.1 — Update `.env` Configuration
Ensure your `.env` contains the required master `SECRET_KEY` and review the new optional VPN configuration variables:

```env
# REQUIRED: Must be identical to legacy SECRET_KEY to decrypt existing server passwords/keys
SECRET_KEY=your_existing_master_secret_key_hex_or_string

# Application Port
APP_PORT=5000
PORT=5000

# Trusted Proxies (CIDR list for BunkerWeb / Reverse Proxy X-Forwarded-For headers)
TRUSTED_PROXIES=172.18.0.0/24,10.0.0.0/8

# In-Process AmneziaWG VPN Subsystem (Optional, default: false)
# Set to true if utilizing the panel as a centralized AWG load-balancing entry point
VPN_ENABLED=false
VPN_LISTEN_PORT=51820
VPN_SUBNET=10.100.0.0/16
```

### Step 4.2 — Verify `docker-compose.yml`
Ensure `docker-compose.yml` points to the Go rewrite image:

```yaml
services:
  amnezia-panel:
    image: ghcr.io/devops-igor/amnezia-web-ui:latest
    container_name: amnezia-panel
    user: "1000:1000"
    ports:
      - "${APP_PORT:-5000}:5000"
      - "${VPN_LISTEN_PORT:-51820}:${VPN_LISTEN_PORT:-51820}/udp"
    restart: unless-stopped
    devices:
      - /dev/net/tun:/dev/net/tun
    cap_drop:
      - ALL
    cap_add:
      - NET_ADMIN
    security_opt:
      - no-new-privileges:true
    read_only: true
    tmpfs:
      - /tmp
      - /var/run/amneziawg
    volumes:
      - ./data:/app/data
    environment:
      - SECRET_KEY=${SECRET_KEY:?SECRET_KEY must be set in .env}
      - TRUSTED_PROXIES=${TRUSTED_PROXIES:-172.18.0.0/24}
      - DATA_DIR=/app/data
      - VPN_ENABLED=${VPN_ENABLED:-false}
      - VPN_LISTEN_PORT=${VPN_LISTEN_PORT:-51820}
      - VPN_SUBNET=${VPN_SUBNET:-10.100.0.0/16}
      - PORT=5000
    healthcheck:
      test: ["CMD-SHELL", "curl -sf http://127.0.0.1:5000/api/health || exit 1"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 20s
```

> [!NOTE]
> On VPS hosts without `/dev/net/tun` support running in management-only mode (`VPN_ENABLED=false`), you can safely comment out `devices: [/dev/net/tun:/dev/net/tun]` and `cap_add: [NET_ADMIN]`.

### Step 4.3 — Launch the Go Container
```bash
# Standalone mode:
docker compose up -d

# Or with BunkerWeb WAF profile:
docker compose --profile bunkerweb up -d
```

---

## 5. Post-Cutover Health & Functional Verification

Execute these verification checks immediately after launching the Go container.

### Step 5.1 — Container Healthcheck & Startup Logs
```bash
# 1. Check container running state
docker compose ps

# 2. Inspect logs for successful schema initialization and preflight check
docker compose logs -f amnezia-panel
```
Expected log entries:
```
INFO Using SECRET_KEY from environment variable
INFO Starting Amnezia Web Panel version=1.0.0 port=5000
INFO No data.json found; skipping migration for fresh install
INFO Background orchestrator started boot_delay=1m0s interval=10m0s
INFO Listening for HTTP connections host=0.0.0.0 port=5000
```

### Step 5.2 — HTTP Health Probe
```bash
curl -i http://127.0.0.1:5000/api/health
```
Expected response:
```http
HTTP/1.1 200 OK
Content-Type: application/json; charset=utf-8

{"status":"ok","version":"1.0.0"}
```

### Step 5.3 — User Authentication & Session Verification
1. Open the panel in your browser: `http://<server-ip>:5000/login` (or `https://<domain>` with BunkerWeb).
2. Log in with your existing admin credentials.
3. **Session Re-authentication Notice**: Due to the security upgrade from Python `itsdangerous` to Go HMAC-SHA256 session signatures, all existing browser sessions will be cleanly expired, and users/administrators will be prompted to log in once.
4. Verify user password authentication: Existing bcrypt hashes (standard passwords, legacy >72-byte passwords truncated at 72 bytes, and new Go >72-byte passwords with SHA-256 pre-hash safeguard) as well as legacy PBKDF2 hashes authenticate seamlessly.

### Step 5.4 — Verify Server Connectivity & Remote Integration
1. Navigate to **Servers** in the dashboard.
2. Click **Check** on an existing server to trigger an SSH connection test.
3. Confirm that decrypted SSH passwords or private keys authenticate cleanly against remote hosts.
4. Verify client connection lists and protocol configuration downloads at `/my`.

---

## 6. Zero-Downtime Rollback Procedure

If unexpected issues arise during cutover, you must roll back to the legacy Python container using the pre-migration cold backup.

> [!IMPORTANT]
> **Mandatory Backup Restoration**: Do not attempt an in-place downgrade against the modified database without restoring the cold backup.
>
> 1. **Xray Sensitive Key Stripping**: On initial startup, the Go container executes security migration `migrateXraySensitiveKeys`, which permanently strips `reality_private_key` from `servers.protocols` JSON to prevent sensitive private keys from remaining in plaintext. If rolling back to Python without restoring the backup, Xray/XTLS configurations cannot be regenerated from the database.
> 2. **Schema Version Format**: Go stores `schema_version` as a JSON-encoded string (`'"1"'`), whereas legacy Python parses `schema_version` via `int(row["value"])`. Running Python directly against a Go-modified database causes a `ValueError`, prompting Python to reset `schema_version` to `0` and rerun migrations.
>
> Restoring the pre-migration cold backup ensures zero data loss and flawless legacy compatibility.

### Rollback Step 1 — Stop Go Container
```bash
docker compose down
```

### Rollback Step 2 — Restore Pre-Migration Cold Backup (Mandatory)
```bash
# 1. Restore SQLite database snapshot
cp "$BACKUP_DIR/data_backup/panel.db" ./data/panel.db

# 2. Restore environment and docker-compose files
cp "$BACKUP_DIR/.env.backup" .env
cp "$BACKUP_DIR/docker-compose.yml.backup" docker-compose.yml

# 3. Verify SQLite integrity of restored database
sqlite3 ./data/panel.db "PRAGMA integrity_check;"
```

### Rollback Step 3 — Restart Legacy Python Stack
```bash
# For standalone legacy container:
docker compose up -d

# Or with BunkerWeb:
docker compose --profile bunkerweb up -d
```

### Rollback Step 4 — Verify Legacy Panel Functionality
```bash
curl -i http://127.0.0.1:5000/login
```
Log in to confirm legacy operations are fully restored.
