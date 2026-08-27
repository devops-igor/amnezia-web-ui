# Domain Models Specification (`01-domain-model.md`)

> **Target Packages:** `internal/models`, `internal/router/*`  
> **Source Python File:** `app/models/schemas.py`  
> **Status:** Ground Truth Specification for Go Rewrite

---

## 1. Constants & Enums

### 1.1 Protocol Constants & Normalization

```go
package models

// Valid protocols supported by the panel
var ValidProtocols = map[string]bool{
	"awg":    true,
	"telemt": true,
	"dns":    true,
}

// Legacy protocol aliases that must be normalized to modern names
var ProtocolAliases = map[string]string{
	"awg2":       "awg",
	"awg_legacy": "awg",
}

// NormalizeProtocol normalizes legacy protocol names (awg2, awg_legacy -> awg).
func NormalizeProtocol(proto string) string {
	if mapped, ok := ProtocolAliases[proto]; ok {
		return mapped
	}
	return proto
}

// IsValidProtocol checks if a protocol is supported.
func IsValidProtocol(proto string) bool {
	return ValidProtocols[NormalizeProtocol(proto)]
}
```

### 1.2 Enums

```go
// AWGObfuscationProfile determines AWG junk packet ranges and overhead.
type AWGObfuscationProfile string

const (
	AWGProfileLite     AWGObfuscationProfile = "lite"     // Jc: 3-5
	AWGProfileStandard AWGObfuscationProfile = "standard" // Jc: 5-8 (default)
	AWGProfilePro      AWGObfuscationProfile = "pro"      // Jc: 4-16
)

// AWGMimicryProfile determines DPI mimicry protocol signature.
type AWGMimicryProfile string

const (
	AWGMimicryAuto AWGMimicryProfile = "auto"
	AWGMimicryTLS  AWGMimicryProfile = "tls"
	AWGMimicryDNS  AWGMimicryProfile = "dns"
	AWGMimicrySIP  AWGMimicryProfile = "sip"
	AWGMimicryQUIC AWGMimicryProfile = "quic"
)

// UserRole defines user authorization levels.
type UserRole string

const (
	RoleAdmin UserRole = "admin"
	RoleUser  UserRole = "user"
)

// TrafficResetStrategy defines counter reset policy.
type TrafficResetStrategy string

const (
	ResetStrategyNever   TrafficResetStrategy = "never"   // Default: counters accumulate monotonically
	ResetStrategyMonthly TrafficResetStrategy = "monthly" // Active in runtime: resets monthly_rx/tx and traffic_used on 1st of month
	ResetStrategyDaily   TrafficResetStrategy = "daily"   // Allowed in schema/API for compatibility, but NOT actively scheduled in runtime background jobs
)

// ReachabilityStatus defines server probe states.
type ReachabilityStatus string

const (
	ReachabilityOnline  ReachabilityStatus = "online"
	ReachabilityOffline ReachabilityStatus = "offline"
	ReachabilityUnknown ReachabilityStatus = "unknown"
)

// LoadBalancingAlgorithm defines VPN routing algorithm.
type LoadBalancingAlgorithm string

const (
	LBLeastConnections LoadBalancingAlgorithm = "least_conn"
	LBWeighted         LoadBalancingAlgorithm = "weighted"
	LBRoundRobin       LoadBalancingAlgorithm = "round_robin"
)
```

---

## 2. Authentication Models

### 2.1 `LoginRequest`
* **Python Source:** `LoginRequest`
* **Constraints:**
  - `Username`: `1 <= len <= 255`
  - `Password`: `1 <= len <= 4096`
  - `Captcha`: Optional string, `len <= 4096`

```go
type LoginRequest struct {
	Username string  `json:"username" validate:"required,min=1,max=255"`
	Password string  `json:"password" validate:"required,min=1,max=4096"`
	Captcha  *string `json:"captcha,omitempty" validate:"omitempty,max=4096"`
}
```

### 2.2 `SetupRequest`
* **Python Source:** `SetupRequest`
* **Constraints:**
  - `Username`: `3 <= len <= 32`, regex `^[a-zA-Z0-9_]+$`
  - `Password`: `8 <= len <= 4096`
  - `ConfirmPassword`: `1 <= len <= 4096`, must match `Password`

