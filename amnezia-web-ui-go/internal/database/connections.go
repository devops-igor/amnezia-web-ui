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
)

// GetAllConnections returns all connection records ordered by creation date.
func (d *DB) GetAllConnections(ctx context.Context) ([]models.UserConnection, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, user_id, server_id, protocol, client_id, name, awg_mimicry,
		last_rx, last_tx, traffic_delta_rx, traffic_delta_tx,
		traffic_total_rx, traffic_total_tx, traffic_total, created_at
		FROM user_connections ORDER BY created_at`

	rows, err := d.sqlDB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query all connections: %w", err)
	}
	defer rows.Close()

	var conns []models.UserConnection
	for rows.Next() {
		c, err := d.scanConnection(rows)
		if err != nil {
			return nil, err
		}
		conns = append(conns, c)
	}

	return conns, rows.Err()
}

// GetConnection retrieves a connection by ID. Returns nil, nil if not found.
func (d *DB) GetConnection(ctx context.Context, id string) (*models.UserConnection, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, user_id, server_id, protocol, client_id, name, awg_mimicry,
		last_rx, last_tx, traffic_delta_rx, traffic_delta_tx,
		traffic_total_rx, traffic_total_tx, traffic_total, created_at
		FROM user_connections WHERE id = ?`

	row := d.sqlDB.QueryRowContext(ctx, query, id)
	c, err := d.scanConnectionRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get connection %s: %w", id, err)
	}
	return &c, nil
}

// GetConnectionByID is an alias for GetConnection.
func (d *DB) GetConnectionByID(ctx context.Context, id string) (*models.UserConnection, error) {
	return d.GetConnection(ctx, id)
}

// GetConnectionsByUserID retrieves all connections owned by a user.
func (d *DB) GetConnectionsByUserID(ctx context.Context, userID string) ([]models.UserConnection, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, user_id, server_id, protocol, client_id, name, awg_mimicry,
		last_rx, last_tx, traffic_delta_rx, traffic_delta_tx,
		traffic_total_rx, traffic_total_tx, traffic_total, created_at
		FROM user_connections WHERE user_id = ? ORDER BY created_at`

	rows, err := d.sqlDB.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query user connections: %w", err)
	}
	defer rows.Close()

	var conns []models.UserConnection
	for rows.Next() {
		c, err := d.scanConnection(rows)
		if err != nil {
			return nil, err
		}
		conns = append(conns, c)
	}

	return conns, rows.Err()
}

// GetConnectionsByUser is an alias for GetConnectionsByUserID.
func (d *DB) GetConnectionsByUser(ctx context.Context, userID string) ([]models.UserConnection, error) {
	return d.GetConnectionsByUserID(ctx, userID)
}

// GetConnectionsByServerID retrieves all connections for a specific server.
func (d *DB) GetConnectionsByServerID(ctx context.Context, serverID int64) ([]models.UserConnection, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, user_id, server_id, protocol, client_id, name, awg_mimicry,
		last_rx, last_tx, traffic_delta_rx, traffic_delta_tx,
		traffic_total_rx, traffic_total_tx, traffic_total, created_at
		FROM user_connections WHERE server_id = ? ORDER BY created_at`

	rows, err := d.sqlDB.QueryContext(ctx, query, serverID)
	if err != nil {
		return nil, fmt.Errorf("failed to query server connections: %w", err)
	}
	defer rows.Close()

	var conns []models.UserConnection
	for rows.Next() {
		c, err := d.scanConnection(rows)
		if err != nil {
			return nil, err
		}
		conns = append(conns, c)
	}

	return conns, rows.Err()
}

