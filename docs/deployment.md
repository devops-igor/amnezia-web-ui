# Amnezia Web Panel — Deployment Guide

This guide covers all deployment scenarios for the Amnezia Web Panel using Docker Compose, from a quick local standalone install to a hardened production stack with BunkerWeb WAF and Telemt telemetry.

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
                                   |
                         +---------v---------+
                         |     telemt        |
                         |  (port 18443)     |  <-- network telemetry
                         +-------------------+
```

| Component | Image | Purpose | Port |
|-----------|-------|---------|------|
| `amnezia-panel` | `ghcr.io/devops-igor/amnezia-web-ui` | Admin + user web panel | 5000 |
| `bunkerweb` | `bunkerity/bunkerweb-all-in-one` | WAF + reverse proxy + SSL | 80/443 |
| `telemt` | `raylabpro/telemt:debian` | Network telemetry collector | 18443 |
| `docker-proxy` | `tecnativa/docker-socket-proxy` | Restricted Docker API for BunkerWeb | — |

Profiles control which services start:

| Command | Services started |
|---------|-----------------|
| `docker compose up -d` | panel only |
| `docker compose --profile bunkerweb up -d` | panel + bunkerweb + docker-proxy |
| `docker compose --profile telemt up -d` | panel + telemt |
| `docker compose --profile bunkerweb --profile telemt up -d` | all three |
| `docker compose --profile bunkerweb --profile telemt -d` | all three |

---

## Prerequisites

- **Docker** with the compose plugin (v2+) — verify with `docker compose version`
- **Domain name** pointed to your server (for production HTTPS/Let's Encrypt)
- **Ports 80 and 443** open on the server firewall
- **Ports 5000 and 18443** accessible if you use standalone or telemt directly
- **UFW or iptables** to restrict access to admin ports
- For Telemt: a `config.toml` file in your config directory (see [Telemt config](#adding-telemt))

---

## Quick Start — Standalone (Local)

Use this for single-node local testing or development. No domain, no SSL, no WAF.

```bash
# 1. Clone the repository
git clone https://github.com/devops-igor/amnezia-web-ui.git
cd amnezia-web-ui

# 2. Create environment file
cp .env.example .env

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

Create a data directory for panel persistence:

```bash
mkdir -p data
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

# Domain for Let's Encrypt + vhost
SERVER_NAME=vpn.example.com

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
- BunkerWeb UI: `https://vpn.example.com/ui` (if `USE_UI=yes`)

> First cert request can take up to 60 seconds. If you see a certificate error immediately, wait and retry.

### Auto-Renewal

Let's Encrypt certificates auto-renew 30 days before expiry. No manual action needed.

### BunkerWeb UI

When `USE_UI=yes`, the BunkerWeb admin interface is available at the same domain on port 8443 (HTTPS only). Use it to view real-time request logs, ban IPs, and adjust WAF sensitivity.

---

## Adding Telemt

Telemt is a network telemetry collector (MTProxy, SNMP, flow monitoring). It runs alongside the panel and bunkerweb.

### Prerequisites

Create a config directory with a `config.toml`:

```bash
mkdir -p telemt-config
# Place your config.toml or other telemt config in telemt-config/
```

### Start with Telemt

```bash
# With BunkerWeb + Telemt
docker compose --profile bunkerweb --profile telemt up -d

# Standalone + Telemt
docker compose --profile telemt up -d
```

Telemt is available at `http://localhost:18443` (standalone) or via the reverse proxy when bunkerweb is active.

---

## Environment Variable Reference