```go
type SetupRequest struct {
	Username        string `json:"username" validate:"required,min=3,max=32,alphanum_underscore"`
	Password        string `json:"password" validate:"required,min=8,max=4096"`
	ConfirmPassword string `json:"confirm_password" validate:"required,min=1,max=4096"`
}

func (r *SetupRequest) Validate() error {
	if r.Password != r.ConfirmPassword {
		return errors.New("passwords do not match")
	}
	return validatePasswordComplexity(r.Password)
}
```

### 2.3 `ChangePasswordRequest`
* **Python Source:** `ChangePasswordRequest`
* **Constraints:**
  - `CurrentPassword`: `1 <= len <= 4096`
  - `NewPassword`: `8 <= len <= 4096`, no null bytes, ≥1 uppercase, ≥1 lowercase, ≥1 digit
  - `ConfirmPassword`: `1 <= len <= 4096`, must match `NewPassword`

```go
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" validate:"required,min=1,max=4096"`
	NewPassword     string `json:"new_password" validate:"required,min=8,max=4096"`
	ConfirmPassword string `json:"confirm_password" validate:"required,min=1,max=4096"`
}

func (r *ChangePasswordRequest) Validate() error {
	if strings.Contains(r.NewPassword, "\x00") {
		return errors.New("password must not contain null bytes")
	}
	if r.NewPassword != r.ConfirmPassword {
		return errors.New("passwords do not match")
	}
	return validatePasswordComplexity(r.NewPassword)
}
```

---

## 3. Server Models

### 3.1 `AddServerRequest`
* **Python Source:** `AddServerRequest`
* **Validation Rules:**
  - `Host`: Optional/Required, `len <= 255`, valid IPv4 or RFC 1123 hostname.
  - `SSHPort`: `1 <= port <= 65535`, default `22`.
  - `Username`: `len <= 255`.
  - `Password`: `len <= 4096`.
  - `PrivateKey`: `len <= 16384`.
  - `Name`: `len <= 255`.

```go
type AddServerRequest struct {
	Host       string `json:"host" validate:"omitempty,max=255"`
	SSHPort    int    `json:"ssh_port" validate:"gte=1,lte=65535"`
	Username   string `json:"username" validate:"max=255"`
	Password   string `json:"password" validate:"max=4096"`
	PrivateKey string `json:"private_key" validate:"max=16384"`
	Name       string `json:"name" validate:"max=255"`
}

func (r *AddServerRequest) Validate() error {
	if r.SSHPort == 0 {
		r.SSHPort = 22
	}
	if r.Host != "" {
		return validateHost(r.Host)
	}
	return nil
}
```

### 3.2 `ConfirmFingerprintRequest`
* **Python Source:** `ConfirmFingerprintRequest`
* **Validation Rules:**
  - Includes all fields from `AddServerRequest` plus `Fingerprint` (`1 <= len <= 256`) and `ServerInfo` (`len <= 16384`).

```go
type ConfirmFingerprintRequest struct {
	Host        string `json:"host" validate:"omitempty,max=255"`
	SSHPort     int    `json:"ssh_port" validate:"gte=1,lte=65535"`
	Username    string `json:"username" validate:"max=255"`
	Password    string `json:"password" validate:"max=4096"`
	PrivateKey  string `json:"private_key" validate:"max=16384"`
	Name        string `json:"name" validate:"max=255"`
	ServerInfo  string `json:"server_info" validate:"max=16384"`
	Fingerprint string `json:"fingerprint" validate:"required,min=1,max=256"`
}

