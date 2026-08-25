# Configuration Specification (`02-configuration.md`)

> **Target Packages:** `internal/config`, `internal/router/settings`, `internal/security`  
> **Source Python Files:** `app/core/config.py`, `app/core/database.py` (settings methods), `app/routers/settings.py`  
> **Status:** Ground Truth Specification for Go Rewrite

---

## 1. Static Configuration (Environment Variables & Flags)

### 1.1 Environment Variable Reference

| Variable | Type | Default | Required | Purpose |
|----------|------|---------|----------|---------|
| `SECRET_KEY` | `string` (hex) | *(see resolution)* | Recommended in prod | Encryption key for credentials, Fernet tokens, and session signing |
| `DATA_DIR` | `string` (path) | `./data` or app root | No | Directory for database (`panel.db`), keyfile (`.secret_key`), and exports |
| `PORT` / `PANEL_PORT` | `int` | `5000` | No | HTTP server listening port |
| `HOST` | `string` | `0.0.0.0` | No | HTTP server listening address |
| `TRUSTED_PROXIES` | `string` (comma CSV) | `""` (empty; `172.18.0.0/24` in compose) | No | Comma-separated IPs / CIDRs allowed to set `X-Forwarded-For` / `X-Real-IP`. If empty, `X-Forwarded-For` is NOT trusted |
| `LOG_LEVEL` | `string` | `INFO` | No | Logging verbosity (`DEBUG`, `INFO`, `WARN`, `ERROR`) |
| `VPN_ENABLED` | `bool` | `false` | No | Feature flag to enable in-process AWG VPN endpoint & load balancer (requires TUN + `CAP_NET_ADMIN`). When `false`, panel runs in lightweight management-only mode |
| `VPN_LISTEN_PORT` | `int` | `51820` | No | UDP port for incoming client AmneziaWG connections (when `VPN_ENABLED=true`) |
| `VPN_SUBNET` | `string` (CIDR) | `10.100.0.0/16` | No | Internal IP allocation pool for connected VPN peers (when `VPN_ENABLED=true`) |

---

### 1.2 `SECRET_KEY` Resolution Algorithm

The application requires a 32-byte secret key for HMAC and Fernet-compatible AES credential encryption. It MUST resolve the key following this strict precedence:

```
[Start]
   │
   ├─► 1. Check `os.Getenv("SECRET_KEY")`
   │      ├─► Non-empty? ──► Use environment key (Log INFO) ──► [Done]
   │      └─► Empty? ──────┐
   │                       ▼
   ├─► 2. Check file `<DATA_DIR>/.secret_key`
   │      ├─► Exists & readable? ──► Read key string (Log INFO) ──► [Done]
   │      └─► Missing / unreadable? ──┐
   │                                  ▼
   └─► 3. Generate New Key
          ├─► Generate 32 cryptographically secure random bytes (`crypto/rand`)
          ├─► Encode as 64-character lowercase hex string (`hex.EncodeToString`)
          ├─► Write to `<DATA_DIR>/.secret_key` with strict permissions `0600`
          ├─► Log WARN: "Generated new SECRET_KEY on first boot..."
          └─► [Done]
```

---

## 2. Dynamic Configuration (`settings` SQLite Table)

The `settings` table uses a simple key-value model (`key TEXT PRIMARY KEY, value TEXT`) where values are JSON-serialized strings.

### 2.1 Complete Settings Registry

