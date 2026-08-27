# External Services & Wire Protocols Specification (`04-external-services.md`)

> **Target Packages:** `internal/manager/*`, `internal/service/remnawave`, `internal/vpn/*`  
> **Source Python Files:** `app/managers/ssh_manager.py`, `app/managers/awg_manager.py`, `app/managers/awg_cps.py`, `app/managers/awg_tc.py`, `app/managers/awg_health.py`, `app/managers/mtproxyl_manager.py`, `dns_manager.py`, `app/services/remnawave_sync.py`  
> **Status:** Ground Truth Specification for Go Rewrite

---

## 1. SSH Client & Remote Execution (`internal/manager/ssh`)

### 1.1 Architecture & Interface

All remote server management occurs via SSH using `golang.org/x/crypto/ssh` and SFTP using `github.com/pkg/sftp`.

```go
type SSHClient interface {
	RunCommand(ctx context.Context, cmd string) (stdout string, stderr string, exitCode int, err error)
	RunSudoCommand(ctx context.Context, cmd string) (stdout string, stderr string, exitCode int, err error)
	UploadFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error
	UploadSudoFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error
	DownloadFile(ctx context.Context, remotePath string) ([]byte, error)
	FileExists(ctx context.Context, remotePath string) (bool, error)
	Close() error
}
```

### 1.2 Host Key Verification & Fingerprinting

1. **Fingerprint Format:** SHA-256 fingerprint formatted as `SHA256:<base64_hash>` matching standard OpenSSH output.
2. **First-Seen Workflow:**
   - When connecting to an unverified server, capture the remote host key.
   - If server ID is not in `known_hosts`, present the fingerprint to the admin via `POST /add` response with `fingerprint_required: true`.
   - On admin confirmation (`POST /confirm-fingerprint`), save to `known_hosts` table.
3. **Strict Verification:**
   - On subsequent connections, verify the presented key against the fingerprint in `known_hosts`.
   - If mismatch detected, reject connection immediately with `ErrHostKeyMismatch` (prevent MITM).

### 1.3 Sudo Command Handling

When running commands as non-root with password:
- Sudo command wrapper: `echo '<escaped_password>' | sudo -S -p '' -- /bin/bash -c '<escaped_command>'`
- Passwords and commands must be properly shell-escaped.

---

## 2. Remote Protocol Containers & CLI Management

### 2.1 Container Specifications Matrix

| Protocol | Container Name | Docker Image | Port Bindings | Volume Mounts | Capabilities & Devices |
|----------|----------------|--------------|---------------|---------------|------------------------|
| **AWG** | `amnezia-awg` | `amneziavpn/amneziawg-go:latest` | `<port>:<port>/udp` | `/opt/amnezia/awg/wg0.conf:/etc/amnezia/amneziawg/wg0.conf:ro` | `CAP_NET_ADMIN`, `/dev/net/tun` |
| **TeleMT (MTProxyL)** | `mtproxyl` | *(Host/Script installed)* | `<port>:<port>/tcp` | `/opt/mtproxyl/secrets.conf`, `/opt/mtproxyl/settings.conf` | None |
| **DNS** | `amnezia-dns` | `mvance/unbound:latest` | `53:53/udp`, `53:53/tcp` | `/opt/amnezia/dns/unbound.conf:/opt/unbound/etc/unbound/unbound.conf:ro` | None |

---

### 2.2 MTProxyL CLI Command Integration (`internal/manager/mtproxyl`)

MTProxyL uses CLI commands over SSH (`/usr/local/bin/mtproxyl <subcommand>`):

#### 1. CLI Lifecycle & Configuration Commands:
- **Binary Path:** `/usr/local/bin/mtproxyl`
- **Secrets File:** `/opt/mtproxyl/secrets.conf`
- **Settings File:** `/opt/mtproxyl/settings.conf`
- **Check Installed:** `test -f /usr/local/bin/mtproxyl && echo found || echo not_found`
- **Installer Script:** `wget -qO /tmp/mtproxyl-install.sh https://raw.githubusercontent.com/Liafanx/MTProxyL/main/install.sh && bash /tmp/mtproxyl-install.sh`
- **Set Port:** `/usr/local/bin/mtproxyl port <port>` *(If port 443 conflicts with BunkerWeb, fallback to port 18443)*
- **Set FakeTLS Domain:** `/usr/local/bin/mtproxyl domain <tls_domain>`
- **Process Control:** `/usr/local/bin/mtproxyl start`, `/usr/local/bin/mtproxyl stop`, `/usr/local/bin/mtproxyl restart`
- **Status Query:** `/usr/local/bin/mtproxyl status --json` (returns `{"status":"running"|"stopped","port":...,"domain":"..."}`)