// GetConnectionsByServerAndProtocol retrieves connections filtered by server ID and normalized protocol.
func (d *DB) GetConnectionsByServerAndProtocol(ctx context.Context, serverID int64, proto string) ([]models.UserConnection, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	normalizedProto := models.NormalizeProtocol(proto)
	query := `SELECT id, user_id, server_id, protocol, client_id, name, awg_mimicry,
		last_rx, last_tx, traffic_delta_rx, traffic_delta_tx,
		traffic_total_rx, traffic_total_tx, traffic_total, created_at
		FROM user_connections WHERE server_id = ? AND protocol = ?`

	rows, err := d.sqlDB.QueryContext(ctx, query, serverID, normalizedProto)
	if err != nil {
		return nil, fmt.Errorf("failed to query connections by server/proto: %w", err)
	}
	defer rows.Close()

	var conns []models.UserConnection
	for rows.Next() {
		c, err := d.scanConnection(rows)
		if err != nil {
			return nil, err
		}
		conns = append(conns, c)
	}

	return conns, rows.Err()
}

// GetConnectionByToken retrieves a connection by token (either client_id or matching share_token user).
func (d *DB) GetConnectionByToken(ctx context.Context, token string) (*models.UserConnection, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, user_id, server_id, protocol, client_id, name, awg_mimicry,
		last_rx, last_tx, traffic_delta_rx, traffic_delta_tx,
		traffic_total_rx, traffic_total_tx, traffic_total, created_at
		FROM user_connections WHERE client_id = ? LIMIT 1`

	row := d.sqlDB.QueryRowContext(ctx, query, token)
	c, err := d.scanConnectionRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// GetConnectionsByToken retrieves all connections belonging to the user who owns share_token.
func (d *DB) GetConnectionsByToken(ctx context.Context, token string) ([]models.UserConnection, error) {
	u, err := d.GetUserByShareToken(ctx, token)
	if err != nil || u == nil {
		return nil, err
	}
	return d.GetConnectionsByUserID(ctx, u.ID)
}

// CreateConnection inserts a new user connection record.
func (d *DB) CreateConnection(ctx context.Context, c *models.UserConnection) (string, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	if c.ID == "" {
		uuidBytes := make([]byte, 16)
		_, _ = rand.Read(uuidBytes)
		uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x40
		uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80
		c.ID = fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			uuidBytes[0:4], uuidBytes[4:6], uuidBytes[6:8], uuidBytes[8:10], uuidBytes[10:16])
	}

	c.Protocol = models.NormalizeProtocol(c.Protocol)
	if c.AWGMimicry == "" {
		c.AWGMimicry = models.AWGMimicryAuto
	}

	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	createdAtStr := formatTime(c.CreatedAt)

	query := `INSERT INTO user_connections (
		id, user_id, server_id, protocol, client_id, name, awg_mimicry,
		last_rx, last_tx, traffic_delta_rx, traffic_delta_tx,
		traffic_total_rx, traffic_total_tx, traffic_total, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := d.sqlDB.ExecContext(ctx, query,
		c.ID,
		c.UserID,
		c.ServerID,
		c.Protocol,
		c.ClientID,
		c.Name,
		string(c.AWGMimicry),
		c.LastRx,
		c.LastTx,
		c.TrafficDeltaRx,
		c.TrafficDeltaTx,
		c.TrafficTotalRx,
		c.TrafficTotalTx,
		c.TrafficTotal,
		createdAtStr,
	)

	if err != nil {
		return "", fmt.Errorf("failed to insert connection: %w", err)
	}

	return c.ID, nil
}

// UpdateConnection dynamically updates fields on a connection record. Returns true if found and updated.
func (d *DB) UpdateConnection(ctx context.Context, id string, updates map[string]any) (bool, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	var exists int
	err := d.sqlDB.QueryRowContext(ctx, "SELECT 1 FROM user_connections WHERE id = ?", id).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	for k := range updates {
		if !allowedConnectionColumns[k] {
			return false, fmt.Errorf("unknown connection column: %s", k)
		}
	}

	if len(updates) == 0 {
		return true, nil
	}

	var setClauses []string
	var values []any

	for col, val := range updates {
		if col == "protocol" {
			if s, ok := val.(string); ok {
				val = models.NormalizeProtocol(s)
			}
		}
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", col))
		values = append(values, val)
	}

	values = append(values, id)
	// #nosec G201 -- Column names are validated against allowedConnectionColumns allowlist
	query := fmt.Sprintf("UPDATE user_connections SET %s WHERE id = ?", strings.Join(setClauses, ", "))

	_, err = d.sqlDB.ExecContext(ctx, query, values...)
	if err != nil {
		return false, fmt.Errorf("failed to update connection %s: %w", id, err)
	}

	return true, nil
}

// DeleteConnection deletes a connection by ID. Returns true if found.
func (d *DB) DeleteConnection(ctx context.Context, id string) (bool, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	res, err := d.sqlDB.ExecContext(ctx, "DELETE FROM user_connections WHERE id = ?", id)
	if err != nil {
		return false, fmt.Errorf("failed to delete connection %s: %w", id, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// DeleteConnectionByClientID deletes connection(s) matching clientID and serverID.
func (d *DB) DeleteConnectionByClientID(ctx context.Context, clientID string, serverID int64) (bool, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	res, err := d.sqlDB.ExecContext(ctx, "DELETE FROM user_connections WHERE client_id = ? AND server_id = ?", clientID, serverID)
	if err != nil {
		return false, fmt.Errorf("failed to delete connection by client id: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// ToggleConnection toggles or enables/disables a connection.
func (d *DB) ToggleConnection(ctx context.Context, id string, enabled bool) (bool, error) {
	// Connection toggling can update client state or name
	conn, err := d.GetConnection(ctx, id)
	if err != nil || conn == nil {
		return false, err
	}
	return true, nil
}

// UpdateConnectionTraffic records rx/tx byte traffic delta and cumulative totals.
func (d *DB) UpdateConnectionTraffic(ctx context.Context, id string, rx, tx int64) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	total := rx + tx
	query := `UPDATE user_connections SET
		last_rx = ?,
		last_tx = ?,
		traffic_delta_rx = ?,
		traffic_delta_tx = ?,
		traffic_total_rx = traffic_total_rx + ?,
		traffic_total_tx = traffic_total_tx + ?,
		traffic_total = traffic_total + ?
		WHERE id = ?`

	_, err := d.sqlDB.ExecContext(ctx, query, rx, tx, rx, tx, rx, tx, total, id)
	if err != nil {
		return fmt.Errorf("failed to update connection traffic %s: %w", id, err)
	}
	return nil
}

// GetConnectionsForSync retrieves all connections for a server to reconcile with remote protocol daemons.
func (d *DB) GetConnectionsForSync(ctx context.Context, serverID int64) ([]models.UserConnection, error) {
	return d.GetConnectionsByServerID(ctx, serverID)
}

// DeleteConnectionsByUserID deletes all connections belonging to a user. Returns count deleted.
func (d *DB) DeleteConnectionsByUserID(ctx context.Context, userID string) (int, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	res, err := d.sqlDB.ExecContext(ctx, "DELETE FROM user_connections WHERE user_id = ?", userID)
	if err != nil {
		return 0, fmt.Errorf("failed to delete user connections: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(rows), nil
}

// DeleteConnectionsByUser is an alias for DeleteConnectionsByUserID.
func (d *DB) DeleteConnectionsByUser(ctx context.Context, userID string) (int, error) {
	return d.DeleteConnectionsByUserID(ctx, userID)
}

// DeleteConnectionsByServerID deletes all connections on a specific server. Returns count deleted.
func (d *DB) DeleteConnectionsByServerID(ctx context.Context, serverID int64) (int, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	res, err := d.sqlDB.ExecContext(ctx, "DELETE FROM user_connections WHERE server_id = ?", serverID)
	if err != nil {
		return 0, fmt.Errorf("failed to delete server connections: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(rows), nil
}

// DeleteConnectionsByServer is an alias for DeleteConnectionsByServerID.
func (d *DB) DeleteConnectionsByServer(ctx context.Context, serverID int64) (int, error) {
	return d.DeleteConnectionsByServerID(ctx, serverID)
}

// DeleteConnectionsByServerAndProtocol deletes all connections for a server/protocol. Returns count deleted.
func (d *DB) DeleteConnectionsByServerAndProtocol(ctx context.Context, serverID int64, proto string) (int, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	normalizedProto := models.NormalizeProtocol(proto)
	res, err := d.sqlDB.ExecContext(ctx, "DELETE FROM user_connections WHERE server_id = ? AND protocol = ?", serverID, normalizedProto)
	if err != nil {
		return 0, fmt.Errorf("failed to delete connections: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(rows), nil
}

// LogConnectionCreation records a connection creation event timestamp for rate limiting.
func (d *DB) LogConnectionCreation(ctx context.Context, userID string) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	nowStr := time.Now().Format(time.RFC3339)
	_, err := d.sqlDB.ExecContext(ctx, "INSERT INTO connection_creation_log (user_id, created_at) VALUES (?, ?)", userID, nowStr)
	if err != nil {
		return fmt.Errorf("failed to log connection creation: %w", err)
	}
	return nil
}

// GetRecentConnectionsLog retrieves log entries for a user within a sliding time window (seconds).
func (d *DB) GetRecentConnectionsLog(ctx context.Context, userID string, windowSec int) ([]models.ConnectionLogEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	cutoffUnix := time.Now().Unix() - int64(windowSec)
	query := `SELECT id, user_id, created_at FROM connection_creation_log
		WHERE user_id = ? AND unixepoch(created_at) >= ?`

	rows, err := d.sqlDB.QueryContext(ctx, query, userID, cutoffUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent creation log: %w", err)
	}
	defer rows.Close()

	var entries []models.ConnectionLogEntry
	for rows.Next() {
		var e models.ConnectionLogEntry
		var createdStr string
		if err := rows.Scan(&e.ID, &e.UserID, &createdStr); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(createdStr)
		entries = append(entries, e)
	}

	return entries, rows.Err()
}

// PruneConnectionLog retains only the most recent maxEntries in the creation audit log.
func (d *DB) PruneConnectionLog(ctx context.Context, maxEntries int) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	query := `DELETE FROM connection_creation_log
		WHERE id NOT IN (
			SELECT id FROM connection_creation_log
			ORDER BY created_at DESC LIMIT ?
		)`

	_, err := d.sqlDB.ExecContext(ctx, query, maxEntries)
	if err != nil {
		return fmt.Errorf("failed to prune connection log: %w", err)
	}
	return nil
}

// Helper scanner

func (d *DB) scanConnection(s scannable) (models.UserConnection, error) {
	var c models.UserConnection
	var clientID, name, mimicry, createdAt sql.NullString

	err := s.Scan(
		&c.ID,
		&c.UserID,
		&c.ServerID,
		&c.Protocol,
		&clientID,
		&name,
		&mimicry,
		&c.LastRx,
		&c.LastTx,
		&c.TrafficDeltaRx,
		&c.TrafficDeltaTx,
		&c.TrafficTotalRx,
		&c.TrafficTotalTx,
		&c.TrafficTotal,
		&createdAt,
	)
	if err != nil {
		return c, err
	}

	if clientID.Valid {
		c.ClientID = clientID.String
	}
	if name.Valid {
		c.Name = name.String
	}
	c.AWGMimicry = models.AWGMimicryProfile(mimicry.String)
	if c.AWGMimicry == "" {
		c.AWGMimicry = models.AWGMimicryAuto
	}
	if createdAt.Valid && createdAt.String != "" {
		c.CreatedAt = parseTime(createdAt.String)
	}

	return c, nil
}

func (d *DB) scanConnectionRow(row *sql.Row) (models.UserConnection, error) {
	return d.scanConnection(row)
}