| Key | Go Struct / Type | Default Value (JSON) | Encryption |
|-----|------------------|----------------------|------------|
| `schema_version` | `string` (int) | `"1"` | Plaintext |
| `appearance` | `models.AppearanceSettings` | `{"title":"Amnezia","logo":"🛡","subtitle":"Web Panel","language":"en"}` | Plaintext |
| `sync` | `models.SyncSettings` | `{"remnawave_url":"","remnawave_api_key":"","remnawave_sync":false,"remnawave_sync_users":false,"remnawave_create_conns":false,"remnawave_server_id":0,"remnawave_protocol":"awg"}` | Plaintext |
| `captcha` | `models.CaptchaSettings` | `{"enabled":false}` | Plaintext |
| `telegram` | `map[string]interface{}` | `{}` | Plaintext |
| `ssl` | `models.SSLSettings` | `{"enabled":false,"domain":"","cert_path":"","key_path":"","cert_text":"","key_text":"","panel_port":5000}` | `cert_text` & `key_text` **ENCRYPTED** |
| `limits` | `models.ConnectionLimits` | `{"max_connections_per_user":10,"connection_rate_limit_count":5,"connection_rate_limit_window":60}` | Plaintext |
| `vpn_config` | `models.VPNConfig` | `{"algorithm":"least_conn","weights":{},"health_threshold_ms":2000,"listen_port":51820,"subnet_cidr":"10.100.0.0/16","max_total_peers":1000,"max_peers_per_backend":200}` | Plaintext |

---

### 2.2 SSL Certificate & Key Encryption Rules

When saving or retrieving the `ssl` settings object:
1. **On Save / Update (`update_setting("ssl", ...)`):**
   - If `cert_text` is non-empty and does NOT already start with the Fernet prefix `gAAAAA`, encrypt it with `security.EncryptCredential(cert_text)`.
   - If `key_text` is non-empty and does NOT already start with `gAAAAA`, encrypt it with `security.EncryptCredential(key_text)`.
   - Serialize the entire struct to JSON and store in the `settings` table.
2. **On Retrieval (`get_setting("ssl")`):**
   - Unmarshal the JSON value.
   - If `cert_text` is encrypted (starts with `gAAAAA`), decrypt it transparently with `security.DecryptCredentialSafe(cert_text)`.
   - If `key_text` is encrypted (starts with `gAAAAA`), decrypt it transparently with `security.DecryptCredentialSafe(key_text)`.
   - Return the decrypted in-memory struct to the caller.
3. **API Stripping:**
   - When serving `GET /api/settings` to frontend clients, `key_text` and `cert_text` MUST be stripped or replaced with boolean flags (`has_cert: true`, `has_key: true`) to prevent secret leakage in browser network tabs.

---

### 2.3 Default Settings Seeding (`EnsureDefaultSettings`)

On database initialization, the engine must iterate through default settings keys and insert them if missing (`INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)`).

```go
var DefaultSettings = map[string]string{
	"appearance": `{"title":"Amnezia","logo":"🛡","subtitle":"Web Panel","language":"en"}`,
	"sync":       `{"remnawave_url":"","remnawave_api_key":"","remnawave_sync":false,"remnawave_sync_users":false,"remnawave_create_conns":false,"remnawave_server_id":0,"remnawave_protocol":"awg"}`,
	"captcha":    `{"enabled":false}`,
	"telegram":   `{}`,
	"ssl":        `{"enabled":false,"domain":"","cert_path":"","key_path":"","cert_text":"","key_text":"","panel_port":5000}`,
	"limits":     `{"max_connections_per_user":10,"connection_rate_limit_count":5,"connection_rate_limit_window":60}`,
	"vpn_config": `{"algorithm":"least_conn","weights":{},"health_threshold_ms":2000,"listen_port":51820,"subnet_cidr":"10.100.0.0/16","max_total_peers":1000,"max_peers_per_backend":200}`,
}
```

---

## 3. Embedded Translations & Internationalization

### 3.1 Supported Languages

1. `en` — English (Default fallback)
2. `fa` — Persian / Farsi
3. `fr` — French
4. `ru` — Russian
5. `zh` — Chinese (Simplified)

### 3.2 Loading & Resolution