#### 2. Client Management Commands:
- **Add Client:** `/usr/local/bin/mtproxyl secret add <username>`
  - *Username Sanitization:* `[a-zA-Z0-9_-]` only, max 32 chars.
  - *Output Parsing:* Extracts `tg://proxy?server=...&port=...&secret=...` link from stdout via regex `tg://\S+`.
- **Set Limits:** `/usr/local/bin/mtproxyl secret setlimits <username> <max_conns> <max_ips> <quota_bytes> <expires>`
  - Format: `<max_conns> <max_ips> <quota_bytes> <expires>` (use `0` for unlimited).
- **Remove Client:** `/usr/local/bin/mtproxyl secret remove <client_id>`
- **Toggle Client:** `/usr/local/bin/mtproxyl secret enable <client_id>` / `/usr/local/bin/mtproxyl secret disable <client_id>`
- **Get Link:** `/usr/local/bin/mtproxyl secret link <client_id>`

#### 3. Secrets File Schema (`/opt/mtproxyl/secrets.conf`):
Each non-comment line is delimited by `|`:
`LABEL|SECRET|CREATED_TS|ENABLED|MAX_CONNS|MAX_IPS|QUOTA_BYTES|EXPIRES|NOTES`

#### 4. Traffic & Connection Stats Parsing:
- **Traffic Query:** `/usr/local/bin/mtproxyl traffic`
  - Output lines format: `● <label>: ↓ <download_val> <unit>  ↑ <upload_val> <unit>  соед: <conns>`
  - Size multipliers: `Б` (1), `КБ` (1024), `МБ` (1024²), `ГБ` (1024³), `ТБ` (1024⁴).
  - Total bytes = `(download_val * unit_mult) + (upload_val * unit_mult)`.
- **Connection Counts:** `/usr/local/bin/mtproxyl connections`
  - Table lines below separator `─────`: `<label>\s+(\d+)`
- **Quota Enforcement:** If `user_data.quota > 0` and `total_octets >= user_data.quota`, disable client via `secret disable <client_id>`.

---

## 3. AmneziaWG Parameters & Obfuscation Profiles (`internal/manager/awg`)

### 3.1 Keypair & PSK Generation
- **X25519 Keypair:** Curve25519 private key (32 random bytes) and public key derived via X25519 basepoint multiplication. Base64 encoded.
- **Preshared Key (PSK):** 32 cryptographically secure random bytes. Base64 encoded.

---

### 3.2 Obfuscation Profiles Parameter Ranges (`AWG_PROFILES`)

AWG generates random parameters based on the selected profile (`lite`, `standard`, `pro`):

| Parameter | Lite Profile | Standard Profile (Default) | Pro Profile |
|-----------|--------------|----------------------------|-------------|
| **Jc** (Junk packet count) | `[3, 5]` | `[5, 8]` | `[4, 16]` |
| **Jmin** (Junk packet min size) | `[5, 15]` | `[30, 80]` | `[50, 256]` |
| **Jmax** (Junk packet max size) | `[Jmin + 10, Jmin + 55]` | `[Jmin + 10, Jmin + 250]` | `[Jmin + 10, Jmin + 1000]` |
| **S1** (Init packet junk size) | `[97, 107]` | `[30, 80]` | `[15, 150]` |
| **S2** (Response packet junk size) | `[17, 27]` | `[30, 80]` | `[15, 150]` |
| **S1/S2 Gap Constraint** | `|S1 - S2| >= 10` *(retry/clamp)* | `|S1 - S2| >= 10` *(retry/clamp)* | `|S1 - S2| >= 10` *(retry/clamp)* |
| **S3** (Cookie reply junk size) | `[16, 26]` | `[15, 32]` | `[8, 64]` |
| **S4** (Transport packet junk size)| `[4, 10]` | `[10, 20]` | `[6, 31]` |
| **MTU** | `1280` | `1280` | `1320` |
| **CPS / I1-I5 Signatures** | None (empty) | Standard (QUIC I1) | Pro (Full I1-I5 chain) |

---

### 3.3 Quadrant Magic Header Generation (`H1`-`H4`)

