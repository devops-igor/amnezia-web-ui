package database

import (
	"context"
	"fmt"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// SaveLeaderboardSnapshot captures and archives the current monthly leaderboard state.
func (d *DB) SaveLeaderboardSnapshot(ctx context.Context, year, month int) (int, error) {
	entries, err := d.GetLeaderboard(ctx, "monthly")
	if err != nil {
		return 0, fmt.Errorf("failed to fetch monthly leaderboard for snapshot: %w", err)
	}
	if len(entries) == 0 {
		return 0, nil
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin leaderboard snapshot tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	nowStr := time.Now().Format(time.RFC3339)
	saved := 0

	for _, entry := range entries {
		query := `INSERT OR REPLACE INTO leaderboard_snapshots
			(year, month, username, rank, download, upload, total, snapshot_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

		res, err := tx.ExecContext(ctx, query,
			year, month, entry.Username, entry.Rank, entry.Download, entry.Upload, entry.Total, nowStr,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to insert snapshot entry: %w", err)
		}
		rows, _ := res.RowsAffected()
		if rows > 0 {
			saved++
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit leaderboard snapshot: %w", err)
	}

	return saved, nil
}

// GetLeaderboardSnapshot retrieves a historical leaderboard snapshot for a specific year and month.
func (d *DB) GetLeaderboardSnapshot(ctx context.Context, year, month int) ([]models.LeaderboardEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT rank, username, download, upload, total
		FROM leaderboard_snapshots
		WHERE year = ? AND month = ?
		ORDER BY rank ASC`

	rows, err := d.sqlDB.QueryContext(ctx, query, year, month)
	if err != nil {
		return nil, fmt.Errorf("failed to query leaderboard snapshot: %w", err)
	}
	defer rows.Close()

	var entries []models.LeaderboardEntry
	for rows.Next() {
		var e models.LeaderboardEntry
		if err := rows.Scan(&e.Rank, &e.Username, &e.Download, &e.Upload, &e.Total); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}

	return entries, rows.Err()
}

// GetLeaderboardHistory retrieves recent snapshots up to limit rows.
func (d *DB) GetLeaderboardHistory(ctx context.Context, limit int) ([]models.LeaderboardSnapshot, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}

	query := `SELECT id, year, month, username, rank, download, upload, total, snapshot_at
		FROM leaderboard_snapshots
		ORDER BY year DESC, month DESC, rank ASC
		LIMIT ?`

	rows, err := d.sqlDB.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query leaderboard history: %w", err)
	}
	defer rows.Close()

	var snapshots []models.LeaderboardSnapshot
	for rows.Next() {
		var s models.LeaderboardSnapshot
		var snapshotAtStr string
		if err := rows.Scan(&s.ID, &s.Year, &s.Month, &s.Username, &s.Rank, &s.Download, &s.Upload, &s.Total, &snapshotAtStr); err != nil {
			return nil, err
		}
		s.SnapshotAt = parseTime(snapshotAtStr)
		snapshots = append(snapshots, s)
	}

	return snapshots, rows.Err()
}

// DeleteOldSnapshots removes leaderboard snapshots older than retainMonths.
func (d *DB) DeleteOldSnapshots(ctx context.Context, retainMonths int) (int, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	cutoff := time.Now().AddDate(0, -retainMonths, 0)
	cutoffYear := cutoff.Year()
	cutoffMonth := int(cutoff.Month())

	query := `DELETE FROM leaderboard_snapshots
		WHERE (year < ?) OR (year = ? AND month < ?)`

	res, err := d.sqlDB.ExecContext(ctx, query, cutoffYear, cutoffYear, cutoffMonth)
	if err != nil {
		return 0, fmt.Errorf("failed to prune old snapshots: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	return int(rows), nil
}