func (r *ConfirmFingerprintRequest) Validate() error {
	if r.Host != "" {
		return validateHost(r.Host)
	}
	return nil
}
```

### 3.3 `InstallProtocolRequest`
* **Python Source:** `InstallProtocolRequest`
* **Validation Rules:**
  - `Protocol`: `awg`, `telemt`, `dns` (default: `awg`).
  - `Port`: String `1 <= len <= 10` (default: `55424`), must parse to valid port number `1-65535`.
  - `TLSEmulation`: Optional bool.
  - `TLSDomain`: Optional string `1 <= len <= 128`, regex `^[a-zA-Z0-9]([a-zA-Z0-9._-]{0,126}[a-zA-Z0-9])?$|^[a-zA-Z0-9]$`.
  - `MaxConnections`: Optional int `1 <= val <= 100000`.
  - `AWGProfile`: Optional `AWGObfuscationProfile` (`lite`, `standard`, `pro`).
  - `AWGCPSProtocol`: Optional string, regex `^(quic|dns|sip)$`.

```go
type InstallProtocolRequest struct {
	Protocol       string                 `json:"protocol" validate:"required,oneof=awg telemt dns"`
	Port           string                 `json:"port" validate:"required,min=1,max=10"`
	TLSEmulation   *bool                  `json:"tls_emulation,omitempty"`
	TLSDomain      *string                `json:"tls_domain,omitempty" validate:"omitempty,max=128"`
	MaxConnections *int                   `json:"max_connections,omitempty" validate:"omitempty,gte=1,lte=100000"`
	AWGProfile     *AWGObfuscationProfile `json:"awg_profile,omitempty" validate:"omitempty,oneof=lite standard pro"`
	AWGCPSProtocol *string                `json:"awg_cps_protocol,omitempty" validate:"omitempty,oneof=quic dns sip"`
}

func (r *InstallProtocolRequest) Validate() error {
	r.Protocol = NormalizeProtocol(r.Protocol)
	if !IsValidProtocol(r.Protocol) {
		return fmt.Errorf("invalid protocol: %s", r.Protocol)
	}
	portNum, err := strconv.Atoi(r.Port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return errors.New("port must be an integer between 1 and 65535")
	}
	if r.TLSDomain != nil && *r.TLSDomain != "" {
		if err := validateTLSDomain(*r.TLSDomain); err != nil {
			return err
		}
	}
	return nil
}
```

### 3.4 `ProtocolRequest` & `ServerConfigSaveRequest`

```go
type ProtocolRequest struct {
	Protocol string `json:"protocol" validate:"required"`
}

func (r *ProtocolRequest) Validate() error {
	r.Protocol = NormalizeProtocol(r.Protocol)
	if !IsValidProtocol(r.Protocol) {
		return fmt.Errorf("invalid protocol: %s", r.Protocol)
	}
	return nil
}

type ServerConfigSaveRequest struct {
	Protocol string `json:"protocol" validate:"required"`
	Config   string `json:"config" validate:"required,min=1,max=65536"`
}

func (r *ServerConfigSaveRequest) Validate() error {
	r.Protocol = NormalizeProtocol(r.Protocol)
	if !IsValidProtocol(r.Protocol) {
		return fmt.Errorf("invalid protocol: %s", r.Protocol)
	}
	return nil
}
```

---

## 4. Connection Models

### 4.1 `AddConnectionRequest` / `MyAddConnectionRequest`
* **Python Source:** `AddConnectionRequest`, `MyAddConnectionRequest`, `AddUserConnectionRequest`

```go
type AddConnectionRequest struct {
	Protocol          string  `json:"protocol" validate:"required"`
	Name              string  `json:"name" validate:"required,min=1,max=255"`
	UserID            *string `json:"user_id,omitempty" validate:"omitempty,max=255"`
	TelemtQuota       *string `json:"telemt_quota,omitempty" validate:"omitempty,max=50"`
	TelemtMaxIPs      *int    `json:"telemt_max_ips,omitempty" validate:"omitempty,gte=1,lte=1000000"`
	TelemtExpiry      *string `json:"telemt_expiry,omitempty" validate:"omitempty,max=50"`
	AWGSpeedLimitDown *int    `json:"awg_speed_limit_down,omitempty" validate:"omitempty,gte=0"`
	AWGSpeedLimitUp   *int    `json:"awg_speed_limit_up,omitempty" validate:"omitempty,gte=0"`
	AWGMimicry        *string `json:"awg_mimicry,omitempty" validate:"omitempty,oneof=auto tls dns sip quic"`
}

type MyAddConnectionRequest struct {
	ServerID          int     `json:"server_id" validate:"required,gte=1"`
	Protocol          string  `json:"protocol" validate:"required"`
	Name              string  `json:"name" validate:"required,min=1,max=255"`
	TelemtQuota       *string `json:"telemt_quota,omitempty" validate:"omitempty,max=50"`
	TelemtMaxIPs      *int    `json:"telemt_max_ips,omitempty" validate:"omitempty,gte=1,lte=1000000"`
	TelemtExpiry      *string `json:"telemt_expiry,omitempty" validate:"omitempty,max=50"`
	AWGSpeedLimitDown *int    `json:"awg_speed_limit_down,omitempty" validate:"omitempty,gte=0"`
	AWGSpeedLimitUp   *int    `json:"awg_speed_limit_up,omitempty" validate:"omitempty,gte=0"`
	AWGMimicry        *string `json:"awg_mimicry,omitempty" validate:"omitempty,oneof=auto tls dns sip quic"`
}

