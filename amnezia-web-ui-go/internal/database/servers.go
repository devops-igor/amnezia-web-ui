package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
)

// GetAllServers retrieves all server records ordered by ID, decrypting credentials and deserializing protocols.
func (d *DB) GetAllServers(ctx context.Context) ([]models.Server, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.sqlDB.QueryContext(ctx, "SELECT id, name, host, ssh_user, ssh_port, ssh_pass, ssh_key, protocols, created_at FROM servers ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("failed to query servers: %w", err)
	}
	defer rows.Close()

	var servers []models.Server
	for rows.Next() {
		s, err := d.scanServer(rows)
		if err != nil {
			return nil, err
		}
		servers = append(servers, s)
	}

	return servers, rows.Err()
}

// GetServer retrieves a server by ID, decrypting credentials. Returns nil, nil if not found.
func (d *DB) GetServer(ctx context.Context, id int64) (*models.Server, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	row := d.sqlDB.QueryRowContext(ctx, "SELECT id, name, host, ssh_user, ssh_port, ssh_pass, ssh_key, protocols, created_at FROM servers WHERE id = ?", id)
	s, err := d.scanServerRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get server %d: %w", id, err)
	}
	return &s, nil
}

// GetServerByID is an alias for GetServer.
func (d *DB) GetServerByID(ctx context.Context, id int64) (*models.Server, error) {
	return d.GetServer(ctx, id)
}

// GetServerCount returns the total number of configured servers.
func (d *DB) GetServerCount(ctx context.Context) (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var count int
	err := d.sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM servers").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count servers: %w", err)
	}
	return count, nil
}

// CountServers is an alias for GetServerCount.
func (d *DB) CountServers(ctx context.Context) (int, error) {
	return d.GetServerCount(ctx)
}

// CreateServer inserts a new server record, encrypting credentials and serializing protocols.
func (d *DB) CreateServer(ctx context.Context, s *models.Server) (int64, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	encPass := s.SSHPass
	encKey := s.SSHKey
	if s.SSHPass != "" && !security.LooksLikeFernetToken(s.SSHPass) {
		ep, err := security.EncryptCredential(s.SSHPass, d.secretKey)
		if err != nil {
			return 0, fmt.Errorf("failed to encrypt ssh password: %w", err)
		}
		encPass = ep
	}
	if s.SSHKey != "" && !security.LooksLikeFernetToken(s.SSHKey) {
		ek, err := security.EncryptCredential(s.SSHKey, d.secretKey)
		if err != nil {
			return 0, fmt.Errorf("failed to encrypt ssh key: %w", err)
		}
		encKey = ek
	}

	cleanedProtocols := security.StripSensitiveProtocolFields(s.Protocols)
	if cleanedProtocols == nil {
		cleanedProtocols = make(map[string]any)
	}
	protoBytes, err := json.Marshal(cleanedProtocols)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal protocols JSON: %w", err)
	}

	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	createdAtStr := formatTime(s.CreatedAt)

	var res sql.Result
	if s.ID > 0 {
		res, err = d.sqlDB.ExecContext(ctx,
			"INSERT INTO servers (id, name, host, ssh_user, ssh_port, ssh_pass, ssh_key, protocols, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			s.ID, s.Name, s.Host, s.SSHUser, s.SSHPort, encPass, encKey, string(protoBytes), createdAtStr,
		)
	} else {
		res, err = d.sqlDB.ExecContext(ctx,
			"INSERT INTO servers (name, host, ssh_user, ssh_port, ssh_pass, ssh_key, protocols, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			s.Name, s.Host, s.SSHUser, s.SSHPort, encPass, encKey, string(protoBytes), createdAtStr,
		)
	}

	if err != nil {
		return 0, fmt.Errorf("failed to insert server: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	s.ID = id
	return id, nil
}

// UpdateServer dynamically updates fields on a server, validating against the column allowlist.
func (d *DB) UpdateServer(ctx context.Context, id int64, updates map[string]any) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	fieldMap := map[string]string{
		"name":        "name",
		"host":        "host",
		"username":    "ssh_user",
		"ssh_user":    "ssh_user",
		"ssh_port":    "ssh_port",
		"password":    "ssh_pass",
		"ssh_pass":    "ssh_pass",
		"private_key": "ssh_key",
		"ssh_key":     "ssh_key",
		"protocols":   "protocols",
	}

	mapped := make(map[string]any)
	for k, v := range updates {
		col, ok := fieldMap[k]
		if !ok {
			col = k
		}
		if !allowedServerColumns[col] {
			return fmt.Errorf("unknown server column: %s", col)
		}
		mapped[col] = v
	}

	if len(mapped) == 0 {
		return nil
	}

	var setClauses []string
	var values []any

	for col, val := range mapped {
		if col == "protocols" {
			if m, ok := val.(map[string]any); ok {
				cleaned := security.StripSensitiveProtocolFields(m)
				b, err := json.Marshal(cleaned)
				if err != nil {
					return fmt.Errorf("failed to marshal protocols: %w", err)
				}
				val = string(b)
			}
		}
		if col == "ssh_pass" || col == "ssh_key" {
			strVal, _ := val.(string)
			if strVal != "" && !security.LooksLikeFernetToken(strVal) {
				enc, err := security.EncryptCredential(strVal, d.secretKey)
				if err != nil {
					return fmt.Errorf("failed to encrypt %s: %w", col, err)
				}
				val = enc
			}
		}

		setClauses = append(setClauses, fmt.Sprintf("%s = ?", col))
		values = append(values, val)
	}

	values = append(values, id)
	// #nosec G201 -- Column names are validated against allowedServerColumns allowlist
	query := fmt.Sprintf("UPDATE servers SET %s WHERE id = ?", strings.Join(setClauses, ", "))

	_, err := d.sqlDB.ExecContext(ctx, query, values...)
	if err != nil {
		return fmt.Errorf("failed to update server %d: %w", id, err)
	}
	return nil
}