To maximize entropy across 32-bit integer space, magic headers `H1`-`H4` are generated using non-overlapping quadrants of `[5, 2^31 - 1]`:

```go
func GenerateQuadrantHeaders() (h1, h2, h3, h4 uint32) {
	const maxVal uint32 = 2147483647 // 2^31 - 1
	const qSize uint32 = maxVal / 4   // 536870911

	headers := make([]uint32, 4)
	for i := 0; i < 4; i++ {
		lo := uint32(5 + uint32(i)*qSize)
		hi := uint32((i + 1)) * qSize
		if i < 3 {
			hi += 1
		}
		a := lo + uint32(rand.Int63n(int64(hi-lo+1)))
		b := lo + uint32(rand.Int63n(int64(hi-lo+1)))
		if a > b {
			a, b = b, a
		}
		if b-a < 1000 {
			if a+1000 <= hi {
				b = a + 1000
			}
		}
		headers[i] = a + uint32(rand.Int63n(int64(b-a+1)))
	}
	return headers[0], headers[1], headers[2], headers[3]
}
```

---

## 4. AmneziaWG Traffic Control (`awg_tc`)

Speed limits are implemented via Linux Traffic Control (`tc`) with Hierarchical Token Bucket (`htb`) and Intermediate Functional Block (`ifb`) devices on the remote host.

```bash
# 1. Load IFB kernel module and enable device
modprobe ifb numifbs=1
ip link set dev ifb0 up

# 2. Setup egress qdisc on physical/awg0 interface
tc qdisc del dev awg0 root 2>/dev/null || true
tc qdisc add dev awg0 root handle 1: htb default 30

# 3. Setup ingress redirect via IFB device
tc qdisc del dev awg0 ingress 2>/dev/null || true
tc qdisc add dev awg0 handle ffff: ingress
tc filter add dev awg0 parent ffff: protocol ip u32 match u32 0 0 action mirred egress redirect dev ifb0

# 4. Setup IFB root qdisc
tc qdisc del dev ifb0 root 2>/dev/null || true
tc qdisc add dev ifb0 root handle 1: htb default 30

# 5. Peer Rate Limits (Class ID = 1:10 + peer_index):
tc class add dev awg0 parent 1: classid 1:{{ .ClassID }} htb rate {{ .LimitDown }}mbit ceil {{ .LimitDown }}mbit
tc filter add dev awg0 protocol ip parent 1: prio 1 u32 match ip dst {{ .PeerIP }}/32 flowid 1:{{ .ClassID }}

tc class add dev ifb0 parent 1: classid 1:{{ .ClassID }} htb rate {{ .LimitUp }}mbit ceil {{ .LimitUp }}mbit
tc filter add dev ifb0 protocol ip parent 1: prio 1 u32 match ip src {{ .PeerIP }}/32 flowid 1:{{ .ClassID }}
```

---

## 5. Binary Characteristic Packet Signatures (CPS / `awg_cps`)

Binary packet signatures generate client-side `I1-I5` packets formatted as `<b 0xHEXBYTES>` in client `.conf` files.

### 5.1 TLS 1.3 / 1.2 ClientHello Layout (`gen_tls`)

* **Target Packet Structure:**
  1. **TLS Record Header (5 bytes):** `0x16` (Handshake), `0x03, 0x01` (TLS 1.0 record version), `uint16(len(handshake_msg))`.
  2. **Handshake Header (4 bytes):** `0x01` (ClientHello), 3 bytes big-endian length of payload.
  3. **Client Version (2 bytes):** `0x03, 0x03` (TLS 1.2 for compatibility).
  4. **Client Random (32 bytes):** Cryptographically secure random bytes.
  5. **Session ID (33 bytes):** `0x20` (length 32) + 32 random bytes.
  6. **Cipher Suites Block (34 bytes):** `uint16(32)` + 16 cipher suites (`0x1301, 0x1302, 0x1303, 0xC02B, 0xC02F, 0xC02C, 0xC030, 0xCCA9, 0xCCA8, 0xC013, 0xC014, 0x009C, 0x009D, 0x002F, 0x0035`).
  7. **Compression Methods (2 bytes):** `0x01, 0x00` (null).
  8. **Extensions Block:** `uint16(len(all_extensions))` + concatenated extensions:
     - **SNI (`0x0000`):** `0x00, 0x00` + length + `0x00` (host_name) + `uint16(len(domain))` + `domain_bytes`.
     - **Supported Groups (`0x000A`):** x25519 (`0x001D`), secp256r1 (`0x0017`), secp384r1 (`0x0018`).
     - **EC Point Formats (`0x000B`):** `0x00, 0x0B, 0x00, 0x02, 0x01, 0x00` (uncompressed).
     - **Signature Algorithms (`0x000D`):** ECDSA/RSA PSS/PKCS1 SHA256/384/512 (`0x0403, 0x0804, 0x0401, 0x0503, 0x0805, 0x0501, 0x0806, 0x0601, 0x0201`).
     - **Supported Versions (`0x002B`):** TLS 1.3 (`0x0304`), TLS 1.2 (`0x0303`).
     - **Key Share (`0x0033`):** x25519 (`0x001D`) + `uint16(32)` + 32 random public key bytes.
     - **ALPN (`0x0010`):** `0x02, 'h', '2', 0x08, 'h', 't', 't', 'p', '/', '1', '.', '1'`.

