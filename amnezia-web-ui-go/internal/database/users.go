package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// GetAllUsers returns all users ordered by creation date descending.
func (d *DB) GetAllUsers(ctx context.Context) ([]models.User, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, username, email, telegramId, description, password_hash, role, enabled,
		traffic_limit, traffic_used, traffic_total, traffic_total_rx, traffic_total_tx,
		monthly_rx, monthly_tx, monthly_reset_at, traffic_reset_strategy,
		share_enabled, share_token, share_password_hash, remnawave_uuid,
		created_at, last_reset_at, expiration_date, expires_at, awg_mimicry, password_change_required, limits
		FROM users ORDER BY created_at DESC`

	rows, err := d.sqlDB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		u, err := d.scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}

	return users, rows.Err()
}

// GetUser retrieves a user by ID. Returns nil, nil if not found.
func (d *DB) GetUser(ctx context.Context, id string) (*models.User, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, username, email, telegramId, description, password_hash, role, enabled,
		traffic_limit, traffic_used, traffic_total, traffic_total_rx, traffic_total_tx,
		monthly_rx, monthly_tx, monthly_reset_at, traffic_reset_strategy,
		share_enabled, share_token, share_password_hash, remnawave_uuid,
		created_at, last_reset_at, expiration_date, expires_at, awg_mimicry, password_change_required, limits
		FROM users WHERE id = ?`

	row := d.sqlDB.QueryRowContext(ctx, query, id)
	u, err := d.scanUserRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user %s: %w", id, err)
	}
	return &u, nil
}

// GetUserByID is an alias for GetUser.
func (d *DB) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	return d.GetUser(ctx, id)
}

// GetUserByUsername retrieves a user by username (case-insensitive).
func (d *DB) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, username, email, telegramId, description, password_hash, role, enabled,
		traffic_limit, traffic_used, traffic_total, traffic_total_rx, traffic_total_tx,
		monthly_rx, monthly_tx, monthly_reset_at, traffic_reset_strategy,
		share_enabled, share_token, share_password_hash, remnawave_uuid,
		created_at, last_reset_at, expiration_date, expires_at, awg_mimicry, password_change_required, limits
		FROM users WHERE LOWER(username) = LOWER(?)`

	row := d.sqlDB.QueryRowContext(ctx, query, strings.TrimSpace(username))
	u, err := d.scanUserRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user by username %s: %w", username, err)
	}
	return &u, nil
}

// GetUserByShareToken retrieves a user by unique share token.
func (d *DB) GetUserByShareToken(ctx context.Context, token string) (*models.User, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, username, email, telegramId, description, password_hash, role, enabled,
		traffic_limit, traffic_used, traffic_total, traffic_total_rx, traffic_total_tx,
		monthly_rx, monthly_tx, monthly_reset_at, traffic_reset_strategy,
		share_enabled, share_token, share_password_hash, remnawave_uuid,
		created_at, last_reset_at, expiration_date, expires_at, awg_mimicry, password_change_required, limits
		FROM users WHERE share_token = ?`

	row := d.sqlDB.QueryRowContext(ctx, query, token)
	u, err := d.scanUserRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user by share token: %w", err)
	}
	return &u, nil
}

