package models

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Valid protocols supported by the panel.
var ValidProtocols = map[string]bool{
	"awg":    true,
	"xray":   true,
	"telemt": true,
	"dns":    true,
}

// ProtocolAliases maps legacy protocol names to standardized names.
var ProtocolAliases = map[string]string{
	"awg2":       "awg",
	"awg_legacy": "awg",
}

// NormalizeProtocol normalizes legacy protocol names (e.g., awg2, awg_legacy -> awg).
func NormalizeProtocol(proto string) string {
	if mapped, ok := ProtocolAliases[proto]; ok {
		return mapped
	}
	return proto
}

// IsValidProtocol checks if a given protocol string is supported.
func IsValidProtocol(proto string) bool {
	return ValidProtocols[NormalizeProtocol(proto)]
}

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
	RoleAdmin   UserRole = "admin"
	RoleSupport UserRole = "support"
	RoleUser    UserRole = "user"
)

// ValidRoles lists valid user roles.
var ValidRoles = map[UserRole]bool{
	RoleAdmin:   true,
	RoleSupport: true,
	RoleUser:    true,
}

// ValidateRole checks if a given user role is recognized.
func ValidateRole(role UserRole) bool {
	return ValidRoles[role]
}

// IsValidRole checks if a string corresponds to a valid user role.
func IsValidRole(role string) bool {
	return ValidRoles[UserRole(role)]
}

// IsAdminOrSupport checks if the role has administrative or support privileges.
func (r UserRole) IsAdminOrSupport() bool {
	return r == RoleAdmin || r == RoleSupport
}

// IsAdmin checks if the user has administrative or support privileges.
func (u *User) IsAdmin() bool {
	if u == nil {
		return false
	}
	return u.Role.IsAdminOrSupport()
}

// TrafficResetStrategy defines counter reset policy.
type TrafficResetStrategy string