type AddUserConnectionRequest struct {
	ServerID     int     `json:"server_id" validate:"required,gte=1"`
	Protocol     string  `json:"protocol" validate:"required"`
	Name         string  `json:"name" validate:"required,min=1,max=255"`
	ClientID     *string `json:"client_id,omitempty" validate:"omitempty,max=255"`
	TelemtQuota  *string `json:"telemt_quota,omitempty" validate:"omitempty,max=50"`
	TelemtMaxIPs *int    `json:"telemt_max_ips,omitempty" validate:"omitempty,gte=1,lte=1000000"`
	TelemtExpiry *string `json:"telemt_expiry,omitempty" validate:"omitempty,max=50"`
	AWGMimicry   *string `json:"awg_mimicry,omitempty" validate:"omitempty,oneof=auto tls dns sip quic"`
}
```

### 4.2 `EditConnectionRequest` & `RenameConnectionRequest`

```go
type EditConnectionRequest struct {
	Protocol          string  `json:"protocol" validate:"required"`
	ClientID          string  `json:"client_id" validate:"max=255"`
	Name              *string `json:"name,omitempty" validate:"omitempty,max=255"`
	UserID            *string `json:"user_id,omitempty" validate:"omitempty,max=100"`
	TelemtQuota       *string `json:"telemt_quota,omitempty" validate:"omitempty,max=50"`
	TelemtMaxIPs      *int    `json:"telemt_max_ips,omitempty" validate:"omitempty,gte=1,lte=1000000"`
	TelemtExpiry      *string `json:"telemt_expiry,omitempty" validate:"omitempty,max=50"`
	AWGSpeedLimitDown *int    `json:"awg_speed_limit_down,omitempty" validate:"omitempty,gte=0"`
	AWGSpeedLimitUp   *int    `json:"awg_speed_limit_up,omitempty" validate:"omitempty,gte=0"`
	AWGMimicry        *string `json:"awg_mimicry,omitempty" validate:"omitempty,oneof=auto tls dns sip quic"`
}

type RenameConnectionRequest struct {
	Name string `json:"name" validate:"required,min=1,max=255"`
}

func (r *RenameConnectionRequest) Validate() error {
	r.Name = strings.TrimSpace(r.Name)
	if strings.Contains(r.Name, "\x00") {
		return errors.New("name cannot contain null bytes")
	}
	if r.Name == "" {
		return errors.New("name cannot be empty or whitespace only")
	}
	return nil
}
```

### 4.3 `SpeedLimitRequest` & `AwgSpeedLimitConfigRequest`

```go
type SpeedLimitRequest struct {
	ClientID       string `json:"client_id" validate:"required"`
	SpeedLimitDown *int   `json:"speed_limit_down,omitempty" validate:"omitempty,gte=0"`
	SpeedLimitUp   *int   `json:"speed_limit_up,omitempty" validate:"omitempty,gte=0"`
}