| Variable | Default | Required | Description |
|----------|---------|----------|-------------|
| `SECRET_KEY` | — | **Yes** | Session signing key. Generate with `python3 -c "import secrets; print(secrets.token_hex(32))"` |
| `AMNEZIA_IMAGE` | `ghcr.io/devops-igor/amnezia-web-ui:latest` | No | Docker image tag. Pin to a version tag or SHA in production |
| `APP_PORT` | `5000` | No | Direct panel port. Note: `APP_PORT=` (empty) maps a random ephemeral port due to Docker behavior, not "no port". Use BunkerWeb + firewall for true isolation |
| `DATA_DIR` | `./data` | No | Host directory for panel SQLite DB and config persistence |
| `TRUSTED_PROXIES` | `172.18.0.0/24` | No | CIDR list of trusted proxies for X-Forwarded-For resolution |
| `SERVER_NAME` | `localhost` | No | Primary domain for BunkerWeb vhost and Let's Encrypt |
| `AUTO_LETS_ENCRYPT` | `yes` | No | Enable automatic Let's Encrypt certificate generation |
| `EMAIL_LETS_ENCRYPT` | — | **Yes (when AUTO_LETS_ENCRYPT=yes)** | Email for Let's Encrypt expiry warnings |
| `BUNKERWEB_VERSION` | `1.6.9` | No | BunkerWeb image version tag |
| `USE_UI` | `yes` | No | Enable BunkerWeb admin UI at `/ui` |
| `USE_GZIP` | `yes` | No | Enable gzip HTTP response compression |
| `USE_MODSECURITY` | `yes` | No | Enable ModSecurity WAF rules |
| `USE_CROWDSEC` | `no` | No | Enable CrowdSec integration (requires separate CrowdSec setup) |
| `MULTISITE` | `yes` | No | Enable BunkerWeb multi-site mode (required for per-site config via labels) |
| `USE_WHITELIST_IP` | `yes` | No | Enable IP whitelist at BunkerWeb level |
| `WHITELIST_IP_LIST` | — | No | Comma-separated IPs/CIDRs for whitelist. Example: `203.0.113.50,203.0.113.100,10.0.0.0/24` |
| `USE_BAD_BEHAVIOR` | `yes` | No | Block known malicious bots via Bad Behavior module |
| `USE_LIMIT_REQ` | `yes` | No | Enable ModSecurity request rate limiting |
| `USE_LIMIT_CONN` | `yes` | No | Enable ModSecurity connection limiting |
| `TELEMT_LOG` | `info` | No | Telemt log level: `trace`, `debug`, `info`, `warn`, `error` |
| `TZ` | `UTC` | No | Server timezone for Telemt timestamps |
| `TELEMT_CONFIG_DIR` | `./telemt-config` | No | Host directory containing telemt `config.toml` |

---

## IP Whitelisting

IP whitelisting operates at the BunkerWeb layer, before traffic reaches the panel.

### How It Works

1. `USE_WHITELIST_IP=yes` in `.env` tells BunkerWeb to activate its whitelist engine.
2. `WHITELIST_IP_LIST=203.0.113.50,203.0.113.100` provides the allowed IPs/CIDRs.
3. BunkerWeb returns HTTP 403 for any request from an IP not in the list.

### Configuration

```env
USE_WHITELIST_IP=yes
WHITELIST_IP_LIST=203.0.113.50,203.0.113.100,10.0.0.0/24
```

### Using Whitelist from Panel Labels (Per-Site Override)

The panel's Docker labels can override the global whitelist for the panel site specifically. In `docker-compose.yml`, the panel service has:

```yaml
labels:
  - bunkerweb.USE_WHITELIST_IP=${USE_WHITELIST_IP:-yes}
  - bunkerweb.WHITELIST_IP_LIST=${WHITELIST_IP_LIST:-}
```

This means the panel site inherits whatever you set in `.env`, but you can override per service by changing the label values directly in `docker-compose.yml`.

### Admin IP Only Setup

```env
USE_WHITELIST_IP=yes
WHITELIST_IP_LIST=203.0.113.50   # your current IP only
```

> Find your IP: `curl -s https://ifconfig.me` or `curl -s https://api.ipify.org`

---

## SSL/TLS

### Auto Let's Encrypt

When `AUTO_LETS_ENCRYPT=yes` and `SERVER_NAME` points to your server, BunkerWeb automatically requests and renews a Let's Encrypt certificate. No manual certificate handling required.

```env
SERVER_NAME=vpn.example.com
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

### Telemt Not Starting

1. Check that `telemt-config/` exists and contains a valid config file:
   ```bash
   ls -la telemt-config/
   ```
2. Check Telemt logs:
   ```bash
   docker compose logs telemt
   ```
3. Verify port 18443 is not in use:
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
├── telemt-config/         # Telemt config (create if using telemt)
│   └── config.toml
└── bw-data/               # BunkerWeb certs and cache (Docker volume)
```

> Never delete the `data/` directory — it contains your SQLite DB with all users and servers.

---

For more details on panel configuration, see the README.md or the inline comments in `.env`.