const (
	ResetStrategyNever   TrafficResetStrategy = "never"   // Default: counters accumulate monotonically
	ResetStrategyMonthly TrafficResetStrategy = "monthly" // Active in runtime: resets monthly_rx/tx and traffic_used on 1st of month
	ResetStrategyDaily   TrafficResetStrategy = "daily"   // Allowed in schema/API for compatibility
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

// Server represents a managed remote VPN host.
type Server struct {
	ID         int64              `json:"id" db:"id"`
	Name       string             `json:"name" db:"name"`
	Host       string             `json:"host" db:"host"`
	SSHUser    string             `json:"ssh_user" db:"ssh_user"`
	SSHPort    int                `json:"ssh_port" db:"ssh_port"`
	SSHPass    string             `json:"ssh_pass,omitempty" db:"ssh_pass"`
	SSHKey     string             `json:"ssh_key,omitempty" db:"ssh_key"`
	Protocols  map[string]any     `json:"protocols" db:"protocols"`
	CreatedAt  time.Time          `json:"created_at" db:"created_at"`
	Status     ReachabilityStatus `json:"status,omitempty"`
	ServerInfo map[string]any     `json:"server_info,omitempty"`
}

// User represents an authenticated dashboard user or VPN client subscriber.
type User struct {
	ID                     string               `json:"id" db:"id"`
	Username               string               `json:"username" db:"username"`
	Email                  *string              `json:"email,omitempty" db:"email"`
	TelegramID             *string              `json:"telegramId,omitempty" db:"telegramId"`
	Description            *string              `json:"description,omitempty" db:"description"`
	PasswordHash           string               `json:"password_hash,omitempty" db:"password_hash"`
	Role                   UserRole             `json:"role" db:"role"`
	Enabled                bool                 `json:"enabled" db:"enabled"`
	TrafficLimit           int64                `json:"traffic_limit" db:"traffic_limit"`
	TrafficUsed            int64                `json:"traffic_used" db:"traffic_used"`
	TrafficTotal           int64                `json:"traffic_total" db:"traffic_total"`
	TrafficTotalRx         int64                `json:"traffic_total_rx" db:"traffic_total_rx"`
	TrafficTotalTx         int64                `json:"traffic_total_tx" db:"traffic_total_tx"`
	MonthlyRx              int64                `json:"monthly_rx" db:"monthly_rx"`
	MonthlyTx              int64                `json:"monthly_tx" db:"monthly_tx"`
	MonthlyResetAt         *string              `json:"monthly_reset_at,omitempty" db:"monthly_reset_at"`
	TrafficResetStrategy   TrafficResetStrategy `json:"traffic_reset_strategy" db:"traffic_reset_strategy"`
	ShareEnabled           bool                 `json:"share_enabled" db:"share_enabled"`
	ShareToken             *string              `json:"share_token,omitempty" db:"share_token"`
	SharePasswordHash      *string              `json:"share_password_hash,omitempty" db:"share_password_hash"`
	RemnaWaveUUID          *string              `json:"remnawave_uuid,omitempty" db:"remnawave_uuid"`
	CreatedAt              time.Time            `json:"created_at" db:"created_at"`
	LastResetAt            *string              `json:"last_reset_at,omitempty" db:"last_reset_at"`
	ExpirationDate         *time.Time           `json:"expiration_date,omitempty" db:"expiration_date"`
	ExpiresAt              *time.Time           `json:"expires_at,omitempty" db:"expires_at"`
	AWGMimicry             AWGMimicryProfile    `json:"awg_mimicry" db:"awg_mimicry"`
	PasswordChangeRequired bool                 `json:"password_change_required" db:"password_change_required"`
	Limits                 map[string]any       `json:"limits,omitempty" db:"limits"`
}

// UserConnection binds a User to a specific protocol on a Server.
type UserConnection struct {
	ID             string            `json:"id" db:"id"`
	UserID         string            `json:"user_id" db:"user_id"`
	ServerID       int64             `json:"server_id" db:"server_id"`
	Protocol       string            `json:"protocol" db:"protocol"`
	ClientID       string            `json:"client_id" db:"client_id"`
	Name           string            `json:"name" db:"name"`
	AWGMimicry     AWGMimicryProfile `json:"awg_mimicry" db:"awg_mimicry"`
	LastRx         int64             `json:"last_rx" db:"last_rx"`
	LastTx         int64             `json:"last_tx" db:"last_tx"`
	TrafficDeltaRx int64             `json:"traffic_delta_rx" db:"traffic_delta_rx"`
	TrafficDeltaTx int64             `json:"traffic_delta_tx" db:"traffic_delta_tx"`
	TrafficTotalRx int64             `json:"traffic_total_rx" db:"traffic_total_rx"`
	TrafficTotalTx int64             `json:"traffic_total_tx" db:"traffic_total_tx"`
	TrafficTotal   int64             `json:"traffic_total" db:"traffic_total"`
	CreatedAt      time.Time         `json:"created_at" db:"created_at"`
}

// Setting represents a generic key-value configuration entry.
type Setting struct {
	Key   string `json:"key" db:"key"`
	Value string `json:"value" db:"value"`
}

// KnownHost stores verified SSH host public key fingerprints.
type KnownHost struct {
	ServerID    int64     `json:"server_id" db:"server_id"`
	Fingerprint string    `json:"fingerprint" db:"fingerprint"`
	FirstSeen   time.Time `json:"first_seen" db:"first_seen"`
}

// LeaderboardEntry represents a ranked user in the traffic leaderboard.
type LeaderboardEntry struct {
	Rank     int    `json:"rank"`
	Username string `json:"username"`
	Download int64  `json:"download"`
	Upload   int64  `json:"upload"`
	Total    int64  `json:"total"`
}

// LeaderboardSnapshot stores historical user traffic stats.
type LeaderboardSnapshot struct {
	ID         int64     `json:"id" db:"id"`
	Year       int       `json:"year" db:"year"`
	Month      int       `json:"month" db:"month"`
	Username   string    `json:"username" db:"username"`
	Rank       int       `json:"rank" db:"rank"`
	Download   int64     `json:"download" db:"download"`
	Upload     int64     `json:"upload" db:"upload"`
	Total      int64     `json:"total" db:"total"`
	SnapshotAt time.Time `json:"snapshot_at" db:"snapshot_at"`
}

// ConnectionLogEntry tracks timestamps of client connection creations for rate limiting.
type ConnectionLogEntry struct {
	ID        int64     `json:"id" db:"id"`
	UserID    string    `json:"user_id" db:"user_id"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// BackendTunnel represents an in-process AWG tunnel to a backend VPN server.
type BackendTunnel struct {
	ID                int64      `json:"id" db:"id"`
	ServerID          int64      `json:"server_id" db:"server_id"`
	InterfaceName     string     `json:"interface_name" db:"interface_name"`
	PublicKey         string     `json:"public_key" db:"public_key"`
	PrivateKey        string     `json:"-" db:"private_key"` // Encrypted at rest
	Endpoint          string     `json:"endpoint" db:"endpoint"`
	Status            string     `json:"status" db:"status"` // connecting, active, degraded, disabled
	LastHealthCheck   *time.Time `json:"last_health_check,omitempty" db:"last_health_check"`
	LatencyMS         int64      `json:"latency_ms" db:"latency_ms"`
	ActiveConnections int        `json:"active_connections" db:"active_connections"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
}

// VPNSession tracks an active user connection through the portal AWG endpoint.
type VPNSession struct {
	ID              string    `json:"id" db:"id"`
	UserID          string    `json:"user_id" db:"user_id"`
	BackendTunnelID int64     `json:"backend_tunnel_id" db:"backend_tunnel_id"`
	PeerPublicKey   string    `json:"peer_public_key" db:"peer_public_key"`
	AssignedIP      string    `json:"assigned_ip" db:"assigned_ip"`
	ConnectedAt     time.Time `json:"connected_at" db:"connected_at"`
	LastSeen        time.Time `json:"last_seen" db:"last_seen"`
	RxBytes         int64     `json:"rx_bytes" db:"rx_bytes"`
	TxBytes         int64     `json:"tx_bytes" db:"tx_bytes"`
	Status          string    `json:"status" db:"status"` // connected, disconnected, draining
}

// VPNConfig stores dynamic configuration for the in-process VPN subsystem.
type VPNConfig struct {
	Algorithm          LoadBalancingAlgorithm `json:"algorithm"`
	Weights            map[int64]int          `json:"weights"` // server_id -> weight (1-100)
	HealthThresholdMS  int                    `json:"health_threshold_ms"`
	ListenPort         int                    `json:"listen_port"`
	SubnetCIDR         string                 `json:"subnet_cidr"`
	MaxTotalPeers      int                    `json:"max_total_peers"`
	MaxPeersPerBackend int                    `json:"max_peers_per_backend"`
}

// AppearanceSettings holds UI display configuration.
type AppearanceSettings struct {
	Title    string `json:"title"`
	Logo     string `json:"logo"`
	Subtitle string `json:"subtitle"`
	Language string `json:"language"`
}

// SyncSettings holds RemnaWave external sync configuration.
type SyncSettings struct {
	RemnawaveURL         string `json:"remnawave_url"`
	RemnawaveAPIKey      string `json:"remnawave_api_key"`
	RemnawaveSync        bool   `json:"remnawave_sync"`
	RemnawaveSyncUsers   bool   `json:"remnawave_sync_users"`
	RemnawaveCreateConns bool   `json:"remnawave_create_conns"`
	RemnawaveServerID    int64  `json:"remnawave_server_id"`
	RemnawaveProtocol    string `json:"remnawave_protocol"`
}

// CaptchaSettings holds CAPTCHA toggle configuration.
type CaptchaSettings struct {
	Enabled bool `json:"enabled"`
}

// SSLSettings holds custom TLS certificate configuration.
type SSLSettings struct {
	Enabled   bool   `json:"enabled"`
	Domain    string `json:"domain"`
	CertPath  string `json:"cert_path"`
	KeyPath   string `json:"key_path"`
	CertText  string `json:"cert_text"`
	KeyText   string `json:"key_text"`
	PanelPort int    `json:"panel_port"`
}

// ConnectionLimits holds global connection creation rate limits.
type ConnectionLimits struct {
	MaxConnectionsPerUser     int `json:"max_connections_per_user"`
	ConnectionRateLimitCount  int `json:"connection_rate_limit_count"`
	ConnectionRateLimitWindow int `json:"connection_rate_limit_window"`
}

// BackupData represents the complete database dump for backup / restore / data.json migration.
type BackupData struct {
	Servers               []map[string]any `json:"servers"`
	Users                 []map[string]any `json:"users"`
	UserConnections       []map[string]any `json:"user_connections"`
	ConnectionCreationLog []map[string]any `json:"connection_creation_log"`
	KnownHosts            []map[string]any `json:"known_hosts,omitempty"`
	LeaderboardSnapshots  []map[string]any `json:"leaderboard_snapshots,omitempty"`
	Settings              map[string]any   `json:"settings"`
}

// ---- Validation and Request/Response Models ----

var (
	UsernameRegex      = regexp.MustCompile(`^[a-z0-9_-]+$`)
	SetupUsernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	TLSDomainRegex     = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]{0,126}[a-zA-Z0-9])?$|^[a-zA-Z0-9]$`)
	HostnameRegex      = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,253}[a-zA-Z0-9])?$|^[a-zA-Z0-9]$`)
)

