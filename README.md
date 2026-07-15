# Amnezia Web Panel

A self-hosted web administration panel for managing AmneziaWG, Xray (XTLS-Reality), MTProxyL, and AmneziaDNS servers. Forked from [PRVTPRO/Amnezia-Web-Panel](https://github.com/PRVTPRO/Amnezia-Web-Panel), but rebuilt from the ground up with a new data layer, expanded protocol support, and a self-service user portal.

Originally inspired by AmneziaVPN, this panel lets administrators manage users, servers, and VPN connections through a modern web interface — while end users can provision and manage their own connections without admin involvement.

---

## Features

### Multi-Protocol VPN Management

| Protocol | Description |
|----------|-------------|
| **AmneziaWG** | WireGuard-based with S3/S4 obfuscation to defeat deep packet inspection (DPI) |
| **Xray (XTLS-Reality)** | Stealthy protocol masking VPN traffic as standard HTTPS |
| **MTProxyL (Telegram Proxy)** | Full-featured Telegram MTProxy with TLS emulation, NFT Smart By-MEKO, Selfmask, geo-blocking, quotas, IP limits, and session tracking |
| **AmneziaDNS** | Internal DNS resolver preventing leaks and blocking |

### User Self-Service Portal

Regular (non-admin) users get their own **My Connections** page at `/my`:

- Create VPN connections — select server and protocol, no admin needed
- View connection details — server name, protocol, creation date
- Download configuration files, QR codes, or VPN key links
- Generate password-protected share links for configs
- Fully compatible with official AmneziaVPN and AmneziaWG clients

### Admin Capabilities

- Add/remove VPN servers via SSH (password or private key)
- Install and uninstall protocols per server
- User management — create, suspend, set traffic limits and expiration
- Per-user connection rate limiting and global connection quotas
- **AWG per-connection speed limiting** — bidirectional IFB shaping (download/upload)
- **Global bandwidth pool** and **default per-connection speed limits**
- **Bulk apply** default speed limits to all connections
- Password-protected share links for configs
- Remnawave user sync
- Server health monitoring and one-click reboot
- Startup reconciliation — automatic stale connection cleanup
- Background task supervisor — quota enforcement, overquota disable, traffic sync
- Traffic usage leaderboard
- Dark/light mode toggle

### Security

- Role-based access: Admin, Support, Regular User
- Connection rate limiting (sliding window) to prevent abuse
- Per-user traffic limits with auto-disable on exhaustion
- Account expiration enforcement
- SSH keys preferred over passwords
- CSRF protection (`starlette-csrf`)
- Login captcha (`multicolorcaptcha`)
- Credential encryption at rest (Fernet via `cryptography`)

---

## Internationalization

The panel ships with runtime language switching for 5 languages:

- English (`en`)
- Russian (`ru`)
- Chinese (`zh`)
- French (`fr`)
- Farsi (`fa`)

Users can switch language from the UI header; translations are stored in `translations/`.

---

## Screenshots

### Setup Wizard

![Setup Wizard](docs/setup-wizard.png)

First-run setup wizard — create the admin account.

### Dashboard After Setup

![Dashboard after setup](docs/dashboard-after-setup.png)

Dashboard after completing setup.

---

## Prerequisites

- **Python 3.10+**
- **SQLite3** (bundled with Python)
- Target servers: **Ubuntu 20.04/22.04/24.04** (x86_64 or ARM64)
- SSH access to target servers (password or private key)

---

## Installation

```bash
git clone https://github.com/devops-igor/amnezia-web-ui.git
cd amnezia-web-ui

python -m venv venv
source venv/bin/activate

pip install -r requirements.txt
```

If migrating from an existing `data.json` setup:

```bash
python migrate_to_sqlite.py
```

Then start the panel:

```bash
python app.py
```

The panel will be available at `http://localhost:5000`.

### First Login

On first startup with an empty database, the panel redirects all requests to the **Setup Wizard** at `/setup`. You choose your own username and password — no random credentials in logs, no forced password change.

---

## Running with Docker

The recommended way to run the panel is with Docker Compose. See the [Deployment Guide](docs/deployment.md) for full production setup including HTTPS/WAF via BunkerWeb.

### Quick Start (Standalone)

```bash
git clone https://github.com/devops-igor/amnezia-web-ui.git
cd amnezia-web-ui
cp .env.example .env
# Edit .env — set SECRET_KEY (generate with: python3 -c "import secrets; print(secrets.token_hex(32))")
docker compose up -d
```

Panel available at **http://localhost:5000**. API docs at http://localhost:5000/docs.

### Production (BunkerWeb WAF + HTTPS)

```bash
# Point your domain DNS A record to this server first
# Edit .env — set PANEL_DOMAIN, EMAIL_LETS_ENCRYPT, SECRET_KEY
docker compose --profile bunkerweb up -d
```

Panel available at **https://your-domain.com** with automatic Let's Encrypt SSL.

> **Note:** MTProxyL is installed on the host as a system service (`mtproxyl install`), not as a Docker Compose profile. The panel communicates with the MTProxyL daemon running on the host. See [docs/deployment.md](docs/deployment.md) for MTProxyL installation steps.

For full setup details (SSL, IP whitelisting, security hardening, troubleshooting), see [docs/deployment.md](docs/deployment.md).

---

## API Documentation

The panel ships with self-documenting API endpoints:

- **Swagger UI**: `http://localhost:5000/docs`
- **ReDoc**: `http://localhost:5000/redoc`

Note: API authentication is session-based (cookie). A dedicated public REST API for external integrations is not currently implemented.

---

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SECRET_KEY` | `dev-secret-key` | Session signing key — **change in production** |
| `DATABASE_PATH` | `panel.db` | Path to SQLite database file |

### Rate Limiting

Connection rate limits are configurable globally via **Settings → Connection Limits**:

- Connection rate limit (requests per time window)

### Traffic Limits

Admins can set per-user traffic limits (bytes). When a user hits their limit, their account is automatically suspended.

### AWG Speed Limits

Per-server AWG speed limits are configured via **Settings → AWG Speed Limit Settings**:

- **Global bandwidth pool** — total download/upload ceiling for the server (Mbps, `0` = unlimited)
- **Default per-connection speed limits** — download/upload applied to new AWG connections (Mbps, `0` = unlimited)
- **Bulk apply** — push the default limits to all existing AWG connections with one action

Per-connection overrides can also be set when adding or editing an AWG connection.

---

## Technology Stack

- **Backend**: FastAPI / Starlette (Python)
- **Frontend**: Vanilla JS, Jinja2 templates, custom CSS (glassmorphism design, dark/light mode)
- **Database**: SQLite (WAL mode) — threaded, concurrent-safe
- **SSH**: Paramiko
- **Security**: bcrypt password hashing, CSRF protection (`starlette-csrf`), login captcha (`multicolorcaptcha`), rate limiting (`slowapi`), credential encryption at rest (`cryptography` / Fernet)
- **Background Tasks**: async orchestrator with startup reconciliation
- **Testing**: pytest (1008 tests), Playwright E2E
- **i18n**: 5-language runtime translations

---

## Testing

The project uses pytest as its primary test runner:

```bash
pytest
```

- **1008 non-E2E tests** covering managers, routers, schemas, database, and background tasks
- **Playwright E2E tests** for critical user flows (run with `pytest -m e2e`)
- **black** formatting enforced (`black --check .`)
- **flake8** linting enforced (`flake8 .`)

CI runs the full suite on every PR.

---

## Security Recommendations

- Run behind a reverse proxy (Nginx/Caddy/BunkerWeb) with SSL termination
- Set a strong `SECRET_KEY` environment variable in production
- Prefer SSH keys over passwords for server connections
- Restrict access to the panel via firewall/network segmentation
- Enable IP whitelisting in BunkerWeb for the panel domain
- The first-run setup wizard ensures no credentials are exposed in container logs

---

## Recent Changes

Major updates since the last README refresh:

- **MTProxyL migration** — replaced Telemt with MTProxyL (SSH+CLI based); 13 users migrated
- **AWG per-connection speed limiting** — IFB-based bidirectional shaping (download/upload)
- **Global bandwidth pool and default limits** — configurable per server
- **Bulk apply speed limits** — apply defaults to all connections at once
- **Batch tc commands** — reduced "Apply to all" from 700+ SSH calls to 2
- **Startup reconciliation** — automatic cleanup of stale connections on boot
- **Share links** — password-protected config sharing
- **Background task orchestrator** — centralized traffic sync, quota enforcement, Remnawave sync
- **CSRF protection and login captcha**
- **Credential encryption at rest**
- **Dark/light mode toggle**
- **i18n** — English, Russian, Chinese, French, Farsi
- **Test suite** — grew to 1008 tests

---

## Contributing

Issues and pull requests are welcome. When submitting a PR, please ensure:

- All pytest tests pass (`pytest`)
- No black formatting violations (`black --check .`)
- No flake8 linting violations (`flake8 .`)
