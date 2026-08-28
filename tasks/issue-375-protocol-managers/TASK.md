# TASK-375: Phase 4 — Protocol Managers (AWG, MTProxyL, DNS)

## Overview & Objective
Implement the complete remote protocol manager subsystem in `amnezia-web-ui-go/internal/manager/` to replace the Python protocol manager implementations for the three supported protocols:
1. **AmneziaWG (AWG)** (`internal/manager/awg`)
2. **MTProxyL / TeleMT** (`internal/manager/mtproxyl`)
3. **DNS (Unbound)** (`internal/manager/dns`)

All managers must satisfy the unified `manager.ProtocolManager` interface in `internal/manager/manager.go`, integrate seamlessly with the existing `SSHClient` interface (`internal/manager/ssh`), and register in the `manager.Registry`.

---

## 1. Specification References & Ground Truth
- [`docs/specs/04-external-services.md`](../../docs/specs/04-external-services.md) (Sections 2, 3, 4, 5, 6)
- [`docs/specs/01-domain-model.md`](../../docs/specs/01-domain-model.md)
- [`docs/plans/2026-08-25-go-rewrite.md`](../../docs/plans/2026-08-25-go-rewrite.md) (Section 3.4, Phase 4)
- Python source implementations for reference:
  - `app/managers/awg_manager.py`
  - `app/managers/awg_cps.py`
  - `app/managers/awg_tc.py`
  - `app/managers/awg_health.py`
  - `app/managers/telemt_manager.py`
  - `dns_manager.py`

---

## 2. Package Architecture & Sub-Task Requirements

```
amnezia-web-ui-go/internal/manager/
├── manager.go                      # ProtocolManager interface, Registry, MockProtocolManager (existing)
├── awg/
│   ├── awg.go                     # AWGManager implementing ProtocolManager, client CRUD, container lifecycle
│   ├── params.go                  # Curve25519 keypair, PSK, obfuscation params (lite, standard, pro), H1-H4 quadrants
│   ├── config.go                  # wg0.conf and clients.json template rendering, IP allocation math
│   ├── cps/
│   │   ├── cps.go                 # GenerateCPSPackets, GenerateConnectionKit, SelectMimicryDomain
│   │   ├── tls.go                 # GenTLS: TLS 1.3/1.2 ClientHello byte assembly
│   │   ├── quic.go                # GenQUICInitial, GenQUICShort: QUIC v1 Initial & Short header packet synthesis
│   │   ├── dns.go                 # GenDNS: Binary DNS query packet synthesis
│   │   └── sip.go                 # GenSIP: SIP INVITE ASCII packet synthesis
│   ├── tc/
│   │   └── tc.go                  # Linux Traffic Control (tc/htb/ifb), peer rate limits, class ID calculation
│   └── health/
│       ├── probe.go               # Noise IK UDP reachability probe & RTT latency measurement
│       └── noise.go               # Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s cryptographic primitives & packet builders
├── mtproxyl/
│   ├── mtproxyl.go                # MTProxyLManager implementing ProtocolManager, CLI execution, client CRUD
│   ├── secrets.go                 # /opt/mtproxyl/secrets.conf parser & serializer
│   └── stats.go                   # Traffic parsing (Russian multipliers Б, КБ, МБ, ГБ, ТБ), connection table parsing, quota enforcement
└── dns/
    ├── dns.go                     # DNSManager implementing ProtocolManager, container lifecycle
    └── unbound.go                 # /opt/amnezia/dns/unbound.conf and forward-records.conf rendering & DNS query probe
```

---

### Detailed Deliverables

### A. AmneziaWG (AWG) Manager (`internal/manager/awg`)

1. **Parameters & Key Generation (`params.go`):**
   - `GenerateWGKeypair() (privateKeyBase64, publicKeyBase64 string, err error)` using `crypto/rand` and `golang.org/x/crypto/curve25519`.
   - `GeneratePSK() (string, error)` generating 32 random bytes in base64.
   - `GenerateQuadrantHeaders() (h1, h2, h3, h4 uint32)`: Non-overlapping quadrant method across `[5, 2^31 - 1]` with minimum span $\ge 1000$.
   - `GenerateAWGParams(profile string) (*AWGParams, error)`: Supports profiles `lite`, `standard` (default), `pro` matching parameter bounds for `Jc`, `Jmin`, `Jmax`, `S1-S4`, `H1-H4`, MTU (`1280` or `1320`), enforcing $|S1 - S2| \ge 10$.