type AwgSpeedLimitConfigRequest struct {
	GlobalSpeedLimitDown  *int `json:"global_speed_limit_down,omitempty" validate:"omitempty,gte=0"`
	GlobalSpeedLimitUp    *int `json:"global_speed_limit_up,omitempty" validate:"omitempty,gte=0"`
	DefaultSpeedLimitDown *int `json:"default_speed_limit_down,omitempty" validate:"omitempty,gte=0"`
	DefaultSpeedLimitUp   *int `json:"default_speed_limit_up,omitempty" validate:"omitempty,gte=0"`
}
```

---

## 5. User Models

### 5.1 `AddUserRequest`
* **Python Source:** `AddUserRequest`
* **Validation Rules:**
  - `Username`: `3 <= len <= 255`, lowercase, regex `^[a-z0-9_-]+$`.
  - `Password`: `8 <= len <= 4096`, ≥1 uppercase, ≥1 lowercase, ≥1 digit.
  - `Role`: `admin` or `user` (default: `user`).
  - `TrafficLimit`: Float/Int `ge=0` (in GB or bytes depending on UI, 0 = unlimited).
  - `TrafficResetStrategy`: `never`, `monthly`, `daily` (default: `never`).
  - Optional connection creation params (`ServerID`, `Protocol`, `ConnectionName`, `ExpirationDate`, `ExpiresAt`, `AWGMimicry`).

```go
type AddUserRequest struct {
	Username             string   `json:"username" validate:"required,min=3,max=255"`
	Password             string   `json:"password" validate:"required,min=8,max=4096"`
	Role                 UserRole `json:"role" validate:"required,oneof=admin user"`
	TelegramID           *string  `json:"telegramId,omitempty" validate:"omitempty,max=255"`
	Email                *string  `json:"email,omitempty" validate:"omitempty,max=255"`
	Description          *string  `json:"description,omitempty" validate:"omitempty,max=1000"`
	TrafficLimit         float64  `json:"traffic_limit" validate:"gte=0"`
	TrafficResetStrategy string   `json:"traffic_reset_strategy" validate:"omitempty,oneof=never monthly daily"`
	ServerID             *int     `json:"server_id,omitempty" validate:"omitempty,gte=1"`
	Protocol             *string  `json:"protocol,omitempty" validate:"omitempty,max=50"`
	ConnectionName       *string  `json:"connection_name,omitempty" validate:"omitempty,max=255"`
	ExpirationDate       *string  `json:"expiration_date,omitempty" validate:"omitempty,max=50"`
	ExpiresAt            *string  `json:"expires_at,omitempty" validate:"omitempty,max=50"`
	AWGMimicry           *string  `json:"awg_mimicry,omitempty" validate:"omitempty,oneof=auto tls dns sip quic"`
}

func (r *AddUserRequest) Validate() error {
	r.Username = strings.ToLower(strings.TrimSpace(r.Username))
	if !usernameRegex.MatchString(r.Username) {
		return errors.New("username must contain only lowercase letters, digits, hyphens, and underscores")
	}
	if err := validatePasswordComplexity(r.Password); err != nil {
		return err
	}
	if r.Protocol != nil && *r.Protocol != "" {
		*r.Protocol = NormalizeProtocol(*r.Protocol)
		if !IsValidProtocol(*r.Protocol) {
			return fmt.Errorf("invalid protocol: %s", *r.Protocol)
		}
	}
	return nil
}
```

### 5.2 `UpdateUserRequest` & `ToggleUserRequest`

```go
type UpdateUserRequest struct {
	TelegramID           *string  `json:"telegramId,omitempty" validate:"omitempty,max=255"`
	Email                *string  `json:"email,omitempty" validate:"omitempty,max=255"`
	Description          *string  `json:"description,omitempty" validate:"omitempty,max=1000"`
	TrafficLimit         *float64 `json:"traffic_limit,omitempty" validate:"omitempty,gte=0"`
	TrafficResetStrategy *string  `json:"traffic_reset_strategy,omitempty" validate:"omitempty,max=50"`
	ExpirationDate       *string  `json:"expiration_date,omitempty" validate:"omitempty,max=50"`
	ExpiresAt            *string  `json:"expires_at,omitempty" validate:"omitempty,max=50"`
	AWGMimicry           *string  `json:"awg_mimicry,omitempty" validate:"omitempty,oneof=auto tls dns sip quic"`
	Password             *string  `json:"password,omitempty" validate:"omitempty,min=8,max=4096"`
}

func (r *UpdateUserRequest) Validate() error {
	if r.Password != nil && *r.Password != "" {
		return validatePasswordComplexity(*r.Password)
	}
	return nil
}

type ToggleUserRequest struct {
	Enabled bool `json:"enabled"`
}
```

---

## 6. Settings Models

```go
type AppearanceSettings struct {
	Title    string `json:"title" validate:"required,min=1,max=100"`
	Logo     string `json:"logo" validate:"required,min=1,max=100"`
	Subtitle string `json:"subtitle" validate:"required,min=1,max=200"`
	Language string `json:"language" validate:"required,min=1,max=10"`
}

