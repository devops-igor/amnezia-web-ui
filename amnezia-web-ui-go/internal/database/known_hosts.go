package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// GetKnownHost retrieves a known host record for a server ID. Returns nil, nil if not found.
func (d *DB) GetKnownHost(ctx context.Context, serverID int64) (*models.KnownHost, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var kh models.KnownHost
	var firstSeenStr sql.NullString
	err := d.sqlDB.QueryRowContext(ctx, "SELECT server_id, fingerprint, first_seen FROM known_hosts WHERE server_id = ?", serverID).
		Scan(&kh.ServerID, &kh.Fingerprint, &firstSeenStr)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get known host for server %d: %w", serverID, err)
	}

	if firstSeenStr.Valid && firstSeenStr.String != "" {
		kh.FirstSeen = parseTime(firstSeenStr.String)
	}

	return &kh, nil
}

// GetKnownHostFingerprint retrieves the stored SSH host fingerprint for a server. Returns "" if not found.
func (d *DB) GetKnownHostFingerprint(ctx context.Context, serverID int64) (string, error) {
	kh, err := d.GetKnownHost(ctx, serverID)
	if err != nil {
		return "", err
	}
	if kh == nil {
		return "", nil
	}
	return kh.Fingerprint, nil
}

// SaveKnownHost stores or updates the verified SSH fingerprint for a server.
func (d *DB) SaveKnownHost(ctx context.Context, serverID int64, fingerprint string) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	query := `INSERT INTO known_hosts (server_id, fingerprint)
		VALUES (?, ?)
		ON CONFLICT(server_id) DO UPDATE SET fingerprint = excluded.fingerprint`

	_, err := d.sqlDB.ExecContext(ctx, query, serverID, fingerprint)
	if err != nil {
		return fmt.Errorf("failed to save known host fingerprint for server %d: %w", serverID, err)
	}
	return nil
}

// SaveKnownHostFingerprint is an alias for SaveKnownHost.
func (d *DB) SaveKnownHostFingerprint(ctx context.Context, serverID int64, fp string) error {
	return d.SaveKnownHost(ctx, serverID, fp)
}

// DeleteKnownHost removes a known host fingerprint.
func (d *DB) DeleteKnownHost(ctx context.Context, serverID int64) (bool, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	res, err := d.sqlDB.ExecContext(ctx, "DELETE FROM known_hosts WHERE server_id = ?", serverID)
	if err != nil {
		return false, fmt.Errorf("failed to delete known host for server %d: %w", serverID, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// GetAllKnownHosts retrieves all stored SSH host key fingerprints.
func (d *DB) GetAllKnownHosts(ctx context.Context) ([]models.KnownHost, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.sqlDB.QueryContext(ctx, "SELECT server_id, fingerprint, first_seen FROM known_hosts ORDER BY server_id")
	if err != nil {
		return nil, fmt.Errorf("failed to query known hosts: %w", err)
	}
	defer rows.Close()

	var hosts []models.KnownHost
	for rows.Next() {
		var kh models.KnownHost
		var firstSeenStr sql.NullString
		if err := rows.Scan(&kh.ServerID, &kh.Fingerprint, &firstSeenStr); err != nil {
			return nil, err
		}
		if firstSeenStr.Valid && firstSeenStr.String != "" {
			kh.FirstSeen = parseTime(firstSeenStr.String)
		}
		hosts = append(hosts, kh)
	}

	return hosts, rows.Err()
}

// IsKnownHostVerified checks if a given fingerprint matches the stored host key using timing-safe comparison.
func (d *DB) IsKnownHostVerified(ctx context.Context, serverID int64, fingerprint string) (bool, error) {
	stored, err := d.GetKnownHostFingerprint(ctx, serverID)
	if err != nil {
		return false, err
	}
	if stored == "" {
		return false, nil
	}
	return constantTimeCompare(stored, fingerprint), nil
}