// GetUserByRemnaWaveUUID retrieves a user by their external RemnaWave UUID.
func (d *DB) GetUserByRemnaWaveUUID(ctx context.Context, uuid string) (*models.User, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, username, email, telegramId, description, password_hash, role, enabled,
		traffic_limit, traffic_used, traffic_total, traffic_total_rx, traffic_total_tx,
		monthly_rx, monthly_tx, monthly_reset_at, traffic_reset_strategy,
		share_enabled, share_token, share_password_hash, remnawave_uuid,
		created_at, last_reset_at, expiration_date, expires_at, awg_mimicry, password_change_required, limits
		FROM users WHERE remnawave_uuid = ?`

	row := d.sqlDB.QueryRowContext(ctx, query, uuid)
	u, err := d.scanUserRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user by remnawave uuid: %w", err)
	}
	return &u, nil
}

// CreateUser inserts a new user record.
func (d *DB) CreateUser(ctx context.Context, u *models.User) (string, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	if u.ID == "" {
		uuidBytes := make([]byte, 16)
		_, _ = rand.Read(uuidBytes)
		uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x40 // v4
		uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80 // RFC 4122
		u.ID = fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			uuidBytes[0:4], uuidBytes[4:6], uuidBytes[6:8], uuidBytes[8:10], uuidBytes[10:16])
	}

	if u.Role == "" {
		u.Role = models.RoleUser
	}
	if u.TrafficResetStrategy == "" {
		u.TrafficResetStrategy = models.ResetStrategyNever
	}
	if u.AWGMimicry == "" {
		u.AWGMimicry = models.AWGMimicryAuto
	}

	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	createdAtStr := formatTime(u.CreatedAt)
	var lastResetStr string
	if u.LastResetAt != nil && *u.LastResetAt != "" {
		lastResetStr = *u.LastResetAt
	} else {
		lastResetStr = createdAtStr
	}

	expDateStr := formatTimePtr(u.ExpirationDate)
	expiresAtStr := formatTimePtr(u.ExpiresAt)
	if expDateStr == nil && expiresAtStr != nil {
		expDateStr = expiresAtStr
	} else if expiresAtStr == nil && expDateStr != nil {
		expiresAtStr = expDateStr
	}

	var monthlyResetStr string
	if u.MonthlyResetAt != nil {
		monthlyResetStr = *u.MonthlyResetAt
	}

	limitsJSON := "{}"
	if u.Limits != nil {
		b, err := json.Marshal(u.Limits)
		if err == nil {
			limitsJSON = string(b)
		}
	}

	enabledInt := 1
	if !u.Enabled {
		enabledInt = 0
	}
	shareEnabledInt := 0
	if u.ShareEnabled {
		shareEnabledInt = 1
	}
	pwdChangeInt := 0
	if u.PasswordChangeRequired {
		pwdChangeInt = 1
	}

	query := `INSERT INTO users (
		id, username, email, telegramId, description, password_hash, role, enabled,
		traffic_limit, traffic_used, traffic_total, traffic_total_rx, traffic_total_tx,
		monthly_rx, monthly_tx, monthly_reset_at, traffic_reset_strategy,
		share_enabled, share_token, share_password_hash, remnawave_uuid,
		created_at, last_reset_at, expiration_date, expires_at, awg_mimicry, password_change_required, limits
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := d.sqlDB.ExecContext(ctx, query,
		u.ID,
		strings.ToLower(strings.TrimSpace(u.Username)),
		u.Email,
		u.TelegramID,
		u.Description,
		u.PasswordHash,
		string(u.Role),
		enabledInt,
		u.TrafficLimit,
		u.TrafficUsed,
		u.TrafficTotal,
		u.TrafficTotalRx,
		u.TrafficTotalTx,
		u.MonthlyRx,
		u.MonthlyTx,
		monthlyResetStr,
		string(u.TrafficResetStrategy),
		shareEnabledInt,
		u.ShareToken,
		u.SharePasswordHash,
		u.RemnaWaveUUID,
		createdAtStr,
		lastResetStr,
		expDateStr,
		expiresAtStr,
		string(u.AWGMimicry),
		pwdChangeInt,
		limitsJSON,
	)

	if err != nil {
		return "", fmt.Errorf("failed to insert user: %w", err)
	}

	return u.ID, nil
}

// UpdateUser dynamically updates fields on a user record. Returns true if user existed and was updated.
func (d *DB) UpdateUser(ctx context.Context, id string, updates map[string]any) (bool, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	var exists int
	err := d.sqlDB.QueryRowContext(ctx, "SELECT 1 FROM users WHERE id = ?", id).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	for k := range updates {
		if !allowedUserColumns[k] {
			return false, fmt.Errorf("unknown user column: %s", k)
		}
	}

	if len(updates) == 0 {
		return true, nil
	}

	var setClauses []string
	var values []any

	for col, val := range updates {
		if col == "id" {
			continue
		}
		if col == "limits" {
			if m, ok := val.(map[string]any); ok {
				b, err := json.Marshal(m)
				if err != nil {
					return false, fmt.Errorf("failed to marshal limits: %w", err)
				}
				val = string(b)
			}
		}
		if col == "enabled" || col == "share_enabled" || col == "password_change_required" {
			if b, ok := val.(bool); ok {
				if b {
					val = 1
				} else {
					val = 0
				}
			}
		}
		if col == "username" {
			if s, ok := val.(string); ok {
				val = strings.ToLower(strings.TrimSpace(s))
			}
		}
		if col == "expiration_date" || col == "expires_at" {
			if t, ok := val.(*time.Time); ok && t != nil {
				val = formatTime(*t)
			} else if t, ok := val.(time.Time); ok {
				val = formatTime(t)
			}
		}

		setClauses = append(setClauses, fmt.Sprintf("%s = ?", col))
		values = append(values, val)
	}

	if len(setClauses) == 0 {
		return true, nil
	}

	values = append(values, id)
	// #nosec G201 -- Column names are validated against allowedUserColumns allowlist
	query := fmt.Sprintf("UPDATE users SET %s WHERE id = ?", strings.Join(setClauses, ", "))

	_, err = d.sqlDB.ExecContext(ctx, query, values...)
	if err != nil {
		return false, fmt.Errorf("failed to update user %s: %w", id, err)
	}

	return true, nil
}

// DeleteUser deletes a user and all associated connections in a transaction.
func (d *DB) DeleteUser(ctx context.Context, id string) (bool, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("failed to begin delete user tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	_, _ = tx.ExecContext(ctx, "DELETE FROM user_connections WHERE user_id = ?", id)
	_, _ = tx.ExecContext(ctx, "DELETE FROM connection_creation_log WHERE user_id = ?", id)
	_, _ = tx.ExecContext(ctx, "DELETE FROM vpn_sessions WHERE user_id = ?", id)

	res, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	if err != nil {
		return false, fmt.Errorf("failed to delete user %s: %w", id, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit delete user: %w", err)
	}

	return rows > 0, nil
}

// ToggleUser toggles a user's enabled status.
func (d *DB) ToggleUser(ctx context.Context, id string, enabled bool) (bool, error) {
	val := 0
	if enabled {
		val = 1
	}
	return d.UpdateUser(ctx, id, map[string]any{"enabled": val == 1})
}

// UpdateUserTraffic increments user traffic totals and period counters.
func (d *DB) UpdateUserTraffic(ctx context.Context, id string, rxDelta, txDelta int64) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	totalDelta := rxDelta + txDelta
	query := `UPDATE users SET
		traffic_used = traffic_used + ?,
		traffic_total = traffic_total + ?,
		traffic_total_rx = traffic_total_rx + ?,
		traffic_total_tx = traffic_total_tx + ?,
		monthly_rx = monthly_rx + ?,
		monthly_tx = monthly_tx + ?
		WHERE id = ?`

	_, err := d.sqlDB.ExecContext(ctx, query, totalDelta, totalDelta, rxDelta, txDelta, rxDelta, txDelta, id)
	if err != nil {
		return fmt.Errorf("failed to update user traffic: %w", err)
	}
	return nil
}

// UpdateUserLimits updates per-user connection limits JSON.
func (d *DB) UpdateUserLimits(ctx context.Context, id string, limits map[string]any) error {
	_, err := d.UpdateUser(ctx, id, map[string]any{"limits": limits})
	return err
}

// UpdateUserExpiry updates user expiration timestamp.
func (d *DB) UpdateUserExpiry(ctx context.Context, id string, expiresAt *time.Time) error {
	var expStr *string
	if expiresAt != nil {
		s := formatTime(*expiresAt)
		expStr = &s
	}
	_, err := d.UpdateUser(ctx, id, map[string]any{
		"expires_at":      expStr,
		"expiration_date": expStr,
	})
	return err
}

// GetUsersOverQuota returns all enabled users that have exceeded their traffic limits.
func (d *DB) GetUsersOverQuota(ctx context.Context) ([]models.User, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	query := `SELECT id, username, email, telegramId, description, password_hash, role, enabled,
		traffic_limit, traffic_used, traffic_total, traffic_total_rx, traffic_total_tx,
		monthly_rx, monthly_tx, monthly_reset_at, traffic_reset_strategy,
		share_enabled, share_token, share_password_hash, remnawave_uuid,
		created_at, last_reset_at, expiration_date, expires_at, awg_mimicry, password_change_required, limits
		FROM users WHERE enabled = 1 AND traffic_limit > 0 AND traffic_used >= traffic_limit`

	rows, err := d.sqlDB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query users over quota: %w", err)
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		u, err := d.scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}

	return users, rows.Err()
}

// GetExpiredUsers returns all enabled users whose expiration timestamp is in the past.
func (d *DB) GetExpiredUsers(ctx context.Context) ([]models.User, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	nowStr := time.Now().Format(time.RFC3339)
	query := `SELECT id, username, email, telegramId, description, password_hash, role, enabled,
		traffic_limit, traffic_used, traffic_total, traffic_total_rx, traffic_total_tx,
		monthly_rx, monthly_tx, monthly_reset_at, traffic_reset_strategy,
		share_enabled, share_token, share_password_hash, remnawave_uuid,
		created_at, last_reset_at, expiration_date, expires_at, awg_mimicry, password_change_required, limits
		FROM users WHERE enabled = 1 AND (
			(expires_at IS NOT NULL AND expires_at != '' AND expires_at < ?) OR
			(expiration_date IS NOT NULL AND expiration_date != '' AND expiration_date < ?)
		)`

	rows, err := d.sqlDB.QueryContext(ctx, query, nowStr, nowStr)
	if err != nil {
		return nil, fmt.Errorf("failed to query expired users: %w", err)
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		u, err := d.scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}

	return users, rows.Err()
}

// CountUsers returns the total count of registered users.
func (d *DB) CountUsers(ctx context.Context) (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var count int
	err := d.sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}
	return count, nil
}

