package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
)

// GetBackendTunnels retrieves all backend AWG tunnel definitions.
func (d *DB) GetBackendTunnels(ctx context.Context) ([]models.BackendTunnel, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, server_id, interface_name, public_key, private_key, endpoint,
		status, last_health_check, latency_ms, active_connections, created_at
		FROM backend_tunnels ORDER BY id`

	rows, err := d.sqlDB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query backend tunnels: %w", err)
	}
	defer rows.Close()

	var tunnels []models.BackendTunnel
	for rows.Next() {
		t, err := d.scanBackendTunnel(rows)
		if err != nil {
			return nil, err
		}
		tunnels = append(tunnels, t)
	}

	return tunnels, rows.Err()
}

// GetAllBackendTunnels is an alias for GetBackendTunnels.
func (d *DB) GetAllBackendTunnels(ctx context.Context) ([]models.BackendTunnel, error) {
	return d.GetBackendTunnels(ctx)
}

// GetBackendTunnel retrieves a backend tunnel by ID. Returns nil, nil if not found.
func (d *DB) GetBackendTunnel(ctx context.Context, id int64) (*models.BackendTunnel, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, server_id, interface_name, public_key, private_key, endpoint,
		status, last_health_check, latency_ms, active_connections, created_at
		FROM backend_tunnels WHERE id = ?`

	row := d.sqlDB.QueryRowContext(ctx, query, id)
	t, err := d.scanBackendTunnelRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get backend tunnel %d: %w", id, err)
	}
	return &t, nil
}

// GetBackendTunnelByID is an alias for GetBackendTunnel.
func (d *DB) GetBackendTunnelByID(ctx context.Context, id int64) (*models.BackendTunnel, error) {
	return d.GetBackendTunnel(ctx, id)
}

// CreateBackendTunnel inserts a new backend tunnel, encrypting the private key at rest.
func (d *DB) CreateBackendTunnel(ctx context.Context, t *models.BackendTunnel) (int64, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	encPrivKey := t.PrivateKey
	if encPrivKey != "" && !security.LooksLikeFernetToken(encPrivKey) {
		ep, err := security.EncryptCredential(encPrivKey, d.secretKey)
		if err != nil {
			return 0, fmt.Errorf("failed to encrypt backend tunnel private key: %w", err)
		}
		encPrivKey = ep
	}

	if t.Status == "" {
		t.Status = "connecting"
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	createdAtStr := formatTime(t.CreatedAt)
	healthCheckStr := formatTimePtr(t.LastHealthCheck)

	query := `INSERT INTO backend_tunnels (
		server_id, interface_name, public_key, private_key, endpoint,
		status, last_health_check, latency_ms, active_connections, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	res, err := d.sqlDB.ExecContext(ctx, query,
		t.ServerID,
		t.InterfaceName,
		t.PublicKey,
		encPrivKey,
		t.Endpoint,
		t.Status,
		healthCheckStr,
		t.LatencyMS,
		t.ActiveConnections,
		createdAtStr,
	)

	if err != nil {
		return 0, fmt.Errorf("failed to insert backend tunnel: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	t.ID = id
	return id, nil
}

// UpdateBackendTunnel dynamically updates fields on a backend tunnel record.
func (d *DB) UpdateBackendTunnel(ctx context.Context, id int64, updates map[string]any) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	for k := range updates {
		if !allowedBackendTunnelColumns[k] {
			return fmt.Errorf("unknown backend tunnel column: %s", k)
		}
	}

	if len(updates) == 0 {
		return nil
	}

	var setClauses []string
	var values []any

	for col, val := range updates {
		if col == "private_key" {
			if s, ok := val.(string); ok && s != "" && !security.LooksLikeFernetToken(s) {
				enc, err := security.EncryptCredential(s, d.secretKey)
				if err != nil {
					return fmt.Errorf("failed to encrypt private key: %w", err)
				}
				val = enc
			}
		}
		if col == "last_health_check" {
			if t, ok := val.(*time.Time); ok && t != nil {
				val = formatTime(*t)
			} else if t, ok := val.(time.Time); ok {
				val = formatTime(t)
			}
		}

		setClauses = append(setClauses, fmt.Sprintf("%s = ?", col))
		values = append(values, val)
	}

	values = append(values, id)
	// #nosec G201 -- Column names are validated against allowedBackendTunnelColumns allowlist
	query := fmt.Sprintf("UPDATE backend_tunnels SET %s WHERE id = ?", strings.Join(setClauses, ", "))

	_, err := d.sqlDB.ExecContext(ctx, query, values...)
	if err != nil {
		return fmt.Errorf("failed to update backend tunnel %d: %w", id, err)
	}

	return nil
}

// UpdateBackendTunnelStatus updates status, latency, and health check timestamp.
func (d *DB) UpdateBackendTunnelStatus(ctx context.Context, id int64, status string, latencyMS int64) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	nowStr := time.Now().Format(time.RFC3339)
	query := `UPDATE backend_tunnels SET status = ?, latency_ms = ?, last_health_check = ? WHERE id = ?`

	_, err := d.sqlDB.ExecContext(ctx, query, status, latencyMS, nowStr, id)
	if err != nil {
		return fmt.Errorf("failed to update backend tunnel status: %w", err)
	}
	return nil
}

// DeleteBackendTunnel removes a backend tunnel record.
func (d *DB) DeleteBackendTunnel(ctx context.Context, id int64) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	_, err := d.sqlDB.ExecContext(ctx, "DELETE FROM backend_tunnels WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete backend tunnel %d: %w", id, err)
	}
	return nil
}

// GetBackendTunnelByServerID retrieves a backend tunnel by its server_id. Returns nil, nil if not found.
func (d *DB) GetBackendTunnelByServerID(ctx context.Context, serverID int64) (*models.BackendTunnel, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, server_id, interface_name, public_key, private_key, endpoint,
		status, last_health_check, latency_ms, active_connections, created_at
		FROM backend_tunnels WHERE server_id = ?`

	row := d.sqlDB.QueryRowContext(ctx, query, serverID)
	t, err := d.scanBackendTunnelRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get backend tunnel for server %d: %w", serverID, err)
	}
	return &t, nil
}

