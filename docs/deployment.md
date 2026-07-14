# Amnezia Web Panel — Deployment Guide

This guide covers all deployment scenarios for the Amnezia Web Panel using Docker Compose, from a quick local standalone install to a hardened production stack with BunkerWeb WAF. MTProxyL is installed separately on the host.

---

## Overview

The panel ships as a Docker Compose setup with three components:

```
                        +-------------------+
                        |   BunkerWeb WAF   |
                        |  (ports 80/443)   |--- Let's Encrypt (auto)
                        +----------+--------+
                                   |
                         +---------v---------+
                         |   amnezia-panel    |  <-- panel + web UI
                         |   (port 5000)      |
                         +-------------------+
```

| Component | Image | Purpose | Port |
|-----------|-------|---------|------|
| `amnezia-panel` | `ghcr.io/devops-igor/amnezia-web-ui` | Admin + user web panel | 5000 |
| `bunkerweb` | `bunkerity/bunkerweb-all-in-one` | WAF + reverse proxy + SSL | 80/443 |
| `docker-proxy` | `tecnativa/docker-socket-proxy` | Restricted Docker API for BunkerWeb | — |
| `mtproxyl` | host-installed | MTProto proxy daemon | 18443 |

Profiles control which services start:

| Command | Services started |
|---------|-----------------|
| `docker compose up -d` | panel only |
| `docker compose --profile bunkerweb up -d` | panel + bunkerweb + docker-proxy |

---

## Prerequisites