2. **Binary CPS Packet Crafting (`cps/`):**
   - `GenTLS(domain string, targetLen int) ([]byte, error)`: TLS 1.3 / 1.2 ClientHello with TLS Record Header, ClientRandom, SessionID, 16 CipherSuites, SNI extension for target domain, Supported Groups (X25519, P-256, P-384), KeyShare (X25519 public key), ALPN (`h2`, `http/1.1`).
   - `GenQUICInitial(domain string, targetLen int) ([]byte, error)`: QUIC v1 Initial packet (`0xC0 | rand`), 8-byte DCID, 8-byte SCID, TLS 1.3 ClientHello payload, padded to target length (1200–1280 bytes).
   - `GenQUICShort(domain string, targetLen int) ([]byte, error)`: QUIC 1-RTT short header packet.
   - `GenDNS(domain string) ([]byte, error)`: Binary DNS query packet with random Transaction ID, Standard Query flag `0x0100`, Question count 1, QNAME labels, Type A `0x0001`, Class IN `0x0001`.
   - `GenSIP(domain string) ([]byte, error)`: Valid ASCII SIP `INVITE` request.
   - `GenerateCPSPackets(profile string) (map[string]string, error)`: Generates `I1-I5` formatted as `<b 0xHEXBYTES>`.
   - `GenerateConnectionKit(server *models.Server, user *models.User, client *AWGClient) (map[string]string, error)`: Generates native Amnezia `.vpn` JSON backup/kit containing all AWG parameters.

3. **Remote Traffic Control (`tc/`):**
   - `SetupIFB(ctx context.Context, sshClient manager.SSHClient, iface string, ifbDev string) error`: Loads `ifb` kernel module, sets up egress HTB root on `awg0`, redirects ingress to `ifb0` via `mirred`.
   - `ApplyPeerSpeedLimit(ctx context.Context, sshClient manager.SSHClient, iface string, ifbDev string, classID int, peerIP string, limitDownMbit int, limitUpMbit int) error`: Configures HTB class & u32 filter for egress and ingress.
   - `RemovePeerSpeedLimit(ctx context.Context, sshClient manager.SSHClient, iface string, ifbDev string, classID int) error`.
   - `ClearSpeedLimits(ctx context.Context, sshClient manager.SSHClient, iface string, ifbDev string) error`.

4. **Noise IK Handshake Health Probes (`health/`):**
   - Implement `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` handshake protocol using `golang.org/x/crypto/blake2s`, `golang.org/x/crypto/chacha20poly1305`, and `golang.org/x/crypto/curve25519`.
   - Handshake initiation packet construction:
     - Ephemeral keypair generation.
     - DH exchanges (ss1 = e_priv * ServerPubKey, ss2 = ClientPrivKey * ServerPubKey).
     - Static key encryption with ChaCha20Poly1305.
     - TAI64N timestamp generation & encryption (`0x400000000000000A + unix_sec`).
     - 116-byte message body assembly (`H1` little-endian || random sender_idx || e_pub || encrypted_static || encrypted_timestamp).
     - MAC1 calculation (BLAKE2s-128 with key `BLAKE2s-256("mac1----" || ServerPubKey)`).
     - Packet framing: `S1` random bytes || message body || MAC1 || 16 zero bytes (MAC2). Total length: `S1 + 148` bytes.
   - Response packet verification:
     - Unpack after `S2` junk bytes.
     - Verify message type (`H2` or `2`) and receiver index == sender index.
     - Ephemeral public key extraction, DH exchanges (ss3, ss4), KDF3 with PSK.
     - Decrypt 16-byte empty authentication payload with `key3`.
   - `ProbeAWGEndpoint(ctx context.Context, endpoint string, serverPubKey string, clientPrivKey string, psk string, h1, h2 uint32, s1, s2 int, timeout time.Duration) (time.Duration, error)`: Pure-Go UDP probe measuring round-trip latency.

5. **AWG Protocol Manager Lifecycle (`awg.go` & `config.go`):**
   - Implements `manager.ProtocolManager`:
     - `Protocol() string` -> `"awg"`
     - `Install(ctx context.Context, server *models.Server, params map[string]any) error`: Pull/run `amneziavpn/amneziawg-go:latest` with `CAP_NET_ADMIN` and `/dev/net/tun`, render and upload `/opt/amnezia/awg/wg0.conf` and `/opt/amnezia/awg/clients.json`.
     - `Uninstall(ctx context.Context, server *models.Server) error`: Stop & remove `amnezia-awg` container and config files.
     - `GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error)`: Read and parse `/opt/amnezia/awg/clients.json`.
     - `AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error)`: Allocate next sequential IP in subnet, generate client keypair/PSK, update `wg0.conf` and `clients.json`, apply live `awg set` without dropping other peers.
     - `RemoveClient(ctx context.Context, server *models.Server, clientID string) error`: Remove peer from `wg0.conf` and `clients.json`, apply `awg set wg0 peer <pubkey> remove`.
     - `GetClientConfig(ctx context.Context, server *models.Server, clientID string) (string, error)`: Render client `.conf` file with `[Interface]` (PrivateKey, Address, DNS, Jc, Jmin, Jmax, S1, S2, H1-H4) and `[Peer]` (PublicKey, PresharedKey, Endpoint, AllowedIPs).