// GetVPNSessionByPeerKey retrieves an active VPN session by peer public key.
func (d *DB) GetVPNSessionByPeerKey(ctx context.Context, key string) (*models.VPNSession, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, user_id, backend_tunnel_id, peer_public_key, assigned_ip,
		connected_at, last_seen, rx_bytes, tx_bytes, status
		FROM vpn_sessions WHERE peer_public_key = ?`

	row := d.sqlDB.QueryRowContext(ctx, query, key)
	s, err := d.scanVPNSessionRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get vpn session by peer key: %w", err)
	}
	return &s, nil
}

// GetVPNSessionByID retrieves a VPN session by its UUID. Returns nil, nil if not found.
func (d *DB) GetVPNSessionByID(ctx context.Context, id string) (*models.VPNSession, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, user_id, backend_tunnel_id, peer_public_key, assigned_ip,
		connected_at, last_seen, rx_bytes, tx_bytes, status
		FROM vpn_sessions WHERE id = ?`

	row := d.sqlDB.QueryRowContext(ctx, query, id)
	s, err := d.scanVPNSessionRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get vpn session %s: %w", id, err)
	}
	return &s, nil
}

// GetVPNSessionsByUserID retrieves all VPN sessions for a specific user.
func (d *DB) GetVPNSessionsByUserID(ctx context.Context, userID string) ([]models.VPNSession, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, user_id, backend_tunnel_id, peer_public_key, assigned_ip,
		connected_at, last_seen, rx_bytes, tx_bytes, status
		FROM vpn_sessions WHERE user_id = ? ORDER BY connected_at DESC`

	rows, err := d.sqlDB.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query user vpn sessions: %w", err)
	}
	defer rows.Close()

	var sessions []models.VPNSession
	for rows.Next() {
		s, err := d.scanVPNSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}

	return sessions, rows.Err()
}

// GetVPNConfig retrieves the load balancing and VPN configuration from settings.
func (d *DB) GetVPNConfig(ctx context.Context) (*models.VPNConfig, error) {
	var cfg models.VPNConfig
	if err := d.GetSetting(ctx, "vpn_config", &cfg); err != nil {
		return nil, err
	}
	if cfg.Algorithm == "" {
		cfg.Algorithm = models.LBLeastConnections
	}
	if cfg.ListenPort == 0 {
		cfg.ListenPort = 51820
	}
	if cfg.SubnetCIDR == "" {
		cfg.SubnetCIDR = "10.100.0.0/16"
	}
	if cfg.HealthThresholdMS == 0 {
		cfg.HealthThresholdMS = 500
	}
	if cfg.MaxTotalPeers == 0 {
		cfg.MaxTotalPeers = 1000
	}
	if cfg.MaxPeersPerBackend == 0 {
		cfg.MaxPeersPerBackend = 250
	}
	if cfg.Weights == nil {
		cfg.Weights = make(map[int64]int)
	}
	return &cfg, nil
}

// SaveVPNConfig persists the VPN configuration to the settings table.
func (d *DB) SaveVPNConfig(ctx context.Context, cfg *models.VPNConfig) error {
	if cfg == nil {
		return errors.New("vpn config is nil")
	}
	return d.SetSetting(ctx, "vpn_config", cfg)
}

// CreateVPNSession records an active VPN session.
func (d *DB) CreateVPNSession(ctx context.Context, s *models.VPNSession) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	if s.ID == "" {
		uuidBytes := make([]byte, 16)
		_, _ = rand.Read(uuidBytes)
		uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x40
		uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80
		s.ID = fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			uuidBytes[0:4], uuidBytes[4:6], uuidBytes[6:8], uuidBytes[8:10], uuidBytes[10:16])
	}

	if s.Status == "" {
		s.Status = "connected"
	}
	if s.ConnectedAt.IsZero() {
		s.ConnectedAt = time.Now().UTC()
	}
	if s.LastSeen.IsZero() {
		s.LastSeen = time.Now().UTC()
	}
	connectedAtStr := formatTime(s.ConnectedAt)
	lastSeenStr := formatTime(s.LastSeen)

	query := `INSERT INTO vpn_sessions (
		id, user_id, backend_tunnel_id, peer_public_key, assigned_ip,
		connected_at, last_seen, rx_bytes, tx_bytes, status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(peer_public_key) DO UPDATE SET
		id = excluded.id,
		user_id = excluded.user_id,
		backend_tunnel_id = excluded.backend_tunnel_id,
		assigned_ip = excluded.assigned_ip,
		connected_at = excluded.connected_at,
		last_seen = excluded.last_seen,
		rx_bytes = excluded.rx_bytes,
		tx_bytes = excluded.tx_bytes,
		status = excluded.status`

	_, err := d.sqlDB.ExecContext(ctx, query,
		s.ID,
		s.UserID,
		s.BackendTunnelID,
		s.PeerPublicKey,
		s.AssignedIP,
		connectedAtStr,
		lastSeenStr,
		s.RxBytes,
		s.TxBytes,
		s.Status,
	)

	if err != nil {
		return fmt.Errorf("failed to insert/update vpn session: %w", err)
	}

	return nil
}

// UpdateVPNSessionTraffic updates session bytes in/out and last seen timestamp.
func (d *DB) UpdateVPNSessionTraffic(ctx context.Context, sessionID string, rx, tx int64) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	nowStr := time.Now().Format(time.RFC3339)
	query := `UPDATE vpn_sessions SET rx_bytes = ?, tx_bytes = ?, last_seen = ? WHERE id = ?`

	_, err := d.sqlDB.ExecContext(ctx, query, rx, tx, nowStr, sessionID)
	if err != nil {
		return fmt.Errorf("failed to update vpn session traffic %s: %w", sessionID, err)
	}
	return nil
}

// GetActiveVPNSessions retrieves all currently connected sessions.
func (d *DB) GetActiveVPNSessions(ctx context.Context) ([]models.VPNSession, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, user_id, backend_tunnel_id, peer_public_key, assigned_ip,
		connected_at, last_seen, rx_bytes, tx_bytes, status
		FROM vpn_sessions WHERE status = 'connected' ORDER BY connected_at DESC`

	rows, err := d.sqlDB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query active vpn sessions: %w", err)
	}
	defer rows.Close()

	var sessions []models.VPNSession
	for rows.Next() {
		s, err := d.scanVPNSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}

	return sessions, rows.Err()
}