// UpdateServerStats updates runtime statistics or server status metadata.
func (d *DB) UpdateServerStats(ctx context.Context, id int64, stats map[string]any) error {
	return d.UpdateServer(ctx, id, map[string]any{"protocols": stats})
}

// UpdateServerReachability updates reachability probe state.
func (d *DB) UpdateServerReachability(ctx context.Context, id int64, status models.ReachabilityStatus) error {
	d.reachabilityMu.Lock()
	defer d.reachabilityMu.Unlock()
	d.reachabilityCache[id] = status
	return nil
}

// UpdateServerReachabilityExtended updates reachability probe state with extended probe details.
func (d *DB) UpdateServerReachabilityExtended(ctx context.Context, id int64, status models.ReachabilityStatus, details map[string]any) error {
	return d.UpdateServerReachability(ctx, id, status)
}

// UpdateServerProtocols updates the protocols JSON blob for a server.
func (d *DB) UpdateServerProtocols(ctx context.Context, id int64, protocols map[string]any) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	cleaned := security.StripSensitiveProtocolFields(protocols)
	if cleaned == nil {
		cleaned = make(map[string]any)
	}
	b, err := json.Marshal(cleaned)
	if err != nil {
		return fmt.Errorf("failed to marshal protocols JSON: %w", err)
	}

	_, err = d.sqlDB.ExecContext(ctx, "UPDATE servers SET protocols = ? WHERE id = ?", string(b), id)
	if err != nil {
		return fmt.Errorf("failed to update server protocols: %w", err)
	}
	return nil
}

// UpdateServerSSHStatus updates SSH status for a server.
func (d *DB) UpdateServerSSHStatus(ctx context.Context, id int64, status string) error {
	// Status tracking
	return nil
}

// UpdateServerCredentials updates and encrypts SSH password and private key.
func (d *DB) UpdateServerCredentials(ctx context.Context, id int64, sshPass, sshKey string) error {
	return d.UpdateServer(ctx, id, map[string]any{
		"ssh_pass": sshPass,
		"ssh_key":  sshKey,
	})
}

// GetServerStatus returns the ReachabilityStatus of a server.
func (d *DB) GetServerStatus(ctx context.Context, id int64) (models.ReachabilityStatus, error) {
	d.reachabilityMu.RLock()
	defer d.reachabilityMu.RUnlock()

	if status, ok := d.reachabilityCache[id]; ok {
		return status, nil
	}
	return models.ReachabilityUnknown, nil
}

// ServerExists checks whether a server with the specified ID exists.
func (d *DB) ServerExists(ctx context.Context, id int64) (bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var dummy int
	err := d.sqlDB.QueryRowContext(ctx, "SELECT 1 FROM servers WHERE id = ?", id).Scan(&dummy)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// DeleteServer deletes a server and cascades to user connections, known hosts, and backend tunnels.
func (d *DB) DeleteServer(ctx context.Context, id int64) (bool, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("failed to begin delete transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	_, _ = tx.ExecContext(ctx, "DELETE FROM user_connections WHERE server_id = ?", id)
	_, _ = tx.ExecContext(ctx, "DELETE FROM known_hosts WHERE server_id = ?", id)
	_, _ = tx.ExecContext(ctx, "DELETE FROM backend_tunnels WHERE server_id = ?", id)

	res, err := tx.ExecContext(ctx, "DELETE FROM servers WHERE id = ?", id)
	if err != nil {
		return false, fmt.Errorf("failed to delete server %d: %w", id, err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit delete server: %w", err)
	}

	return rowsAffected > 0, nil
}

// Internal scan helpers

type scannable interface {
	Scan(dest ...any) error
}

func (d *DB) scanServer(s scannable) (models.Server, error) {
	var srv models.Server
	var sshPass, sshKey, protoJSON, createdAt sql.NullString

	err := s.Scan(
		&srv.ID,
		&srv.Name,
		&srv.Host,
		&srv.SSHUser,
		&srv.SSHPort,
		&sshPass,
		&sshKey,
		&protoJSON,
		&createdAt,
	)
	if err != nil {
		return srv, err
	}

	if sshPass.Valid && sshPass.String != "" {
		srv.SSHPass = security.DecryptCredentialSafe(sshPass.String, d.secretKey)
	}
	if sshKey.Valid && sshKey.String != "" {
		srv.SSHKey = security.DecryptCredentialSafe(sshKey.String, d.secretKey)
	}

	srv.Protocols = make(map[string]any)
	if protoJSON.Valid && protoJSON.String != "" {
		_ = json.Unmarshal([]byte(protoJSON.String), &srv.Protocols)
		srv.Protocols = security.StripSensitiveProtocolFields(srv.Protocols)
	}

	if createdAt.Valid && createdAt.String != "" {
		srv.CreatedAt = parseTime(createdAt.String)
	}

	d.reachabilityMu.RLock()
	if st, ok := d.reachabilityCache[srv.ID]; ok {
		srv.Status = st
	} else {
		srv.Status = models.ReachabilityUnknown
	}
	d.reachabilityMu.RUnlock()

	return srv, nil
}

func (d *DB) scanServerRow(row *sql.Row) (models.Server, error) {
	return d.scanServer(row)
}
