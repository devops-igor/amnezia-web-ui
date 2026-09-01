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

// ContainerNameForProtocol maps a validated protocol to its Docker container name.
// The second return value is false for unknown protocols, so callers must reject
// unvalidated user input instead of interpolating it into shell commands.
func ContainerNameForProtocol(proto string) (string, bool) {
	switch NormalizeProtocol(proto) {
	case "awg":
		return "amnezia-awg", true
	case "telemt":
		return "telemt", true
	case "dns":
		return "amnezia-dns", true
	default:
		return "", false
	}
}

// ConfigPathForProtocol maps a validated protocol to its remote config file path.
// Rejecting unknown protocols here prevents path injection via user-supplied input.
func ConfigPathForProtocol(proto string) (string, bool) {
	switch NormalizeProtocol(proto) {
	case "awg":
		return "/opt/amnezia/awg/awg0.conf", true
	case "dns":
		return "/opt/amnezia/dns/unbound.conf", true
	case "telemt":
		return "/opt/mtproxyl/settings.conf", true
	default:
		return "", false
	}
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
	SSHPass    string             `json:"-" db:"ssh_pass"`
	SSHKey     string             `json:"-" db:"ssh_key"`
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
	PasswordHash           string               `json:"-" db:"password_hash"`
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
	SharePasswordHash      *string              `json:"-" db:"share_password_hash"`
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
	ServerName     string            `json:"server_name,omitempty" db:"-"`
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

// SessionData represents authenticated session information stored in signed cookies and request context.
type SessionData struct {
	UserID                 string          `json:"user_id,omitempty"`
	Username               string          `json:"username,omitempty"`
	Role                   UserRole        `json:"role,omitempty"`
	PasswordChangeRequired bool            `json:"password_change_required,omitempty"`
	CaptchaAnswer          string          `json:"captcha_answer,omitempty"`
	ShareAuthenticated     map[string]bool `json:"share_authenticated,omitempty"`
	Extra                  map[string]any  `json:"extra,omitempty"`
}

// IsAuthenticated returns true if the session contains a valid user ID.
func (s *SessionData) IsAuthenticated() bool {
	return s != nil && s.UserID != ""
}

// IsAdmin returns true if the session user has admin privileges.
func (s *SessionData) IsAdmin() bool {
	return s != nil && s.Role == RoleAdmin
}

// IsAdminOrSupport returns true if the session user has admin or support privileges.
func (s *SessionData) IsAdminOrSupport() bool {
	return s != nil && s.Role.IsAdminOrSupport()
}

// ToMap converts SessionData to a map for serialization.
func (s *SessionData) ToMap() map[string]any {
	if s == nil {
		return nil
	}
	m := make(map[string]any)
	if s.UserID != "" {
		m["user_id"] = s.UserID
	}
	if s.Username != "" {
		m["username"] = s.Username
	}
	if s.Role != "" {
		m["role"] = string(s.Role)
	}
	if s.PasswordChangeRequired {
		m["password_change_required"] = true
	}
	if s.CaptchaAnswer != "" {
		m["captcha_answer"] = s.CaptchaAnswer
	}
	if len(s.ShareAuthenticated) > 0 {
		m["share_authenticated"] = s.ShareAuthenticated
	}
	for k, v := range s.Extra {
		m[k] = v
	}
	return m
}

// SessionDataFromMap converts a generic map into typed SessionData.
func SessionDataFromMap(m map[string]any) *SessionData {
	if m == nil {
		return nil
	}
	s := &SessionData{
		ShareAuthenticated: make(map[string]bool),
		Extra:              make(map[string]any),
	}
	for k, v := range m {
		switch k {
		case "user_id":
			if str, ok := v.(string); ok {
				s.UserID = str
			}
		case "username":
			if str, ok := v.(string); ok {
				s.Username = str
			}
		case "role":
			if str, ok := v.(string); ok {
				s.Role = UserRole(str)
			}
		case "password_change_required":
			if b, ok := v.(bool); ok {
				s.PasswordChangeRequired = b
			}
		case "captcha_answer":
			if str, ok := v.(string); ok {
				s.CaptchaAnswer = str
			}
		case "share_authenticated":
			if sm, ok := v.(map[string]bool); ok {
				for token, auth := range sm {
					s.ShareAuthenticated[token] = auth
				}
			} else if sm, ok := v.(map[string]any); ok {
				for token, auth := range sm {
					if b, ok := auth.(bool); ok {
						s.ShareAuthenticated[token] = b
					}
				}
			}
		default:
			s.Extra[k] = v
		}
	}
	return s
}

// ProtocolRequest defines a request with just a protocol name.
type ProtocolRequest struct {
	Protocol string `json:"protocol"`
}

func (r *ProtocolRequest) Validate() error {
	r.Protocol = NormalizeProtocol(r.Protocol)
	if !IsValidProtocol(r.Protocol) {
		return fmt.Errorf("invalid protocol: %s", r.Protocol)
	}
	return nil
}

// ServerConfigSaveRequest defines a payload to save server protocol configuration.
type ServerConfigSaveRequest struct {
	Protocol string `json:"protocol"`
	Config   string `json:"config"`
}

func (r *ServerConfigSaveRequest) Validate() error {
	r.Protocol = NormalizeProtocol(r.Protocol)
	if !IsValidProtocol(r.Protocol) {
		return fmt.Errorf("invalid protocol: %s", r.Protocol)
	}
	if len(r.Config) == 0 || len(r.Config) > 65536 {
		return errors.New("config must be between 1 and 65536 characters")
	}
	return nil
}

// AddConnectionRequest defines parameters for adding a new connection to a server.
type AddConnectionRequest struct {
	Protocol          string  `json:"protocol"`
	Name              string  `json:"name"`
	UserID            *string `json:"user_id,omitempty"`
	TelemtQuota       *string `json:"telemt_quota,omitempty"`
	TelemtMaxIPs      *int    `json:"telemt_max_ips,omitempty"`
	TelemtExpiry      *string `json:"telemt_expiry,omitempty"`
	AWGSpeedLimitDown *int    `json:"awg_speed_limit_down,omitempty"`
	AWGSpeedLimitUp   *int    `json:"awg_speed_limit_up,omitempty"`
	AWGMimicry        *string `json:"awg_mimicry,omitempty"`
}

func (r *AddConnectionRequest) Validate() error {
	r.Protocol = NormalizeProtocol(r.Protocol)
	if !IsValidProtocol(r.Protocol) {
		return fmt.Errorf("invalid protocol: %s", r.Protocol)
	}
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len(r.Name) > 255 {
		return errors.New("name must be between 1 and 255 characters")
	}
	return nil
}

// MyAddConnectionRequest defines parameters for adding a user's own connection.
type MyAddConnectionRequest struct {
	ServerID          int64   `json:"server_id"`
	Protocol          string  `json:"protocol"`
	Name              string  `json:"name"`
	TelemtQuota       *string `json:"telemt_quota,omitempty"`
	TelemtMaxIPs      *int    `json:"telemt_max_ips,omitempty"`
	TelemtExpiry      *string `json:"telemt_expiry,omitempty"`
	AWGSpeedLimitDown *int    `json:"awg_speed_limit_down,omitempty"`
	AWGSpeedLimitUp   *int    `json:"awg_speed_limit_up,omitempty"`
	AWGMimicry        *string `json:"awg_mimicry,omitempty"`
}

func (r *MyAddConnectionRequest) Validate() error {
	if r.ServerID <= 0 {
		return errors.New("server_id must be greater than 0")
	}
	r.Protocol = NormalizeProtocol(r.Protocol)
	if !IsValidProtocol(r.Protocol) {
		return fmt.Errorf("invalid protocol: %s", r.Protocol)
	}
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len(r.Name) > 255 {
		return errors.New("name must be between 1 and 255 characters")
	}
	return nil
}

// AddUserConnectionRequest defines parameters for adding a connection to a specific user.
type AddUserConnectionRequest struct {
	ServerID     int64   `json:"server_id"`
	Protocol     string  `json:"protocol"`
	Name         string  `json:"name"`
	ClientID     *string `json:"client_id,omitempty"`
	TelemtQuota  *string `json:"telemt_quota,omitempty"`
	TelemtMaxIPs *int    `json:"telemt_max_ips,omitempty"`
	TelemtExpiry *string `json:"telemt_expiry,omitempty"`
	AWGMimicry   *string `json:"awg_mimicry,omitempty"`
}

func (r *AddUserConnectionRequest) Validate() error {
	if r.ServerID <= 0 {
		return errors.New("server_id must be greater than 0")
	}
	r.Protocol = NormalizeProtocol(r.Protocol)
	if !IsValidProtocol(r.Protocol) {
		return fmt.Errorf("invalid protocol: %s", r.Protocol)
	}
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len(r.Name) > 255 {
		return errors.New("name must be between 1 and 255 characters")
	}
	return nil
}

// EditConnectionRequest defines parameters for editing an existing connection.
type EditConnectionRequest struct {
	Protocol          string  `json:"protocol"`
	ClientID          string  `json:"client_id"`
	Name              *string `json:"name,omitempty"`
	UserID            *string `json:"user_id,omitempty"`
	TelemtQuota       *string `json:"telemt_quota,omitempty"`
	TelemtMaxIPs      *int    `json:"telemt_max_ips,omitempty"`
	TelemtExpiry      *string `json:"telemt_expiry,omitempty"`
	AWGSpeedLimitDown *int    `json:"awg_speed_limit_down,omitempty"`
	AWGSpeedLimitUp   *int    `json:"awg_speed_limit_up,omitempty"`
	AWGMimicry        *string `json:"awg_mimicry,omitempty"`
}

// RenameConnectionRequest defines connection rename payload.
type RenameConnectionRequest struct {
	Name string `json:"name"`
}

func (r *RenameConnectionRequest) Validate() error {
	r.Name = strings.TrimSpace(r.Name)
	if strings.Contains(r.Name, "\x00") {
		return errors.New("name cannot contain null bytes")
	}
	if r.Name == "" || len(r.Name) > 255 {
		return errors.New("name must be between 1 and 255 characters")
	}
	return nil
}

// SpeedLimitRequest defines client speed limit configuration.
type SpeedLimitRequest struct {
	ClientID       string `json:"client_id"`
	SpeedLimitDown *int   `json:"speed_limit_down,omitempty"`
	SpeedLimitUp   *int   `json:"speed_limit_up,omitempty"`
}

// AwgSpeedLimitConfigRequest defines AWG server-wide speed limits.
type AwgSpeedLimitConfigRequest struct {
	GlobalSpeedLimitDown  *int `json:"global_speed_limit_down,omitempty"`
	GlobalSpeedLimitUp    *int `json:"global_speed_limit_up,omitempty"`
	DefaultSpeedLimitDown *int `json:"default_speed_limit_down,omitempty"`
	DefaultSpeedLimitUp   *int `json:"default_speed_limit_up,omitempty"`
}

// UpdateUserRequest defines parameters for updating an existing user.
type UpdateUserRequest struct {
	TelegramID           *string  `json:"telegramId,omitempty"`
	Email                *string  `json:"email,omitempty"`
	Description          *string  `json:"description,omitempty"`
	TrafficLimit         *float64 `json:"traffic_limit,omitempty"`
	TrafficResetStrategy *string  `json:"traffic_reset_strategy,omitempty"`
	ExpirationDate       *string  `json:"expiration_date,omitempty"`
	ExpiresAt            *string  `json:"expires_at,omitempty"`
	AWGMimicry           *string  `json:"awg_mimicry,omitempty"`
	Password             *string  `json:"password,omitempty"`
}

func (r *UpdateUserRequest) Validate() error {
	if r.Password != nil && *r.Password != "" {
		if len(*r.Password) < 8 || len(*r.Password) > 4096 {
			return errors.New("password must be between 8 and 4096 characters")
		}
		return ValidatePasswordComplexity(*r.Password)
	}
	return nil
}

// ToggleUserRequest defines user enable/disable payload.
type ToggleUserRequest struct {
	Enabled bool `json:"enabled"`
}

// ToggleConnectionRequest defines connection enable/disable payload.
type ToggleConnectionRequest struct {
	Enabled bool `json:"enabled"`
}

// ConnectionActionRequest defines common client ID + protocol request payload.
type ConnectionActionRequest struct {
	ClientID string `json:"client_id"`
	Protocol string `json:"protocol"`
}

func (r *ConnectionActionRequest) Validate() error {
	if r.ClientID == "" {
		return errors.New("client_id is required")
	}
	r.Protocol = NormalizeProtocol(r.Protocol)
	if !IsValidProtocol(r.Protocol) {
		return fmt.Errorf("invalid protocol: %s", r.Protocol)
	}
	return nil
}

// ShareSetupRequest defines parameters for configuring user share link.
type ShareSetupRequest struct {
	Password       *string `json:"password,omitempty"`
	ExpiresInHours *int    `json:"expires_in_hours,omitempty"`
}

// ShareAuthRequest defines password verification payload for public share link.
type ShareAuthRequest struct {
	Password string `json:"password"`
}

// AutoTrialRequest defines parameters for server reachability/auto-trial testing.
type AutoTrialRequest struct {
	ServerID  int64    `json:"server_id"`
	Protocols []string `json:"protocols"`
}

// SaveSettingsRequest defines settings save payload.
type SaveSettingsRequest struct {
	Appearance AppearanceSettings     `json:"appearance"`
	Sync       SyncSettings           `json:"sync"`
	Captcha    CaptchaSettings        `json:"captcha"`
	Telegram   map[string]interface{} `json:"telegram,omitempty"`
	SSL        SSLSettings            `json:"ssl"`
	Limits     ConnectionLimits       `json:"limits"`
}

// ServerItemResponse represents server data in responses.
type ServerItemResponse struct {
	ID         int64          `json:"id"`
	Name       string         `json:"name"`
	Host       string         `json:"host"`
	SSHPort    int            `json:"ssh_port"`
	Username   string         `json:"username"`
	ServerInfo map[string]any `json:"server_info,omitempty"`
	Protocols  map[string]any `json:"protocols"`
}

// ServerStatsResponse represents server resource telemetry.
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

// ServerCheckResponse represents server connectivity check result.
type ServerCheckResponse struct {
	Connection      string         `json:"connection"`
	DockerInstalled bool           `json:"docker_installed"`
	Protocols       map[string]any `json:"protocols"`
}

// UserItemResponse represents user data in responses.
type UserItemResponse struct {
	ID                   string  `json:"id"`
	Username             string  `json:"username"`
	Role                 string  `json:"role"`
	Enabled              bool    `json:"enabled"`
	CreatedAt            string  `json:"created_at"`
	TelegramID           *string `json:"telegramId,omitempty"`
	Email                *string `json:"email,omitempty"`
	Description          *string `json:"description,omitempty"`
	ConnectionsCount     int     `json:"connections_count"`
	TrafficUsed          int64   `json:"traffic_used"`
	TrafficTotal         int64   `json:"traffic_total"`
	TrafficLimit         int64   `json:"traffic_limit"`
	TrafficResetStrategy string  `json:"traffic_reset_strategy"`
	LastResetAt          *string `json:"last_reset_at,omitempty"`
	ExpirationDate       *string `json:"expiration_date,omitempty"`
	ExpiresAt            *string `json:"expires_at,omitempty"`
	AWGMimicry           *string `json:"awg_mimicry,omitempty"`
	ShareEnabled         bool    `json:"share_enabled"`
	ShareToken           *string `json:"share_token,omitempty"`
	HasSharePassword     bool    `json:"has_share_password"`
	Source               string  `json:"source"`
}

// PaginatedUsersResponse represents paginated user list response.
type PaginatedUsersResponse struct {
	Users []UserItemResponse `json:"users"`
	Total int                `json:"total"`
	Page  int                `json:"page"`
	Size  int                `json:"size"`
	Pages int                `json:"pages"`
}

// LeaderboardEntryResponse represents ranked user in leaderboard response.
type LeaderboardEntryResponse struct {
	Rank     int    `json:"rank"`
	Username string `json:"username"`
	Download int64  `json:"download"`
	Upload   int64  `json:"upload"`
	Total    int64  `json:"total"`
}

// LeaderboardResponse represents leaderboard response.
type LeaderboardResponse struct {
	Period          string                     `json:"period"`
	Entries         []LeaderboardEntryResponse `json:"entries"`
	CurrentUserRank *int                       `json:"current_user_rank,omitempty"`
	MonthlyLabel    *string                    `json:"monthly_label,omitempty"`
}