// ValidatePasswordComplexity enforces password strength rules.
func ValidatePasswordComplexity(p string) error {
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

// ValidateHost checks if a host string is a valid IP or hostname.
func ValidateHost(host string) error {
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}
	if HostnameRegex.MatchString(host) {
		return nil
	}
	return errors.New("host must be a valid IPv4 address or hostname")
}

// ValidateTLSDomain checks if a TLS domain string matches permitted patterns.
func ValidateTLSDomain(domain string) error {
	if !TLSDomainRegex.MatchString(domain) {
		return errors.New("tls_domain must be 1-128 chars, alphanumeric/dots/hyphens/underscores only")
	}
	return nil
}

// LoginRequest defines credentials for dashboard authentication.
type LoginRequest struct {
	Username string  `json:"username"`
	Password string  `json:"password"`
	Captcha  *string `json:"captcha,omitempty"`
}

func (r *LoginRequest) Validate() error {
	if strings.TrimSpace(r.Username) == "" {
		return errors.New("username is required")
	}
	if r.Password == "" {
		return errors.New("password is required")
	}
	return nil
}

// SetupRequest defines the initial admin account creation payload.
type SetupRequest struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirm_password"`
}

func (r *SetupRequest) Validate() error {
	if len(r.Username) < 3 || len(r.Username) > 32 || !SetupUsernameRegex.MatchString(r.Username) {
		return errors.New("username must be 3-32 alphanumeric characters or underscore")
	}
	if len(r.Password) < 8 || len(r.Password) > 4096 {
		return errors.New("password must be between 8 and 4096 characters")
	}
	if r.Password != r.ConfirmPassword {
		return errors.New("passwords do not match")
	}
	return ValidatePasswordComplexity(r.Password)
}