type SyncSettings struct {
	RemnawaveURL         string `json:"remnawave_url" validate:"max=2048"`
	RemnawaveAPIKey      string `json:"remnawave_api_key" validate:"max=512"`
	RemnawaveSync        bool   `json:"remnawave_sync"`
	RemnawaveSyncUsers   bool   `json:"remnawave_sync_users"`
	RemnawaveCreateConns bool   `json:"remnawave_create_conns"`
	RemnawaveServerID    int    `json:"remnawave_server_id" validate:"gte=0"`
	RemnawaveProtocol    string `json:"remnawave_protocol" validate:"required,max=50"`
}

type CaptchaSettings struct {
	Enabled bool `json:"enabled"`
}

type SSLSettings struct {
	Enabled   bool   `json:"enabled"`
	Domain    string `json:"domain" validate:"max=255"`
	CertPath  string `json:"cert_path" validate:"max=4096"`
	KeyPath   string `json:"key_path" validate:"max=4096"`
	CertText  string `json:"cert_text" validate:"max=65536"`
	KeyText   string `json:"key_text" validate:"max=65536"`
	PanelPort int    `json:"panel_port" validate:"gte=1,lte=65535"`
}

type ConnectionLimits struct {
	MaxConnectionsPerUser     int `json:"max_connections_per_user" validate:"gte=1,lte=1000"`
	ConnectionRateLimitCount  int `json:"connection_rate_limit_count" validate:"gte=1,lte=1000"`
	ConnectionRateLimitWindow int `json:"connection_rate_limit_window" validate:"gte=1,lte=86400"`
}

type SaveSettingsRequest struct {
	Appearance AppearanceSettings     `json:"appearance"`
	Sync       SyncSettings           `json:"sync"`
	Captcha    CaptchaSettings        `json:"captcha"`
	Telegram   map[string]interface{} `json:"telegram,omitempty"`
	SSL        SSLSettings            `json:"ssl"`
	Limits     ConnectionLimits       `json:"limits"`
}
```

---

## 7. Response DTO Models

### 7.1 Server Responses

```go
type ServerItemResponse struct {
	ID         int                    `json:"id"`
	Name       string                 `json:"name"`
	Host       string                 `json:"host"`
	SSHPort    int                    `json:"ssh_port"`
	Username   string                 `json:"username"`
	ServerInfo string                 `json:"server_info"`
	Protocols  map[string]interface{} `json:"protocols"`
}

type ServerStatsResponse struct {
	CPU         float64 `json:"cpu"`
	RAMUsed     int64   `json:"ram_used"`
	RAMTotal    int64   `json:"ram_total"`
	RAMPercent  float64 `json:"ram_percent"`
	DiskUsed    int64   `json:"disk_used"`
	DiskTotal   int64   `json:"disk_total"`
	DiskPercent float64 `json:"disk_percent"`
	NetRx       int64   `json:"net_rx"`
	NetTx       int64   `json:"net_tx"`
	Uptime      string  `json:"uptime"`
}

type ServerCheckResponse struct {
	Connection      string                 `json:"connection"` // "ok" or error string
	DockerInstalled bool                   `json:"docker_installed"`
	Protocols       map[string]interface{} `json:"protocols"`
}
```

### 7.2 User & Leaderboard Responses

```go
type UserItemResponse struct {
	ID                   string  `json:"id"`
	Username             string  `json:"username"`
	Role                 string  `json:"role"`
	Enabled              bool    `json:"enabled"`
	CreatedAt            string  `json:"created_at"`
	TelegramID           *string `json:"telegramId"`
	Email                *string `json:"email"`
	Description          *string `json:"description"`
	ConnectionsCount     int     `json:"connections_count"`
	TrafficUsed          int64   `json:"traffic_used"`
	TrafficTotal         int64   `json:"traffic_total"`
	TrafficLimit         int64   `json:"traffic_limit"`
	TrafficResetStrategy string  `json:"traffic_reset_strategy"`
	LastResetAt          *string `json:"last_reset_at"`
	ExpirationDate       *string `json:"expiration_date"`
	ExpiresAt            *string `json:"expires_at"`
	AWGMimicry           *string `json:"awg_mimicry"`
	ShareEnabled         bool    `json:"share_enabled"`
	ShareToken           *string `json:"share_token"`
	HasSharePassword     bool    `json:"has_share_password"`
	Source               string  `json:"source"` // "Local" or "RemnaWave"
}

type PaginatedUsersResponse struct {
	Users []UserItemResponse `json:"users"`
	Total int                `json:"total"`
	Page  int                `json:"page"`
	Size  int                `json:"size"`
	Pages int                `json:"pages"`
}

