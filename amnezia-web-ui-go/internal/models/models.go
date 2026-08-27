package models

import (
	"time"
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
	AWGProfileLite     AWGObfuscationProfile = "lite"
	AWGProfileStandard AWGObfuscationProfile = "standard"
	AWGProfilePro      AWGObfuscationProfile = "pro"
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
	ResetStrategyNever   TrafficResetStrategy = "never"
	ResetStrategyMonthly TrafficResetStrategy = "monthly"
	ResetStrategyDaily   TrafficResetStrategy = "daily"
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
	ID        int64              `json:"id"`
	Name      string             `json:"name"`
	Host      string             `json:"host"`
	SSHUser   string             `json:"ssh_user"`
	SSHPort   int                `json:"ssh_port"`
	SSHPass   string             `json:"ssh_pass,omitempty"`
	SSHKey    string             `json:"ssh_key,omitempty"`
	Protocols map[string]any     `json:"protocols"`
	CreatedAt time.Time          `json:"created_at"`
	Status    ReachabilityStatus `json:"status,omitempty"`
}

// User represents an authenticated dashboard user or VPN client subscriber.
type User struct {
	ID                     string               `json:"id"`
	Username               string               `json:"username"`
	PasswordHash           string               `json:"password_hash,omitempty"`
	Role                   UserRole             `json:"role"`
	Enabled                bool                 `json:"enabled"`
	TrafficUsed            int64                `json:"traffic_used"`
	TrafficLimit           int64                `json:"traffic_limit"`
	MonthlyRx              int64                `json:"monthly_rx"`
	MonthlyTx              int64                `json:"monthly_tx"`
	ResetStrategy          TrafficResetStrategy `json:"reset_strategy"`
	ExpiresAt              *time.Time           `json:"expires_at,omitempty"`
	ExpirationDate         *time.Time           `json:"expiration_date,omitempty"`
	AWGMimicry             AWGMimicryProfile    `json:"awg_mimicry"`
	PasswordChangeRequired bool                 `json:"password_change_required"`
	Limits                 map[string]any       `json:"limits,omitempty"`
	CreatedAt              time.Time            `json:"created_at"`
}

// UserConnection binds a User to a specific protocol on a Server.
type UserConnection struct {
	ID         string            `json:"id"`
	UserID     string            `json:"user_id"`
	ServerID   int64             `json:"server_id"`
	Protocol   string            `json:"protocol"`
	ClientID   string            `json:"client_id"`
	Name       string            `json:"name"`
	AWGMimicry AWGMimicryProfile `json:"awg_mimicry"`
	RxBytes    int64             `json:"rx_bytes"`
	TxBytes    int64             `json:"tx_bytes"`
	CreatedAt  time.Time         `json:"created_at"`
}

// Setting represents a generic key-value configuration entry.
type Setting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// KnownHost stores verified SSH host public key fingerprints.
type KnownHost struct {
	ServerID    int64     `json:"server_id"`
	Fingerprint string    `json:"fingerprint"`
	FirstSeen   time.Time `json:"first_seen"`
}

// LeaderboardSnapshot stores historical user traffic stats.
type LeaderboardSnapshot struct {
	ID         int64     `json:"id"`
	Year       int       `json:"year"`
	Month      int       `json:"month"`
	Username   string    `json:"username"`
	Rank       int       `json:"rank"`
	Download   int64     `json:"download"`
	Upload     int64     `json:"upload"`
	Total      int64     `json:"total"`
	SnapshotAt time.Time `json:"snapshot_at"`
}