---

### 5.2 QUIC Initial Packet Layout (`gen_quic_initial`)

* **Target Length:** Random between 1200 and 1280 bytes.
* **Byte Layout:**
  1. Header Byte: `0xC0 | (rand(0..3) << 4) | (rand(0..3) << 2)` (Long header, Initial Type).
  2. Version: 4 bytes `0x00000001` (QUIC v1).
  3. DCID: 1 byte len (`0x08`) + 8 random bytes.
  4. SCID: 1 byte len (`0x08`) + 8 random bytes.
  5. Token: `0x00` (zero length).
  6. Length: 2 bytes big-endian length of remaining payload.
  7. Packet Number: 1-4 random bytes.
  8. Payload: Synthetic TLS 1.3 ClientHello (SNI from `QUIC_DOMAINS`).
  9. Padding: `0x00` bytes to target length.

---

### 5.3 DNS Query Layout (`gen_dns`) & SIP INVITE Layout (`gen_sip`)

- **DNS:** 2-byte Transaction ID + `0x0100` (Standard Query) + `0x0001` Question + QNAME labels (e.g. `\x06google\x03com\x00`) + Type A (`0x0001`) + Class IN (`0x0001`).
- **SIP:** Text ASCII `INVITE sip:user@<domain> SIP/2.0\r\nVia: ...\r\n...Content-Length: 0\r\n\r\n`.
- **Formatting:** Formatted as `<b 0x%x>` using hex encoding.

---

## 6. Noise Protocol IK Handshake Health Probes (`awg_health`)

AmneziaWG reachability probes perform a real `Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s` handshake over UDP.

### 6.1 Cryptographic Constants & Primitives

```
INITIAL_CHAIN_KEY = BLAKE2s-256("Noise_IKpsk2_25519_ChaChaPoly_BLAKE2s")   [32 bytes]
INITIAL_HASH      = BLAKE2s-256(INITIAL_CHAIN_KEY || "WireGuard v1 zx2c4 Jason@zx2c4.com") [32 bytes]
LABEL_MAC1        = "mac1----"                                             [8 bytes]
```

### 6.2 Key Derivation Functions (HMAC-BLAKE2s)

```go
func HMACBlake2s(key, data []byte) []byte {
	h, _ := blake2s.New256(key)
	h.Write(data)
	return h.Sum(nil)
}

func KDF1(key, data []byte) []byte {
	prk := HMACBlake2s(key, data)
	return HMACBlake2s(prk, []byte{0x01})
}

func KDF2(key, data []byte) (t1, t2 []byte) {
	prk := HMACBlake2s(key, data)
	t1 = HMACBlake2s(prk, []byte{0x01})
	t2 = HMACBlake2s(prk, append(t1, 0x02))
	return t1, t2
}

func KDF3(key, data []byte) (t1, t2, t3 []byte) {
	prk := HMACBlake2s(key, data)
	t1 = HMACBlake2s(prk, []byte{0x01})
	t2 = HMACBlake2s(prk, append(t1, 0x02))
	t3 = HMACBlake2s(prk, append(t2, 0x03))
	return t1, t2, t3
}
```

---

### 6.3 Handshake Initiation Packet Construction (Step-by-Step)