---

### B. MTProxyL / TeleMT Manager (`internal/manager/mtproxyl`)

1. **CLI Execution & Secrets Config (`mtproxyl.go`, `secrets.go`):**
   - Binary path: `/usr/local/bin/mtproxyl`.
   - `Install(ctx, server, params)`: Download and run install script if missing, configure port (default 443 or fallback 18443) and FakeTLS domain, start service.
   - `Uninstall(ctx, server)`: Stop service and remove files.
   - Secrets file parser (`/opt/mtproxyl/secrets.conf`):
     - `LABEL|SECRET|CREATED_TS|ENABLED|MAX_CONNS|MAX_IPS|QUOTA_BYTES|EXPIRES|NOTES`.
     - Thread-safe parsing and serialization preserving metadata.

2. **Client Management:**
   - `AddClient`: `/usr/local/bin/mtproxyl secret add <username>` (sanitized `[a-zA-Z0-9_-]`, max 32 chars), extract `tg://proxy?server=...&port=...&secret=...` link via regex.
   - `RemoveClient`: `/usr/local/bin/mtproxyl secret remove <client_id>`.
   - `ToggleClient`: `/usr/local/bin/mtproxyl secret enable <client_id>` / `disable <client_id>`.
   - `GetClientConfig`: Return `tg://` proxy connection link.

3. **Traffic Stats & Quota Enforcement (`stats.go`):**
   - Parse `/usr/local/bin/mtproxyl traffic`:
     - Line format: `● <label>: ↓ <download_val> <unit>  ↑ <upload_val> <unit>  соед: <conns>`
     - Unit multipliers: `Б` (1), `КБ` (1024), `МБ` (1024²), `ГБ` (1024³), `ТБ` (1024⁴).
   - Parse `/usr/local/bin/mtproxyl connections`:
     - Extract connection count per client.
   - Quota enforcement: identify clients exceeding quota and invoke `secret disable`.

---

### C. DNS Manager (`internal/manager/dns`)

1. **Unbound Container Lifecycle (`dns.go`, `unbound.go`):**
   - Container name: `amnezia-dns`, image: `mvance/unbound:latest`.
   - Ports: `53:53/udp`, `53:53/tcp`.
   - Config path: `/opt/amnezia/dns/unbound.conf`.
   - `Install(ctx, server, params)`: Write `unbound.conf`, start container.
   - `Uninstall(ctx, server)`: Stop & destroy container, clean `/opt/amnezia/dns`.
   - `HealthCheck(ctx, server)`: Perform UDP DNS query probe against server port 53.

---

## 3. Testing & Verification Requirements

1. **Unit & Table-Driven Tests:**
   - `internal/manager/awg/*_test.go`:
     - Parameter range validity and quadrant distribution tests.
     - CPS binary layout assertions (packet sizes, headers, SNI extraction).
     - Noise IK cryptographic vector tests (KDF1, KDF2, KDF3, HMACBlake2s, handshake packet assembly matching Python vectors).
     - Config template rendering tests for `wg0.conf` and `clients.json`.
     - Traffic control command generation tests.
   - `internal/manager/mtproxyl/*_test.go`:
     - Secrets file parsing / serialization round-trip tests.
     - Traffic string parsing with diverse Russian unit combinations (`Б`, `КБ`, `МБ`, `ГБ`, `ТБ`, decimals, edge cases).
     - Connection table parsing tests.
   - `internal/manager/dns/*_test.go`:
     - Unbound configuration generation and parameter verification.
2. **Compilation & Quality Gates:**
   - `go fmt ./... && go vet ./... && go build ./...`
   - `go test -race -cover ./...` with target statement coverage $\ge 85.0\%$ across all `internal/manager/*` packages.
   - `golangci-lint run ./...` (0 issues).
   - `gosec ./...` (0 issues).
   - `govulncheck ./...` (0 app vulnerabilities).
   - Python test suite: `pytest tests/test_*.py -q` (all 1130 pass with zero regressions).

---

## 4. Handover & State Machine
Once all checks pass, output `tasks/issue-375-protocol-managers/DEV_HANDOVER.md` and append `IMPLEMENTATION_COMPLETE` to `WORKLOG.md`.