// DeleteVPNSession removes a VPN session record.
func (d *DB) DeleteVPNSession(ctx context.Context, sessionID string) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	_, err := d.sqlDB.ExecContext(ctx, "DELETE FROM vpn_sessions WHERE id = ?", sessionID)
	if err != nil {
		return fmt.Errorf("failed to delete vpn session %s: %w", sessionID, err)
	}
	return nil
}

// Helper scanners

func (d *DB) scanBackendTunnel(s scannable) (models.BackendTunnel, error) {
	var t models.BackendTunnel
	var privKey, healthCheck, createdAt sql.NullString

	err := s.Scan(
		&t.ID,
		&t.ServerID,
		&t.InterfaceName,
		&t.PublicKey,
		&privKey,
		&t.Endpoint,
		&t.Status,
		&healthCheck,
		&t.LatencyMS,
		&t.ActiveConnections,
		&createdAt,
	)
	if err != nil {
		return t, err
	}

	if privKey.Valid && privKey.String != "" {
		t.PrivateKey = security.DecryptCredentialSafe(privKey.String, d.secretKey)
	}
	if healthCheck.Valid && healthCheck.String != "" {
		ht := parseTime(healthCheck.String)
		if !ht.IsZero() {
			t.LastHealthCheck = &ht
		}
	}
	if createdAt.Valid && createdAt.String != "" {
		t.CreatedAt = parseTime(createdAt.String)
	}

	return t, nil
}

func (d *DB) scanBackendTunnelRow(row *sql.Row) (models.BackendTunnel, error) {
	return d.scanBackendTunnel(row)
}

func (d *DB) scanVPNSession(s scannable) (models.VPNSession, error) {
	var v models.VPNSession
	var connectedAt, lastSeen sql.NullString

	err := s.Scan(
		&v.ID,
		&v.UserID,
		&v.BackendTunnelID,
		&v.PeerPublicKey,
		&v.AssignedIP,
		&connectedAt,
		&lastSeen,
		&v.RxBytes,
		&v.TxBytes,
		&v.Status,
	)
	if err != nil {
		return v, err
	}

	if connectedAt.Valid && connectedAt.String != "" {
		v.ConnectedAt = parseTime(connectedAt.String)
	}
	if lastSeen.Valid && lastSeen.String != "" {
		v.LastSeen = parseTime(lastSeen.String)
	}

	return v, nil
}

func (d *DB) scanVPNSessionRow(row *sql.Row) (models.VPNSession, error) {
	return d.scanVPNSession(row)
}