```
[Inputs: ServerPubKey (32B), ClientPrivKey (32B), PSK (32B), H1 (uint32), S1 (int)]

1. Initialize State:
   h  = BLAKE2s-256(INITIAL_HASH || ServerPubKey)
   ck = INITIAL_CHAIN_KEY

2. Generate Client Ephemeral Keypair:
   (client_e_priv, client_e_pub) = GenerateX25519()

3. Mix Ephemeral Public Key:
   h  = BLAKE2s-256(h || client_e_pub)
   ck = KDF1(ck, client_e_pub)

4. First DH Exchange (e_priv * ServerPubKey):
   ss1 = X25519(client_e_priv, ServerPubKey)
   ck, key1 = KDF2(ck, ss1)

5. Encrypt Client Static Public Key:
   encrypted_static = ChaCha20Poly1305_Encrypt(key1, nonce=0x00*12, plaintext=ClientPubKey, aad=h) [48 bytes]
   h = BLAKE2s-256(h || encrypted_static)

6. Second DH Exchange (ClientPrivKey * ServerPubKey):
   ss2 = X25519(ClientPrivKey, ServerPubKey)
   ck, key2 = KDF2(ck, ss2)

7. Generate & Encrypt TAI64N Timestamp:
   tai_sec  = 0x400000000000000A + unix_time_sec   [uint64 Big-Endian]
   tai_nsec = (unix_time_nsec) & ~0xFFFFFF         [uint32 Big-Endian]
   tai64n   = tai_sec (8B) || tai_nsec (4B)        [12 bytes]
   encrypted_timestamp = ChaCha20Poly1305_Encrypt(key2, nonce=0x00*12, plaintext=tai64n, aad=h) [28 bytes]
   h = BLAKE2s-256(h || encrypted_timestamp)

8. Assemble 116-Byte Message Body:
   msg_type_bytes   = uint32_to_le(H1)             [4 bytes]
   sender_idx_bytes = uint32_to_le(rand_idx)       [4 bytes]
   msg_body = msg_type_bytes || sender_idx_bytes || client_e_pub (32B) || encrypted_static (48B) || encrypted_timestamp (28B)

9. Compute MAC1 & MAC2:
   mac1_key = BLAKE2s-256(LABEL_MAC1 || ServerPubKey)
   MAC1 = BLAKE2s-128(key=mac1_key, data=msg_body) [16 bytes]
   MAC2 = 16 zero bytes                             [16 bytes]

10. Wire Packet Assembly:
    wire_packet = S1_random_junk_bytes (S1) || msg_body (116B) || MAC1 (16B) || MAC2 (16B)
    [Total length: S1 + 148 bytes]
```

---

### 6.4 Handshake Response Packet Verification (Step-by-Step)

```
[Inputs: WirePacket, State(h, ck, client_e_priv, ClientPrivKey, PSK, sender_idx), H2 (uint32), S2 (int)]

1. Unpack Wire Packet:
   Verify len(WirePacket) >= S2 + 92
   Skip S2 bytes: payload = WirePacket[S2:]

2. Verify Header & Index:
   msg_type = le_to_uint32(payload[0:4])
   Verify msg_type == H2 or msg_type == 2
   receiver_idx = le_to_uint32(payload[4:8])
   Verify receiver_idx == sender_idx

3. Extract Fields:
   server_e_pub    = payload[8:40]   [32 bytes]
   encrypted_empty = payload[40:56]  [16 bytes]
   MAC1            = payload[56:72]  [16 bytes]
   MAC2            = payload[72:88]  [16 bytes]

4. DH Exchanges & Final Key Derivation:
   h   = BLAKE2s-256(h || server_e_pub)
   ck  = KDF1(ck, server_e_pub)
   ss3 = X25519(client_e_priv, server_e_pub)
   ck  = KDF1(ck, ss3)
   ss4 = X25519(ClientPrivKey, server_e_pub)
   ck  = KDF1(ck, ss4)
   ck, tau, key3 = KDF3(ck, PSK)  [If no PSK, use 32 zero bytes]
   h   = BLAKE2s-256(h || tau)

5. Authenticate Response:
   decrypted = ChaCha20Poly1305_Decrypt(key3, nonce=0x00*12, ciphertext=encrypted_empty, aad=h)
   Verify decrypted == []byte{} (Authentication tag valid)
```

---

## 7. RemnaWave REST API Client Contract (`internal/service/remnawave`)

- **Endpoint:** `GET {remnawave_url}/api/users`
- **Headers:** `Authorization: Bearer {remnawave_api_key}`, `Accept: application/json`
- **Sync Logic:**
  - Update local accounts where `remnawave_uuid` matches.
  - Create local accounts if `remnawave_sync_users=true`.
  - Provision server connections if `remnawave_create_conns=true` and `remnawave_server_id > 0`.