type LeaderboardEntryResponse struct {
	Rank     int    `json:"rank"`
	Username string `json:"username"`
	Download int64  `json:"download"`
	Upload   int64  `json:"upload"`
	Total    int64  `json:"total"`
}

type LeaderboardResponse struct {
	Period          string                     `json:"period"`
	Entries         []LeaderboardEntryResponse `json:"entries"`
	CurrentUserRank *int                       `json:"current_user_rank,omitempty"`
	MonthlyLabel    *string                    `json:"monthly_label,omitempty"`
}
```

---

## 8. New VPN Subsystem Models (Phase 4E & 5.8)

```go
type BackendTunnel struct {
	ID                int       `json:"id" db:"id"`
	ServerID          int       `json:"server_id" db:"server_id"`
	InterfaceName     string    `json:"interface_name" db:"interface_name"`
	PublicKey         string    `json:"public_key" db:"public_key"`
	PrivateKey        string    `json:"-" db:"private_key"` // Encrypted at rest
	Endpoint          string    `json:"endpoint" db:"endpoint"`
	Status            string    `json:"status" db:"status"` // connecting, active, degraded, disabled
	LastHealthCheck   *string   `json:"last_health_check" db:"last_health_check"`
	LatencyMS         int64     `json:"latency_ms" db:"latency_ms"`
	ActiveConnections int       `json:"active_connections" db:"active_connections"`
	CreatedAt         string    `json:"created_at" db:"created_at"`
}

type VPNSession struct {
	ID              string  `json:"id" db:"id"`
	UserID          string  `json:"user_id" db:"user_id"`
	BackendTunnelID int     `json:"backend_tunnel_id" db:"backend_tunnel_id"`
	PeerPublicKey   string  `json:"peer_public_key" db:"peer_public_key"`
	AssignedIP      string  `json:"assigned_ip" db:"assigned_ip"`
	ConnectedAt     string  `json:"connected_at" db:"connected_at"`
	LastSeen        string  `json:"last_seen" db:"last_seen"`
	RxBytes         int64   `json:"rx_bytes" db:"rx_bytes"`
	TxBytes         int64   `json:"tx_bytes" db:"tx_bytes"`
	Status          string  `json:"status" db:"status"` // connected, disconnected, draining
}

type VPNConfig struct {
	Algorithm          LoadBalancingAlgorithm `json:"algorithm"`
	Weights            map[int]int            `json:"weights"` // server_id -> weight (1-100)
	HealthThresholdMS  int                    `json:"health_threshold_ms"`
	ListenPort         int                    `json:"listen_port"`
	SubnetCIDR         string                 `json:"subnet_cidr"`
	MaxTotalPeers      int                    `json:"max_total_peers"`
	MaxPeersPerBackend int                    `json:"max_peers_per_backend"`
}
```

---

## 9. Validation Helpers & Regular Expressions

```go
package models

import (
	"errors"
	"net"
	"regexp"
	"strings"
	"unicode"
)

var (
	usernameRegex = regexp.MustCompile(`^[a-z0-9_-]+$`)
	tlsDomainRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]{0,126}[a-zA-Z0-9])?$|^[a-zA-Z0-9]$`)
	hostnameRegex  = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,253}[a-zA-Z0-9])?$|^[a-zA-Z0-9]$`)
)

func validatePasswordComplexity(p string) error {
	var hasUpper, hasLower, hasDigit bool
	for _, c := range p {
		switch {
		case unicode.IsUpper(c):
			hasUpper = true
		case unicode.IsLower(c):
			hasLower = true
		case unicode.IsDigit(c):
			hasDigit = true
		}
	}
	if !hasUpper {
		return errors.New("password must contain at least one uppercase letter")
	}
	if !hasLower {
		return errors.New("password must contain at least one lowercase letter")
	}
	if !hasDigit {
		return errors.New("password must contain at least one digit")
	}
	return nil
}

func validateHost(host string) error {
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}
	if hostnameRegex.MatchString(host) {
		return nil
	}
	return errors.New("host must be a valid IPv4 address or hostname")
}

func validateTLSDomain(domain string) error {
	if !tlsDomainRegex.MatchString(domain) {
		return errors.New("tls_domain must be 1-128 chars, alphanumeric/dots/hyphens/underscores only")
	}
	return nil
}
```
