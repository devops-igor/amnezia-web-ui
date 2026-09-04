# Amnezia Web Panel

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Docker Image Size](https://img.shields.io/badge/Docker%20Image-~38.6MB-blue?style=flat&logo=docker)](https://github.com/devops-igor/amnezia-web-ui)
[![Memory Footprint](https://img.shields.io/badge/RAM%20Usage-~15MB-success?style=flat)]()
[![Startup Time](https://img.shields.io/badge/Cold%20Startup-%3C50ms-brightgreen?style=flat)]()
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

A high-performance, self-hosted web administration panel and load-balancing VPN portal for managing AmneziaWG, Xray (XTLS-Reality), MTProxyL, and AmneziaDNS servers. Built from the ground up as a pure-Go single binary with zero CGO dependencies, ultra-lean Alpine runtime, and native userspace AmneziaWG engine integration.

Originally inspired by AmneziaVPN, this panel allows administrators to manage users, remote servers, and VPN connections through an intuitive web interface — while end users can self-provision and download their own VPN configs without admin intervention.

---

## Key Highlights & Performance

- **Ultra-Lean Production Image**: **~38.6 MB** total image size on Alpine 3.22 (reduced from ~280 MB in legacy Python/Flask).
- **Minimal Resource Footprint**: **~15 MB RSS RAM** runtime memory under typical production workload.
- **Instantaneous Cold Startup**: Starts in **< 50 milliseconds** with fail-fast SQLite preflight checks.
- **Pure-Go Architecture**: Zero CGO dependencies (`modernc.org/sqlite`), cross-compiles cleanly to x86_64 and ARM64.
- **Non-Root Hardened Security**: Runs as unprivileged `appuser` (UID 1000, GID 1000) with `read_only` rootfs, `cap_drop: [ALL]`, and `cap_add: [NET_ADMIN]`.
- **In-Process AmneziaWG Subsystem**: Native in-process userspace VPN endpoint (`github.com/amnezia-vpn/amneziawg-go`) providing centralized single-entrypoint tunneling and intelligent load balancing across backend VPN nodes.

---

## Features

### Multi-Protocol VPN Management

| Protocol | Description |
| :--- | :--- |
| **AmneziaWG** | WireGuard-based with S3/S4 obfuscation to defeat deep packet inspection (DPI) |
| **Xray (XTLS-Reality)** | Stealthy protocol masking VPN traffic as standard TLS 1.3 HTTPS traffic |
| **MTProxyL (Telegram Proxy)** | Telegram MTProto proxy with TLS emulation, NFT Smart By-MEKO, Selfmask, quotas, and session tracking |
| **AmneziaDNS** | Internal Unbound DNS resolver preventing DNS leaks and blocking |
| **In-Process VPN Portal** | Optional native load balancer routing user AWG traffic to backend server pools |

### User Self-Service Portal (`/my`)

Regular (non-admin) users get their own **My Connections** dashboard:

- **Self-Provisioning**: Create VPN connections on any available server and protocol without admin intervention.
- **Config Formats**: Download `.conf` profiles, scan dynamic QR codes, or copy official `vpn://` key links.
- **Share Links**: Generate password-protected, expirable sharing links for friends and family.
- **Client Compatibility**: 100% compatible with official AmneziaVPN and AmneziaWG mobile and desktop clients.

### Admin Capabilities

- **Remote Server Orchestration**: Add and configure VPN servers via SSH (password or RSA/Ed25519/ECDSA private keys).
- **Lifecycle Management**: Install, configure, start, stop, and uninstall protocols per server with one click.
- **User Management**: Create, suspend, set traffic limits, expiration dates, and custom connection quotas.
- **AWG Speed Limiting**: Bidirectional IFB traffic shaping (download/upload) per connection or globally via bulk apply.
- **Traffic Accounting & Leaderboard**: Real-time traffic deltas, monthly rollovers, and historical leaderboard snapshots.
- **Automated Rebalancing & Health Probing**: Pure-Go Noise IK handshakes and background health probes over UDP.
- **RemnaWave User Sync**: Two-way user and connection synchronization with RemnaWave billing APIs.
- **Appearance & Dark Mode**: Dynamic branding customization and dark/light glassmorphic UI.

### Security Architecture

- **Role-Based Access Control (RBAC)**: Distinct permissions for `Admin`, `Support`, and `User`.
- **Credential Encryption at Rest**: SSH passwords, private keys, and SSL certificates are encrypted via Fernet-compatible AES-128-CBC + HMAC-SHA256 (HKDF-SHA256 derived from master `SECRET_KEY`).
- **Bcrypt with 72-Byte Pre-Hash Safeguard**: Secure user password authentication using bcrypt cost 12 with SHA-256 pre-hashing to eliminate the 72-byte truncation boundary vulnerability.
- **Double-Submit CSRF Protection**: Constant-time token verification for state-changing HTTP requests.
- **Token-Bucket Rate Limiter**: In-memory sliding window rate limiting with background garbage collection.
- **Automated Directory Writability Preflight**: Preflight probe verifying `./data` ownership (UID 1000:1000) on startup with actionable remediation guidance.

---

## Internationalization (i18n)

The panel includes built-in runtime language switching for 5 languages:

- English (`en`)
- Russian (`ru`)
- Chinese (`zh`)
- French (`fr`)
- Farsi (`fa`)

---

## Technology Stack

```
                     +-----------------------------------+
                     |       Amnezia Web Panel (Go)      |
                     |  Chi v5 HTTP Router (64 routes)   |
                     |  html/template Engine (go:embed)  |
                     +-----------------+-----------------+
                                       |
          +----------------------------+----------------------------+
          |                            |                            |
+---------v---------+        +---------v---------+        +---------v---------+
|    Database Layer |        | Protocol Managers |        |   In-Process VPN  |
|  modernc.org/     |        | SSH / SFTP Pool   |        |   amneziawg-go    |
|  sqlite (WAL Mode)|        | AWG / MTProxyL    |        |   Load Balancer   |
+-------------------+        +-------------------+        +-------------------+
```

- **Runtime & Language**: Go 1.22+ (`cmd/panel/main.go`)
- **HTTP Routing**: `github.com/go-chi/chi/v5`
- **Database Engine**: `modernc.org/sqlite` (Pure Go, WAL mode, serialized writes, zero locking issues)
- **VPN Core**: `github.com/amnezia-vpn/amneziawg-go` (Pure-Go userspace AWG engine & Noise IK probing)
- **SSH & SFTP**: `golang.org/x/crypto/ssh` and `github.com/pkg/sftp`
- **Cryptography**: `crypto/aes`, `crypto/cipher`, `crypto/hkdf`, `crypto/hmac`, `golang.org/x/crypto/bcrypt`
- **Templates & Static Assets**: Go standard library `html/template` with `embed.FS`
- **Background Orchestrator**: `golang.org/x/sync/errgroup` + `context.Context`

---

## Deployment & Quick Start

The recommended production deployment method is Docker Compose. See [`docs/deployment.md`](docs/deployment.md) for full instructions and BunkerWeb WAF integration.

### Quick Start (Standalone Docker)

```bash
# 1. Clone repository
git clone https://github.com/devops-igor/amnezia-web-ui.git
cd amnezia-web-ui

# 2. Setup environment and data directory
cp .env.example .env
mkdir -p data
sudo chown -R 1000:1000 data

# 3. Generate master SECRET_KEY
SECRET_KEY=$(openssl rand -hex 32)
sed -i "s/your-32-byte-hex-secret-key-here/$SECRET_KEY/" .env

# 4. Start panel
docker compose up -d
```

Access the panel in your browser at `http://localhost:5000`.

### Production Deployment (BunkerWeb WAF + Let's Encrypt HTTPS)

```bash
# Edit .env with your PANEL_DOMAIN and EMAIL_LETS_ENCRYPT
docker compose --profile bunkerweb up -d
```

### Upgrading from Legacy Python Installations

Upgrading from the legacy Python container to the Go container requires zero data migration scripts. The Go application automatically applies additive SQLite schemas upon first boot.

See the step-by-step **[Production Migration Runbook](docs/migration-runbook.md)** for detailed instructions:
1. Back up `data/panel.db` and preserve `SECRET_KEY`.
2. Update directory ownership: `sudo chown -R 1000:1000 ./data`.
3. Update `docker-compose.yml` to the Go image and restart.

---

## Development & Testing

### Building Locally

```bash
cd amnezia-web-ui-go
make build
# Binary produced at bin/panel
```

### Running Test Suites & Quality Gates

```bash
cd amnezia-web-ui-go

# Run unit tests with race detection and statement coverage
go test -race -cover ./...

# Run code linter
golangci-lint run ./...

# Run static security scanner
gosec -quiet ./...

# Run vulnerability audit
govulncheck ./...

# Run Playwright E2E test suite (from repo root)
make test-e2e
```

---

## Contributing

Contributions and pull requests are welcome! Please ensure:
1. All Go unit tests pass with race detector enabled: `go test -race ./...` (0 data races, statement coverage $\ge 85\%$).
2. `golangci-lint` and `gosec` report 0 issues.
3. Code is formatted with `go fmt ./...`.

---

## License

This project is licensed under the MIT License — see the [LICENSE](LICENSE) file for details.