```go
package config

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed translations/*.json
var translationFS embed.FS

var (
	translationsMu sync.RWMutex
	translations   = make(map[string]map[string]string)
)

func LoadTranslations() error {
	translationsMu.Lock()
	defer translationsMu.Unlock()

	entries, err := translationFS.ReadDir("translations")
	if err != nil {
		return fmt.Errorf("failed to read embedded translations: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			lang := strings.TrimSuffix(entry.Name(), ".json")
			data, err := translationFS.ReadFile("translations/" + entry.Name())
			if err != nil {
				return fmt.Errorf("failed to read %s: %w", entry.Name(), err)
			}
			var dict map[string]string
			if err := json.Unmarshal(data, &dict); err != nil {
				return fmt.Errorf("failed to parse %s: %w", entry.Name(), err)
			}
			translations[lang] = dict
		}
	}
	return nil
}

// T translates a key for the given language with fallback to English.
func T(lang, key string) string {
	translationsMu.RLock()
	defer translationsMu.RUnlock()

	if dict, ok := translations[lang]; ok {
		if val, exists := dict[key]; exists && val != "" {
			return val
		}
	}
	// Fallback to English
	if dict, ok := translations["en"]; ok {
		if val, exists := dict[key]; exists && val != "" {
			return val
		}
	}
	return key
}
```

---

## 4. Path Management & Config Loader

```go
package config

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Paths struct {
	DataDir   string
	DBPath    string
	SecretKey string
}

type AppConfig struct {
	Paths          *Paths
	Host           string
	Port           int
	LogLevel       string
	TrustedProxies []string
	TrustedCIDRs   []*net.IPNet
	TrustedIPs     []net.IP
	VPNEnabled     bool
	VPNListenPort  int
	VPNSubnet      string
	SecretKey      string
}

func ResolvePaths() *Paths {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	_ = os.MkdirAll(dataDir, 0755)

	return &Paths{
		DataDir:   dataDir,
		DBPath:    filepath.Join(dataDir, "panel.db"),
		SecretKey: filepath.Join(dataDir, ".secret_key"),
	}
}

func LoadConfig() (*AppConfig, error) {
	paths := ResolvePaths()
	secretKey := ResolveSecretKey(paths.DataDir)

	port := 5000
	if p := os.Getenv("PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	} else if p := os.Getenv("PANEL_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}

	vpnPort := 51820
	if vp := os.Getenv("VPN_LISTEN_PORT"); vp != "" {
		if v, err := strconv.Atoi(vp); err == nil {
			vpnPort = v
		}
	}

	vpnEnabled := os.Getenv("VPN_ENABLED") == "true" || os.Getenv("VPN_ENABLED") == "1"
	vpnSubnet := os.Getenv("VPN_SUBNET")
	if vpnSubnet == "" {
		vpnSubnet = "10.100.0.0/16"
	}

	rawProxies := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES"))
	var trustedProxies []string
	var trustedCIDRs []*net.IPNet
	var trustedIPs []net.IP

	if rawProxies != "" {
		for _, entry := range strings.Split(rawProxies, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			trustedProxies = append(trustedProxies, entry)
			if strings.Contains(entry, "/") {
				if _, ipNet, err := net.ParseCIDR(entry); err == nil {
					trustedCIDRs = append(trustedCIDRs, ipNet)
				}
			} else {
				if ip := net.ParseIP(entry); ip != nil {
					trustedIPs = append(trustedIPs, ip)
				}
			}
		}
	}

	host := os.Getenv("HOST")
	if host == "" {
		host = "0.0.0.0"
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "INFO"
	}

	return &AppConfig{
		Paths:          paths,
		Host:           host,
		Port:           port,
		LogLevel:       logLevel,
		TrustedProxies: trustedProxies,
		TrustedCIDRs:   trustedCIDRs,
		TrustedIPs:     trustedIPs,
		VPNEnabled:     vpnEnabled,
		VPNListenPort:  vpnPort,
		VPNSubnet:      vpnSubnet,
		SecretKey:      secretKey,
	}, nil
}
```