// ChangePasswordRequest defines password update payload for the current user.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

func (r *ChangePasswordRequest) Validate() error {
	if strings.Contains(r.NewPassword, "\x00") {
		return errors.New("password must not contain null bytes")
	}
	if len(r.NewPassword) < 8 || len(r.NewPassword) > 4096 {
		return errors.New("password must be between 8 and 4096 characters")
	}
	if r.NewPassword != r.ConfirmPassword {
		return errors.New("passwords do not match")
	}
	return ValidatePasswordComplexity(r.NewPassword)
}

// AddServerRequest defines parameters for adding a new server.
type AddServerRequest struct {
	Host       string `json:"host"`
	SSHPort    int    `json:"ssh_port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
	Name       string `json:"name"`
}

func (r *AddServerRequest) Validate() error {
	if r.SSHPort == 0 {
		r.SSHPort = 22
	}
	if r.SSHPort < 1 || r.SSHPort > 65535 {
		return errors.New("ssh_port must be between 1 and 65535")
	}
	if r.Host != "" {
		return ValidateHost(r.Host)
	}
	return nil
}

// ConfirmFingerprintRequest defines SSH fingerprint verification payload.
type ConfirmFingerprintRequest struct {
	AddServerRequest
	ServerInfo  string `json:"server_info"`
	Fingerprint string `json:"fingerprint"`
}

// InstallProtocolRequest defines protocol deployment options on a server.
type InstallProtocolRequest struct {
	Protocol       string                 `json:"protocol"`
	Port           string                 `json:"port"`
	TLSEmulation   *bool                  `json:"tls_emulation,omitempty"`
	TLSDomain      *string                `json:"tls_domain,omitempty"`
	MaxConnections *int                   `json:"max_connections,omitempty"`
	AWGProfile     *AWGObfuscationProfile `json:"awg_profile,omitempty"`
	AWGCPSProtocol *string                `json:"awg_cps_protocol,omitempty"`
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
		if err := ValidateTLSDomain(*r.TLSDomain); err != nil {
			return err
		}
	}
	return nil
}

// AddUserRequest defines user creation parameters.
type AddUserRequest struct {
	Username             string   `json:"username"`
	Password             string   `json:"password"`
	Role                 UserRole `json:"role"`
	TelegramID           *string  `json:"telegramId,omitempty"`
	Email                *string  `json:"email,omitempty"`
	Description          *string  `json:"description,omitempty"`
	TrafficLimit         float64  `json:"traffic_limit"`
	TrafficResetStrategy string   `json:"traffic_reset_strategy"`
	ServerID             *int64   `json:"server_id,omitempty"`
	Protocol             *string  `json:"protocol,omitempty"`
	ConnectionName       *string  `json:"connection_name,omitempty"`
	ExpirationDate       *string  `json:"expiration_date,omitempty"`
	ExpiresAt            *string  `json:"expires_at,omitempty"`
	AWGMimicry           *string  `json:"awg_mimicry,omitempty"`
}

func (r *AddUserRequest) Validate() error {
	r.Username = strings.ToLower(strings.TrimSpace(r.Username))
	if len(r.Username) < 3 || len(r.Username) > 255 || !UsernameRegex.MatchString(r.Username) {
		return errors.New("username must be 3-255 characters with lowercase letters, digits, hyphens, and underscores")
	}
	if len(r.Password) < 8 || len(r.Password) > 4096 {
		return errors.New("password must be between 8 and 4096 characters")
	}
	if err := ValidatePasswordComplexity(r.Password); err != nil {
		return err
	}
	if r.Role != "" && !ValidateRole(r.Role) {
		return fmt.Errorf("invalid user role: %s", r.Role)
	}
	if r.Protocol != nil && *r.Protocol != "" {
		*r.Protocol = NormalizeProtocol(*r.Protocol)
		if !IsValidProtocol(*r.Protocol) {
			return fmt.Errorf("invalid protocol: %s", *r.Protocol)
		}
	}
	return nil
}