- **Docker** with the compose plugin (v2+) — verify with `docker compose version`
- **Domain name** pointed to your server (for production HTTPS/Let's Encrypt)
- **Ports 80 and 443** open on the server firewall
- **Ports 5000** accessible for standalone panel access
- **UFW or iptables** to restrict access to admin ports
- For MTProxyL: installed via `mtproxyl install` on the host (see [Adding MTProxyL](#adding-mtproxyl))

---

## Quick Start — Standalone (Local)

Use this for single-node local testing or development. No domain, no SSL, no WAF.

```bash
# 1. Clone the repository
git clone https://github.com/devops-igor/amnezia-web-ui.git
cd amnezia-web-ui

# 2. Create environment and data directories
cp .env.example .env
mkdir -p data
chown 100:101 data

# 3. Generate a secret key
SECRET_KEY=$(python3 -c "import secrets; print(secrets.token_hex(32))")
echo "SECRET_KEY=$SECRET_KEY" >> .env

# 4. Start the panel
docker compose up -d

# 5. Open in browser
# Panel:    http://localhost:5000
# API docs: http://localhost:5000/docs
```

To stop:

```bash
docker compose down
```

To update:

```bash
docker compose pull
docker compose up -d
```

---

## Production Setup with BunkerWeb

BunkerWeb acts as a reverse proxy and WAF in front of the panel. It automatically provisions a Let's Encrypt certificate for your domain.

### Step 1 — DNS A Record

Point your domain (e.g. `vpn.example.com`) to your server's public IP:

```
A  vpn.example.com  →  203.0.113.42
```

Wait for DNS propagation (TTL depends on your registrar; typically 5–30 minutes).

### Step 2 — Prepare Config Directory

Create a data directory for panel persistence and set ownership so the
container (which runs as `appuser` UID 100, GID 101) can write to it:

```bash
mkdir -p data
chown 100:101 data
```

### Step 3 — Edit `.env`

Copy `.env.example` to `.env` and fill in:

```bash
cp .env.example .env
nano .env    # or $EDITOR
```

Critical fields:

```env
# REQUIRED — generate with: python3 -c "import secrets; print(secrets.token_hex(32))"
SECRET_KEY=<your-generated-key>

# Domain for the panel — BunkerWeb vhost + Let's Encrypt
PANEL_DOMAIN=vpn.example.com

# Let's Encrypt email
EMAIL_LETS_ENCRYPT=admin@example.com

# Keep direct panel port disabled when using BunkerWeb
APP_PORT=

# Trusted proxies — include bw-net subnet
TRUSTED_PROXIES=172.18.0.0/24,10.0.0.0/8

# Whitelist admin IPs (optional but recommended)
USE_WHITELIST_IP=yes
WHITELIST_IP_LIST=203.0.113.50,203.0.113.100
```

> **Security note**: When `APP_PORT=` (empty), Docker Compose still maps a random ephemeral host port to container port 5000. This means the panel may still be reachable from the host on an unpredictable port. For true isolation, rely on BunkerWeb as your sole entry point and use host firewall rules (`ufw`) to restrict direct access.

### Step 4 — Start the Stack

```bash
docker compose --profile bunkerweb up -d
```

Check status:

```bash
docker compose ps
docker compose logs -f bunkerweb
```

### Step 5 — Verify HTTPS

After ~30 seconds (Let's Encrypt needs time), open:

- Panel: `https://vpn.example.com`

> First cert request can take up to 60 seconds. If you see a certificate error immediately, wait and retry.

### Auto-Renewal

Let's Encrypt certificates auto-renew 30 days before expiry. No manual action needed.

### BunkerWeb UI

When `USE_UI=yes`, the BunkerWeb admin interface is available. Use it to view real-time request logs, ban IPs, and adjust WAF sensitivity.

There are two ways to access the BunkerWeb UI:

1. **Via domain** (if `BUNKERWEB_UI_DOMAIN` is set): open `https://dev.example.com` in your browser
2. **Via SSH tunnel** (default, always available): access port 7080 on localhost

   ```bash
   # On your local machine:
   ssh -L 7080:127.0.0.1:7080 user@server
   # Then open http://localhost:7080 in your browser
   ```

Credentials are created on first access — you'll be prompted to set an admin password.

---

## Adding MTProxyL

MTProxyL is installed on the host as a system service, not via Docker Compose. The panel communicates with the MTProxyL daemon running on the host.

### Prerequisites

Install MTProxyL on the host:

```bash
# Clone and install
git clone https://github.com/yrncf/mtproxyl.git /opt/mtproxyl
cd /opt/mtproxyl
./install.sh
```

The installer sets up the service at `/opt/mtproxyl/` with a default `config.toml`.

### Configuration

Edit the MTProxyL config at `/opt/mtproxyl/config.toml` to add your Telegram servers and secrets. Refer to the MTProxyL documentation for configuration options.

### Managing MTProxyL

```bash
# Check status
mtproxyl status

# View logs
mtproxyl logs

# Health check
mtproxyl health

# Restart after config changes
mtproxyl restart
```

MTProxyL runs on port 18443 by default. The panel reads the port from `TELEMT_PORT` in `.env`.

---

## Multiple Domains (BunkerWeb Multisite)

BunkerWeb supports running the Amnezia panel and its own admin UI on separate domains. This is configured via three environment variables in `.env`.

### Overview

With multisite configured, you get:

- **Panel domain** (`PANEL_DOMAIN`): serves the Amnezia Web Panel
- **UI domain** (`BUNKERWEB_UI_DOMAIN`): serves the BunkerWeb admin interface
- **Local port** (`BUNKERWEB_UI_PORT`): localhost-only port for SSH tunnel access to the UI

### Configuration via .env

```env
# =============================================================================
# BUNKERWEB MULTISITE — Domain Routing
# =============================================================================

# Domain serving the Amnezia Web Panel (reverse-proxied through BunkerWeb).
# Defaults to SERVER_NAME for backward compatibility with single-domain setups.
PANEL_DOMAIN=vpn.example.com

# Domain serving the BunkerWeb Web UI (optional).
# Set this to expose BunkerWeb's admin interface on a separate domain.
# Leave empty for single-domain setups — the UI is still accessible via
# BUNKERWEB_UI_PORT below (default: SSH tunnel to localhost:7080).
BUNKERWEB_UI_DOMAIN=dev.example.com

# Localhost port for direct BunkerWeb UI access (SSH tunnel).
# Default: 127.0.0.1:7080 — accessible only from localhost for security.
BUNKERWEB_UI_PORT=127.0.0.1:7080
```

### Example Setup

For a deployment with:
- `dev.drochi.games` → BunkerWeb Web UI
- `vpn.dev.drochi.games` → Amnezia Web Panel

```env
PANEL_DOMAIN=vpn.dev.drochi.games
BUNKERWEB_UI_DOMAIN=dev.drochi.games
BUNKERWEB_UI_PORT=127.0.0.1:7080
```

Both domains need DNS A records pointing to your server's IP. BunkerWeb will request separate Let's Encrypt certificates for each.

After starting with `docker compose --profile bunkerweb up -d`:

- Panel: `https://vpn.dev.drochi.games`
- BunkerWeb UI: `https://dev.drochi.games` (or `http://localhost:7080` via SSH tunnel)

### Single-Domain Mode

When `BUNKERWEB_UI_DOMAIN` is empty (the default), the setup operates in single-domain mode:

- Panel is served at `PANEL_DOMAIN`
- BunkerWeb UI is **only** accessible via the localhost port (`BUNKERWEB_UI_PORT`)
- The `${BUNKERWEB_UI_DOMAIN:-}_*` environment variables in docker-compose.yml resolve to harmless no-ops

This is backward compatible — existing single-domain deployments continue to work.

### Accessing the BunkerWeb UI

**Via domain** (when `BUNKERWEB_UI_DOMAIN` is set):

Open `https://dev.example.com` in your browser. BunkerWeb serves its own admin interface on port 7000 internally, routed through the reverse proxy.

**Via SSH tunnel** (default, always available):

```bash
# On your local machine:
ssh -L 7080:127.0.0.1:7080 user@server
# Then open http://localhost:7080 in your browser
```

**Via direct port** (if `BUNKERWEB_UI_PORT` is set to a non-localhost address):

Open `http://server-ip:7080` in your browser. Not recommended for production — use SSH tunnel instead.

### First-Time BunkerWeb UI Setup

The BunkerWeb UI creates admin credentials on first access. When you open the UI for the first time, you'll be prompted to set a password. This password is stored in the `bw-data` Docker volume and persists across restarts.

---

## Environment Variable Reference

| Variable | Default | Required | Description |
|----------|---------|----------|-------------|
| `SECRET_KEY` | — | **Yes** | Session signing key. Generate with `python3 -c "import secrets; print(secrets.token_hex(32))"` |
| `AMNEZIA_IMAGE` | `ghcr.io/devops-igor/amnezia-web-ui:latest` | No | Docker image tag. Pin to a version tag or SHA in production |
| `APP_PORT` | `5000` | No | Direct panel port. Note: `APP_PORT=` (empty) maps a random ephemeral port due to Docker behavior, not "no port". Use BunkerWeb + firewall for true isolation |
| `DATA_DIR` | `./data` | No | Host directory for panel SQLite DB and config persistence |
| `TRUSTED_PROXIES` | `172.18.0.0/24` | No | CIDR list of trusted proxies for X-Forwarded-For resolution |
| `SERVER_NAME` | `localhost` | No | Legacy — prefer PANEL_DOMAIN for the panel. Used as default for PANEL_DOMAIN |
| `PANEL_DOMAIN` | — | No | Domain serving the Amnezia Web Panel (reverse-proxied through BunkerWeb). Defaults to SERVER_NAME |
| `BUNKERWEB_UI_DOMAIN` | — | No | Domain for the BunkerWeb Web UI (optional). When empty, UI is only accessible via localhost port |
| `BUNKERWEB_UI_PORT` | `127.0.0.1:7080` | No | Localhost bind for BunkerWeb UI port 7000. Access via SSH tunnel: `ssh -L 7080:127.0.0.1:7080 user@server` |
| `AUTO_LETS_ENCRYPT` | `yes` | No | Enable automatic Let's Encrypt certificate generation |
| `EMAIL_LETS_ENCRYPT` | — | **Yes (when AUTO_LETS_ENCRYPT=yes)** | Email for Let's Encrypt expiry warnings |
| `BUNKERWEB_VERSION` | `1.6.9` | No | BunkerWeb image version tag |
| `USE_UI` | `yes` | No | Enable BunkerWeb admin UI. When BUNKERWEB_UI_DOMAIN is set, accessible at that domain. Otherwise accessible via BUNKERWEB_UI_PORT (localhost only) |
| `USE_GZIP` | `yes` | No | Enable gzip HTTP response compression |
| `USE_MODSECURITY` | `yes` | No | Enable ModSecurity WAF rules |
| `USE_CROWDSEC` | `no` | No | Enable CrowdSec integration (requires separate CrowdSec setup) |
| `ALLOWED_METHODS` | `GET\|POST\|HEAD\|PATCH\|DELETE` | No | HTTP methods allowed through ModSecurity. Default includes PATCH (for speed-limit API) and DELETE (for future use). Reduce for stricter security |
| `MULTISITE` | `yes` | No | Enable BunkerWeb multi-site mode (required for per-site config) |
| `USE_WHITELIST_IP` | `yes` | No | Enable IP whitelist for the panel domain (per-site, via `${PANEL_DOMAIN}_` prefix). Global default is `no`; the panel site overrides it |
| `WHITELIST_IP_LIST` | — | No | Space- or comma-separated IPs/CIDRs for whitelist. Example: `203.0.113.50,203.0.113.100,10.0.0.0/24` |
| `USE_BAD_BEHAVIOR` | `yes` | No | Block known malicious bots via Bad Behavior module |
| `USE_LIMIT_REQ` | `yes` | No | Enable ModSecurity request rate limiting |
| `USE_LIMIT_CONN` | `yes` | No | Enable ModSecurity connection limiting |
| `TELEMT_PORT` | `18443` | No | Port where MTProxyL listens (default 18443; use 443 when bunkerweb is off) |
| `TZ` | `UTC` | No | Server timezone for timestamps |

---

## IP Whitelisting

IP whitelisting operates at the BunkerWeb layer, before traffic reaches the panel. BunkerWeb uses **per-site environment variables** to control whitelisting for each domain independently.

### How It Works

1. `USE_WHITELIST_IP=yes` in `.env` tells BunkerWeb to activate its whitelist engine for the panel domain.
2. `WHITELIST_IP_LIST=203.0.113.50,203.0.113.100` provides the allowed IPs/CIDRs.
3. BunkerWeb returns HTTP 403 for any request from an IP not in the list.

### Per-Site Configuration (BunkerWeb Multisite)

In multisite mode, BunkerWeb applies settings **per site** using the `${PANEL_DOMAIN}_` prefix. The `docker-compose.yml` configures this automatically:

```yaml
# In the bunkerweb service environment:
- USE_WHITELIST_IP=no                    # Global default (off for all sites)
- WHITELIST_IP_LIST=${WHITELIST_IP_LIST:-}
# Per-site override — only the panel domain gets whitelist protection:
- ${PANEL_DOMAIN:-}_USE_WHITELIST_IP=${USE_WHITELIST_IP:-no}
- ${PANEL_DOMAIN:-}_WHITELIST_IP_LIST=${WHITELIST_IP_LIST:-}
```

When `USE_WHITELIST_IP=yes` is set in `.env`, the per-site vars expand to (e.g.) `vpn.example.com_USE_WHITELIST_IP=yes`, activating whitelisting **only for the panel domain**. Other domains (like the BunkerWeb UI domain) remain unaffected and use the global default (`no`).

> **Why not use Docker labels on the panel container?** BunkerWeb's Docker autoconf mode reads `bunkerweb.*` labels to discover services, but it **does not apply** all label settings as per-site configuration. Specifically, `USE_WHITELIST_IP` and `WHITELIST_IP_LIST` set via labels are silently ignored by the autoconf controller — the whitelist ends up empty and all traffic is blocked with a 403/404. Per-site environment variables on the BunkerWeb service are the correct mechanism.

### Configuration

```env
USE_WHITELIST_IP=yes
WHITELIST_IP_LIST=203.0.113.50,203.0.113.100,10.0.0.0/24
```

### Admin IP Only Setup

```env
USE_WHITELIST_IP=yes
WHITELIST_IP_LIST=203.0.113.50   # your current IP only
```

> Find your IP: `curl -s https://ifconfig.me` or `curl -s https://api.ipify.org`

---

## ModSecurity Allowed HTTP Methods

When ModSecurity is enabled (`USE_MODSECURITY=yes`, the default), BunkerWeb restricts which HTTP methods can reach the panel. The default `ALLOWED_METHODS` only includes `GET|POST|HEAD`, which is too restrictive for the panel API.

### How It Works

1. ModSecurity's Core Rule Set checks every incoming request's HTTP method against `ALLOWED_METHODS`.
2. If the method is not in the list, ModSecurity returns **405 Method Not Allowed** before the request reaches the panel.
3. The panel's speed-limit API (`PATCH /api/servers/{id}/connections/{client}/speed-limit`) requires `PATCH`.

### Configuration

```env
# In .env — pipe-separated list of allowed HTTP methods
# PATCH is required for the speed-limit API
# DELETE is included for future API endpoints
ALLOWED_METHODS=GET|POST|HEAD|PATCH|DELETE
```

This is set in `docker-compose.yml` as a BunkerWeb environment variable so it applies globally.

### Troubleshooting 405 Errors

If you see **405 Method Not Allowed** with a BunkerWeb-branded error page when using the panel's speed-limit feature (or any API call using PATCH/DELETE):

1. Check `ALLOWED_METHODS` includes the method: `docker exec bunkerweb cat /etc/nginx/variables.env | grep ALLOWED_METHODS`
2. Add the missing method to `docker-compose.yml` under the `bunkerweb` service environment
3. Restart: `docker compose --profile bunkerweb up -d bunkerweb`

---

## SSL/TLS

### Auto Let's Encrypt

When `AUTO_LETS_ENCRYPT=yes` and `PANEL_DOMAIN` points to your server, BunkerWeb automatically requests and renews a Let's Encrypt certificate. No manual certificate handling required.

```env
PANEL_DOMAIN=vpn.example.com
AUTO_LETS_ENCRYPT=yes
EMAIL_LETS_ENCRYPT=admin@example.com
```

### Certificate Storage

Let's Encrypt certificates are stored in the BunkerWeb volume (`bw-data`). To inspect:

```bash
docker compose exec bunkerweb ls /data/certs/
```

### Troubleshooting Certificate Failures

**Error: "Client could not reach any nameserver"**

- DNS A record not yet propagated. Check with: `dig +short vpn.example.com` or `nslookup vpn.example.com`
- Wait up to 30 minutes after creating the DNS record.

**Error: "Connection refused" during ACME challenge**

- Port 80 is blocked by firewall. Open it: `ufw allow 80/tcp`
- Another service is using port 80 (Apache, Nginx). Stop it or change its port.

**Error: "Timeout during connect"**

- Firewall still blocking port 80. Verify: `curl -v http://vpn.example.com/.well-known/acme-challenge/test`
- Port 80 not forwarded from router to server.

**Error: "Incorrect NS record"**

- Domain's nameservers don't match where the A record was created. Fix DNS at the registrar.

**Let's Encrypt not auto-renewing**

- Check cert expiry: `docker compose exec bunkerweb openssl x509 -in /data/certs/cert.pem -noout -dates`
- Renew manually: `docker compose exec bunkerweb certbot renew`

### External Certificates

To use your own certificates instead of Let's Encrypt:

1. Place `cert.pem`, `key.pem`, and `ca.pem` in a directory
2. Mount the directory into bunkerweb
3. Set `AUTO_LETS_ENCRYPT=no`
4. Add labels to the panel service for certificate paths (see BunkerWeb docs)

---

## Security Hardening Checklist

### Firewall

```bash
# Allow SSH (22), HTTP (80), HTTPS (443) only
ufw default deny incoming
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw enable
```

> Do NOT expose port 5000 directly in production unless you fully understand the security implications. When using BunkerWeb, set `APP_PORT=` in `.env` — but note that Docker may still map a random ephemeral host port. Use host firewall rules (`ufw`) to restrict direct access if needed.

### SSH Hardening

```bash
# Use key-based auth, disable password auth in /etc/ssh/sshd_config:
# PasswordAuthentication no
# PubkeyAuthentication yes

# Restrict which users can SSH:
# AllowUsers admin deploy
```

### Docker Security

```bash
# Keep Docker updated
sudo apt update && sudo apt upgrade docker-ce docker-ce-cli containerd.io

# Don't expose the Docker socket
# If bunkerweb needs it (it does via docker-proxy), the docker-proxy limits access.
# Never mount /var/run/docker.sock directly to the panel container.

# Run containers with security restrictions (already in compose):
# security_opt: no-new-privileges:true
# read_only: true (panel)
# tmpfs /tmp (panel)
```

### Regular Updates

```bash
# Update Docker images weekly
docker compose pull
docker compose up -d

# Or use Watchtower (automatic updates):
# docker run -d --name watchtower -v /var/run/docker.sock:/var/run/docker.sock containrrr/watchtower
```

### Database Backups

```bash
# Backup the SQLite DB
cp data/panel.db "data/panel.db.$(date +%Y%m%d).bak"

# Add to crontab for nightly backups:
# 0 3 * * * cp /path/to/data/panel.db /path/to/backup/panel.db.$(date +\%Y\%m\%d).bak
```

### BunkerWeb Recommendations

- Keep `USE_MODSECURITY=yes` (OWASP rules)
- Keep `USE_BAD_BEHAVIOR=yes` (blocks known abusive bots)
- Enable `USE_LIMIT_REQ` and `USE_LIMIT_CONN` to prevent DDoS
- Review BunkerWeb logs periodically: `docker compose logs -f bunkerweb`
- If using CrowdSec, integrate it for dynamic blocking of attacking IPs

---

## Troubleshooting

### Port Conflicts

**Error: `port is already allocated`**

Something else is using a port. Find it:

```bash
# Check what's using port 80
sudo lsof -i :80
sudo netstat -tlnp | grep :80

# Common culprits: Apache, Nginx, Caddy
sudo systemctl stop apache2    # Apache
sudo systemctl stop nginx      # Nginx
```

**Fix**: Stop the conflicting service or change its port.

### BunkerWeb Not Proxying to Panel

1. Check that the panel is labeled correctly:
   ```bash
   docker compose exec bunkerweb bwcli list
   ```
2. Check BunkerWeb logs for backend connection errors:
   ```bash
   docker compose logs bunkerweb | grep -i error
   ```
3. Verify the panel is on the same Docker network:
   ```bash
   docker network inspect amnezia-web-panel_bw-net
   ```
4. Check panel health:
   ```bash
   docker compose ps amnezia-panel
   docker compose logs amnezia-panel | tail -20
   ```

### Certificate Generation Failures

1. Verify DNS: `dig +short vpn.example.com` should return your server IP
2. Verify port 80 is open: `curl -v http://vpn.example.com`
3. Check BunkerWeb logs: `docker compose logs bunkerweb | grep -i acme`
4. Ensure `EMAIL_LETS_ENCRYPT` is set

### Panel Not Accessible

```bash
# Check all containers are running
docker compose ps

# Check panel logs
docker compose logs -f amnezia-panel

# Check panel health
curl -v http://localhost:5000/

# If using bunkerweb, check via bunkerweb
curl -v https://vpn.example.com/ -k  # -k to skip cert verify if not ready
```

### MTProxyL Not Starting

1. Check that MTProxyL is installed:
   ```bash
   mtproxyl status
   ```
2. Check MTProxyL logs:
   ```bash
   mtproxyl logs
   ```
3. Run a health check:
   ```bash
   mtproxyl health
   ```
4. Verify port 18443 is not in use:
   ```bash
   sudo lsof -i :18443
   ```

### Disk Space

Docker logs can fill up disk. Set up log rotation via Docker daemon.json:

```json
{
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  }
}
```

Apply with:

```bash
sudo systemctl restart docker
```

### Reset Everything

```bash
docker compose down -v   # removes volumes (WARNING: destroys data)
docker compose pull
docker compose up -d
```

To reset only data:

```bash
docker compose down
rm -rf data
mkdir data
docker compose up -d
```

---

## Data Directory Structure

After running the stack, your directory looks like:

```
.
├── docker-compose.yml
├── .env
├── data/                  # Panel data (persist this)
│   └── panel.db           # SQLite database
└── bw-data/               # BunkerWeb certs and cache (Docker volume)

# MTProxyL is installed at /opt/mtproxyl/ — its config and logs are managed there.
# MTProxyL is NOT part of the docker-compose stack.
```

> Never delete the `data/` directory — it contains your SQLite DB with all users and servers.

---

For more details on panel configuration, see the README.md or the inline comments in `.env`.