// ResetMonthlyTraffic resets period traffic counters for users with 'monthly' reset strategy.
func (d *DB) ResetMonthlyTraffic(ctx context.Context) (int, error) {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	nowStr := time.Now().Format(time.RFC3339)
	query := `UPDATE users SET
		monthly_rx = 0,
		monthly_tx = 0,
		traffic_used = 0,
		monthly_reset_at = ?,
		last_reset_at = ?
		WHERE traffic_reset_strategy = 'monthly'`

	res, err := d.sqlDB.ExecContext(ctx, query, nowStr, nowStr)
	if err != nil {
		return 0, fmt.Errorf("failed to reset monthly traffic: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	// Also reset connection traffic deltas
	_, _ = d.sqlDB.ExecContext(ctx, "UPDATE user_connections SET traffic_delta_rx = 0, traffic_delta_tx = 0")

	return int(rows), nil
}

// GetLeaderboard aggregates and returns ranked users based on traffic totals.
func (d *DB) GetLeaderboard(ctx context.Context, period string) ([]models.LeaderboardEntry, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var downloadCol, uploadCol string
	if period == "monthly" {
		downloadCol = "monthly_tx"
		uploadCol = "monthly_rx"
	} else {
		downloadCol = "traffic_total_tx"
		uploadCol = "traffic_total_rx"
	}

	// #nosec G201 -- Column names are strictly determined by period parameter
	query := fmt.Sprintf(`SELECT username, %s AS download, %s AS upload, (%s + %s) AS total
		FROM users
		WHERE enabled = 1 AND (%s + %s) > 0
		ORDER BY total DESC, LOWER(username) ASC`,
		downloadCol, uploadCol, downloadCol, uploadCol, downloadCol, uploadCol,
	)

	rows, err := d.sqlDB.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query leaderboard: %w", err)
	}
	defer rows.Close()

	var entries []models.LeaderboardEntry
	rank := 1
	for rows.Next() {
		var e models.LeaderboardEntry
		if err := rows.Scan(&e.Username, &e.Download, &e.Upload, &e.Total); err != nil {
			return nil, err
		}
		e.Rank = rank
		rank++
		entries = append(entries, e)
	}

	return entries, rows.Err()
}

// Helper scan functions

func (d *DB) scanUser(s scannable) (models.User, error) {
	var u models.User
	var email, telID, desc, shareToken, sharePass, remnaUUID sql.NullString
	var monthlyResetAt, lastResetAt, expDate, expiresAt sql.NullString
	var role, strategy, mimicry, limitsJSON, createdAt sql.NullString
	var enabled, shareEnabled, pwdChange int

	err := s.Scan(
		&u.ID,
		&u.Username,
		&email,
		&telID,
		&desc,
		&u.PasswordHash,
		&role,
		&enabled,
		&u.TrafficLimit,
		&u.TrafficUsed,
		&u.TrafficTotal,
		&u.TrafficTotalRx,
		&u.TrafficTotalTx,
		&u.MonthlyRx,
		&u.MonthlyTx,
		&monthlyResetAt,
		&strategy,
		&shareEnabled,
		&shareToken,
		&sharePass,
		&remnaUUID,
		&createdAt,
		&lastResetAt,
		&expDate,
		&expiresAt,
		&mimicry,
		&pwdChange,
		&limitsJSON,
	)
	if err != nil {
		return u, err
	}

	u.Email = nullStringToPtr(email)
	u.TelegramID = nullStringToPtr(telID)
	u.Description = nullStringToPtr(desc)
	u.ShareToken = nullStringToPtr(shareToken)
	u.SharePasswordHash = nullStringToPtr(sharePass)
	u.RemnaWaveUUID = nullStringToPtr(remnaUUID)
	u.MonthlyResetAt = nullStringToPtr(monthlyResetAt)
	u.LastResetAt = nullStringToPtr(lastResetAt)

	u.Role = models.UserRole(role.String)
	if u.Role == "" {
		u.Role = models.RoleUser
	}

	u.TrafficResetStrategy = models.TrafficResetStrategy(strategy.String)
	if u.TrafficResetStrategy == "" {
		u.TrafficResetStrategy = models.ResetStrategyNever
	}

	u.AWGMimicry = models.AWGMimicryProfile(mimicry.String)
	if u.AWGMimicry == "" {
		u.AWGMimicry = models.AWGMimicryAuto
	}

	u.Enabled = enabled != 0
	u.ShareEnabled = shareEnabled != 0
	u.PasswordChangeRequired = pwdChange != 0

	if createdAt.Valid && createdAt.String != "" {
		u.CreatedAt = parseTime(createdAt.String)
	}

	if expDate.Valid && expDate.String != "" {
		t := parseTime(expDate.String)
		if !t.IsZero() {
			u.ExpirationDate = &t
		}
	}
	if expiresAt.Valid && expiresAt.String != "" {
		t := parseTime(expiresAt.String)
		if !t.IsZero() {
			u.ExpiresAt = &t
		}
	}
	if u.ExpirationDate == nil && u.ExpiresAt != nil {
		u.ExpirationDate = u.ExpiresAt
	} else if u.ExpiresAt == nil && u.ExpirationDate != nil {
		u.ExpiresAt = u.ExpirationDate
	}

	u.Limits = make(map[string]any)
	if limitsJSON.Valid && limitsJSON.String != "" {
		_ = json.Unmarshal([]byte(limitsJSON.String), &u.Limits)
	}

	return u, nil
}

func (d *DB) scanUserRow(row *sql.Row) (models.User, error) {
	return d.scanUser(row)
}
