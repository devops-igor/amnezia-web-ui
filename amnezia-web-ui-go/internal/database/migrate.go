package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
)

// LoadData dumps all database records into a unified BackupData structure matching legacy data.json.
func (d *DB) LoadData(ctx context.Context) (*models.BackupData, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	servers, err := d.loadServers(ctx)
	if err != nil {
		return nil, err
	}

	users, err := d.loadUsers(ctx)
	if err != nil {
		return nil, err
	}

	conns, err := d.loadConnections(ctx)
	if err != nil {
		return nil, err
	}

	creationLog, _ := d.loadCreationLog(ctx)
	knownHosts, _ := d.loadKnownHosts(ctx)
	snapshots, _ := d.loadLeaderboardSnapshots(ctx)
	settings, _ := d.GetAllSettings(ctx)

	return &models.BackupData{
		Servers:               servers,
		Users:                 users,
		UserConnections:       conns,
		ConnectionCreationLog: creationLog,
		KnownHosts:            knownHosts,
		LeaderboardSnapshots:  snapshots,
		Settings:              settings,
	}, nil
}

func (d *DB) loadServers(ctx context.Context) ([]map[string]any, error) {
	srvRows, err := d.sqlDB.QueryContext(ctx, "SELECT id, name, host, ssh_user, ssh_port, ssh_pass, ssh_key, protocols, created_at FROM servers ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("failed to load servers: %w", err)
	}
	defer srvRows.Close()

	var servers []map[string]any
	for srvRows.Next() {
		var id int64
		var name, host, user, pass, key, protoJSON, createdAt sql.NullString
		var port int
		if err := srvRows.Scan(&id, &name, &host, &user, &port, &pass, &key, &protoJSON, &createdAt); err != nil {
			return nil, err
		}
		var protoMap map[string]any
		if protoJSON.Valid && protoJSON.String != "" {
			_ = json.Unmarshal([]byte(protoJSON.String), &protoMap)
		}
		protoMap = security.StripSensitiveProtocolFields(protoMap)

		decPass := ""
		if pass.Valid && pass.String != "" {
			decPass = security.DecryptCredentialSafe(pass.String, d.secretKey)
		}
		decKey := ""
		if key.Valid && key.String != "" {
			decKey = security.DecryptCredentialSafe(key.String, d.secretKey)
		}

		sMap := map[string]any{
			"id":          id,
			"name":        name.String,
			"host":        host.String,
			"username":    user.String,
			"ssh_port":    port,
			"password":    decPass,
			"private_key": decKey,
			"protocols":   protoMap,
			"created_at":  createdAt.String,
		}
		servers = append(servers, sMap)
	}
	return servers, nil
}

func (d *DB) loadUsers(ctx context.Context) ([]map[string]any, error) {
	uRows, err := d.sqlDB.QueryContext(ctx, `SELECT id, username, email, telegramId, description, password_hash, role, enabled,
		traffic_limit, traffic_used, traffic_total, traffic_total_rx, traffic_total_tx,
		monthly_rx, monthly_tx, monthly_reset_at, traffic_reset_strategy,
		share_enabled, share_token, share_password_hash, remnawave_uuid,
		created_at, last_reset_at, expiration_date, expires_at, awg_mimicry, password_change_required, limits
		FROM users ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("failed to load users: %w", err)
	}
	defer uRows.Close()

	var users []map[string]any
	for uRows.Next() {
		u, err := d.scanUser(uRows)
		if err != nil {
			return nil, err
		}
		uMap := map[string]any{
			"id":                       u.ID,
			"username":                 u.Username,
			"email":                    u.Email,
			"telegramId":               u.TelegramID,
			"description":              u.Description,
			"password_hash":            u.PasswordHash,
			"role":                     string(u.Role),
			"enabled":                  u.Enabled,
			"traffic_limit":            u.TrafficLimit,
			"traffic_used":             u.TrafficUsed,
			"traffic_total":            u.TrafficTotal,
			"traffic_total_rx":         u.TrafficTotalRx,
			"traffic_total_tx":         u.TrafficTotalTx,
			"monthly_rx":               u.MonthlyRx,
			"monthly_tx":               u.MonthlyTx,
			"monthly_reset_at":         u.MonthlyResetAt,
			"traffic_reset_strategy":   string(u.TrafficResetStrategy),
			"share_enabled":            u.ShareEnabled,
			"share_token":              u.ShareToken,
			"share_password_hash":      u.SharePasswordHash,
			"remnawave_uuid":           u.RemnaWaveUUID,
			"created_at":               formatTime(u.CreatedAt),
			"last_reset_at":            u.LastResetAt,
			"expiration_date":          formatTimePtr(u.ExpirationDate),
			"expires_at":               formatTimePtr(u.ExpiresAt),
			"awg_mimicry":              string(u.AWGMimicry),
			"password_change_required": u.PasswordChangeRequired,
			"limits":                   u.Limits,
		}
		users = append(users, uMap)
	}
	return users, nil
}

func (d *DB) loadConnections(ctx context.Context) ([]map[string]any, error) {
	connRows, err := d.sqlDB.QueryContext(ctx, `SELECT id, user_id, server_id, protocol, client_id, name, awg_mimicry,
		last_rx, last_tx, traffic_delta_rx, traffic_delta_tx,
		traffic_total_rx, traffic_total_tx, traffic_total, created_at
		FROM user_connections ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("failed to load connections: %w", err)
	}
	defer connRows.Close()

	var conns []map[string]any
	for connRows.Next() {
		c, err := d.scanConnection(connRows)
		if err != nil {
			return nil, err
		}
		cMap := map[string]any{
			"id":               c.ID,
			"user_id":          c.UserID,
			"server_id":        c.ServerID,
			"protocol":         c.Protocol,
			"client_id":        c.ClientID,
			"name":             c.Name,
			"awg_mimicry":      string(c.AWGMimicry),
			"last_rx":          c.LastRx,
			"last_tx":          c.LastTx,
			"traffic_delta_rx": c.TrafficDeltaRx,
			"traffic_delta_tx": c.TrafficDeltaTx,
			"traffic_total_rx": c.TrafficTotalRx,
			"traffic_total_tx": c.TrafficTotalTx,
			"traffic_total":    c.TrafficTotal,
			"created_at":       formatTime(c.CreatedAt),
		}
		conns = append(conns, cMap)
	}
	return conns, nil
}

func (d *DB) loadCreationLog(ctx context.Context) ([]map[string]any, error) {
	logRows, err := d.sqlDB.QueryContext(ctx, "SELECT user_id, created_at FROM connection_creation_log ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer logRows.Close()

	var res []map[string]any
	for logRows.Next() {
		var uid, created string
		if err := logRows.Scan(&uid, &created); err == nil {
			res = append(res, map[string]any{
				"user_id":   uid,
				"timestamp": created,
			})
		}
	}
	return res, nil
}

func (d *DB) loadKnownHosts(ctx context.Context) ([]map[string]any, error) {
	khRows, err := d.sqlDB.QueryContext(ctx, "SELECT server_id, fingerprint FROM known_hosts ORDER BY server_id")
	if err != nil {
		return nil, err
	}
	defer khRows.Close()

	var res []map[string]any
	for khRows.Next() {
		var sid int64
		var fp string
		if err := khRows.Scan(&sid, &fp); err == nil {
			res = append(res, map[string]any{
				"server_id":   sid,
				"fingerprint": fp,
			})
		}
	}
	return res, nil
}

func (d *DB) loadLeaderboardSnapshots(ctx context.Context) ([]map[string]any, error) {
	snapRows, err := d.sqlDB.QueryContext(ctx, "SELECT year, month, username, rank, download, upload, total, snapshot_at FROM leaderboard_snapshots ORDER BY year, month, rank")
	if err != nil {
		return nil, err
	}
	defer snapRows.Close()

	var res []map[string]any
	for snapRows.Next() {
		var s models.LeaderboardSnapshot
		var snapshotAtStr string
		if err := snapRows.Scan(&s.Year, &s.Month, &s.Username, &s.Rank, &s.Download, &s.Upload, &s.Total, &snapshotAtStr); err == nil {
			res = append(res, map[string]any{
				"year":        s.Year,
				"month":       s.Month,
				"username":    s.Username,
				"rank":        s.Rank,
				"download":    s.Download,
				"upload":      s.Upload,
				"total":       s.Total,
				"snapshot_at": snapshotAtStr,
			})
		}
	}
	return res, nil
}

// SaveData restores and replaces the full database content from BackupData within a single transaction in correct FK order.
func (d *DB) SaveData(ctx context.Context, data *models.BackupData) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin SaveData transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// 1. Delete existing data in correct FK order
	clearQueries := []string{
		"DELETE FROM connection_creation_log",
		"DELETE FROM vpn_sessions",
		"DELETE FROM user_connections",
		"DELETE FROM known_hosts",
		"DELETE FROM backend_tunnels",
		"DELETE FROM leaderboard_snapshots",
		"DELETE FROM users",
		"DELETE FROM servers",
		"DELETE FROM settings",
	}
	for _, q := range clearQueries {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("failed to clear table: %w", err)
		}
	}

	if err := d.saveServers(ctx, tx, data.Servers); err != nil {
		return err
	}
	if err := d.saveUsers(ctx, tx, data.Users); err != nil {
		return err
	}
	if err := d.saveConnections(ctx, tx, data.UserConnections); err != nil {
		return err
	}
	if err := d.saveAuxiliaryTables(ctx, tx, data); err != nil {
		return err
	}
	if err := d.saveSettings(ctx, tx, data.Settings); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit SaveData transaction: %w", err)
	}

	slog.Info("SaveData: database successfully restored from JSON structure")
	return nil
}

func (d *DB) saveServers(ctx context.Context, tx *sql.Tx, servers []map[string]any) error {
	for _, srv := range servers {
		name, _ := srv["name"].(string)
		host, _ := srv["host"].(string)
		sshUser, _ := srv["username"].(string)
		if sshUser == "" {
			sshUser, _ = srv["ssh_user"].(string)
		}
		sshPort := 22
		if p, ok := srv["ssh_port"].(float64); ok && p > 0 {
			sshPort = int(p)
		} else if p, ok := srv["ssh_port"].(int); ok && p > 0 {
			sshPort = p
		}

		rawPass, _ := srv["password"].(string)
		if rawPass == "" {
			rawPass, _ = srv["ssh_pass"].(string)
		}
		rawKey, _ := srv["private_key"].(string)
		if rawKey == "" {
			rawKey, _ = srv["ssh_key"].(string)
		}

		encPass := rawPass
		if rawPass != "" && !security.LooksLikeFernetToken(rawPass) && d.secretKey != "" {
			if ep, err := security.EncryptCredential(rawPass, d.secretKey); err == nil {
				encPass = ep
			}
		}
		encKey := rawKey
		if rawKey != "" && !security.LooksLikeFernetToken(rawKey) && d.secretKey != "" {
			if ek, err := security.EncryptCredential(rawKey, d.secretKey); err == nil {
				encKey = ek
			}
		}

		protoMap, _ := srv["protocols"].(map[string]any)
		cleanedProto := security.StripSensitiveProtocolFields(protoMap)
		if cleanedProto == nil {
			cleanedProto = make(map[string]any)
		}
		protoBytes, _ := json.Marshal(cleanedProto)

		createdAtStr, _ := srv["created_at"].(string)
		if createdAtStr == "" {
			createdAtStr = time.Now().Format(time.RFC3339)
		}

		idVal, hasID := srv["id"]
		var id int64
		if hasID {
			id = getInt64(idVal)
		}

		var err error
		if id > 0 {
			_, err = tx.ExecContext(ctx,
				"INSERT INTO servers (id, name, host, ssh_user, ssh_port, ssh_pass, ssh_key, protocols, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
				id, name, host, sshUser, sshPort, encPass, encKey, string(protoBytes), createdAtStr,
			)
		} else {
			_, err = tx.ExecContext(ctx,
				"INSERT INTO servers (name, host, ssh_user, ssh_port, ssh_pass, ssh_key, protocols, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
				name, host, sshUser, sshPort, encPass, encKey, string(protoBytes), createdAtStr,
			)
		}
		if err != nil {
			return fmt.Errorf("failed to restore server: %w", err)
		}
	}
	return nil
}

func (d *DB) saveUsers(ctx context.Context, tx *sql.Tx, users []map[string]any) error {
	for _, u := range users {
		id, _ := u["id"].(string)
		username, _ := u["username"].(string)
		username = strings.ToLower(strings.TrimSpace(username))
		email, _ := u["email"].(string)
		telID, _ := u["telegramId"].(string)
		desc, _ := u["description"].(string)
		pwdHash, _ := u["password_hash"].(string)
		role, _ := u["role"].(string)
		if role == "" {
			role = "user"
		}

		enabledInt := 1
		if en, ok := u["enabled"].(bool); ok && !en {
			enabledInt = 0
		}

		trafficLimit := getInt64(u["traffic_limit"])
		trafficUsed := getInt64(u["traffic_used"])
		trafficTotal := getInt64(u["traffic_total"])
		trafficTotalRx := getInt64(u["traffic_total_rx"])
		trafficTotalTx := getInt64(u["traffic_total_tx"])
		monthlyRx := getInt64(u["monthly_rx"])
		monthlyTx := getInt64(u["monthly_tx"])

		monthlyResetAt, _ := u["monthly_reset_at"].(string)
		strategy, _ := u["traffic_reset_strategy"].(string)
		if strategy == "" {
			strategy = "never"
		}

		shareEnabledInt := 0
		if se, ok := u["share_enabled"].(bool); ok && se {
			shareEnabledInt = 1
		}
		shareToken, _ := u["share_token"].(string)
		sharePass, _ := u["share_password_hash"].(string)
		remnaUUID, _ := u["remnawave_uuid"].(string)

		createdAtStr, _ := u["created_at"].(string)
		if createdAtStr == "" {
			createdAtStr = time.Now().Format(time.RFC3339)
		}
		lastResetStr, _ := u["last_reset_at"].(string)
		if lastResetStr == "" {
			lastResetStr = createdAtStr
		}

		expDateStr, _ := u["expiration_date"].(string)
		expiresAtStr, _ := u["expires_at"].(string)
		if expDateStr == "" && expiresAtStr != "" {
			expDateStr = expiresAtStr
		} else if expiresAtStr == "" && expDateStr != "" {
			expiresAtStr = expDateStr
		}

		mimicry, _ := u["awg_mimicry"].(string)
		if mimicry == "" {
			mimicry = "auto"
		}

		pwdChangeInt := 0
		if pc, ok := u["password_change_required"].(bool); ok && pc {
			pwdChangeInt = 1
		}

		limitsJSON := "{}"
		if lim, ok := u["limits"].(map[string]any); ok {
			b, _ := json.Marshal(lim)
			limitsJSON = string(b)
		}

		query := `INSERT INTO users (
			id, username, email, telegramId, description, password_hash, role, enabled,
			traffic_limit, traffic_used, traffic_total, traffic_total_rx, traffic_total_tx,
			monthly_rx, monthly_tx, monthly_reset_at, traffic_reset_strategy,
			share_enabled, share_token, share_password_hash, remnawave_uuid,
			created_at, last_reset_at, expiration_date, expires_at, awg_mimicry, password_change_required, limits
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

		_, err := tx.ExecContext(ctx, query,
			id, username, nullString(email), nullString(telID), nullString(desc),
			pwdHash, role, enabledInt, trafficLimit, trafficUsed, trafficTotal,
			trafficTotalRx, trafficTotalTx, monthlyRx, monthlyTx, monthlyResetAt,
			strategy, shareEnabledInt, nullString(shareToken), nullString(sharePass),
			nullString(remnaUUID), createdAtStr, lastResetStr, nullString(expDateStr),
			nullString(expiresAtStr), mimicry, pwdChangeInt, limitsJSON,
		)
		if err != nil {
			return fmt.Errorf("failed to restore user: %w", err)
		}
	}
	return nil
}

func (d *DB) saveConnections(ctx context.Context, tx *sql.Tx, conns []map[string]any) error {
	for _, c := range conns {
		id, _ := c["id"].(string)
		userID, _ := c["user_id"].(string)
		serverID := getInt64(c["server_id"])
		protocol, _ := c["protocol"].(string)
		protocol = models.NormalizeProtocol(protocol)
		clientID, _ := c["client_id"].(string)
		name, _ := c["name"].(string)
		mimicry, _ := c["awg_mimicry"].(string)
		if mimicry == "" {
			mimicry = "auto"
		}

		lastRx := getInt64(c["last_rx"])
		lastTx := getInt64(c["last_tx"])
		deltaRx := getInt64(c["traffic_delta_rx"])
		deltaTx := getInt64(c["traffic_delta_tx"])
		totRx := getInt64(c["traffic_total_rx"])
		totTx := getInt64(c["traffic_total_tx"])
		tot := getInt64(c["traffic_total"])

		createdAtStr, _ := c["created_at"].(string)
		if createdAtStr == "" {
			createdAtStr = time.Now().Format(time.RFC3339)
		}

		query := `INSERT INTO user_connections (
			id, user_id, server_id, protocol, client_id, name, awg_mimicry,
			last_rx, last_tx, traffic_delta_rx, traffic_delta_tx,
			traffic_total_rx, traffic_total_tx, traffic_total, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

		_, err := tx.ExecContext(ctx, query,
			id, userID, serverID, protocol, clientID, name, mimicry,
			lastRx, lastTx, deltaRx, deltaTx, totRx, totTx, tot, createdAtStr,
		)
		if err != nil {
			return fmt.Errorf("failed to restore user connection: %w", err)
		}
	}
	return nil
}

func (d *DB) saveAuxiliaryTables(ctx context.Context, tx *sql.Tx, data *models.BackupData) error {
	for _, entry := range data.ConnectionCreationLog {
		uid, _ := entry["user_id"].(string)
		ts, _ := entry["timestamp"].(string)
		if ts == "" {
			ts, _ = entry["created_at"].(string)
		}
		if uid != "" {
			_, _ = tx.ExecContext(ctx, "INSERT INTO connection_creation_log (user_id, created_at) VALUES (?, ?)", uid, ts)
		}
	}

	for _, kh := range data.KnownHosts {
		sid := getInt64(kh["server_id"])
		fp, _ := kh["fingerprint"].(string)
		if sid > 0 && fp != "" {
			_, _ = tx.ExecContext(ctx, `INSERT INTO known_hosts (server_id, fingerprint) VALUES (?, ?)
				ON CONFLICT(server_id) DO UPDATE SET fingerprint = excluded.fingerprint`, sid, fp)
		}
	}

	for _, snap := range data.LeaderboardSnapshots {
		year := int(getInt64(snap["year"]))
		month := int(getInt64(snap["month"]))
		username, _ := snap["username"].(string)
		rank := int(getInt64(snap["rank"]))
		download := getInt64(snap["download"])
		upload := getInt64(snap["upload"])
		total := getInt64(snap["total"])
		snapAt, _ := snap["snapshot_at"].(string)
		if snapAt == "" {
			snapAt = time.Now().Format(time.RFC3339)
		}

		if year > 0 && month > 0 && username != "" {
			_, _ = tx.ExecContext(ctx, `INSERT OR REPLACE INTO leaderboard_snapshots
				(year, month, username, rank, download, upload, total, snapshot_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				year, month, username, rank, download, upload, total, snapAt,
			)
		}
	}
	return nil
}

func (d *DB) saveSettings(ctx context.Context, tx *sql.Tx, settingsMap map[string]any) error {
	if settingsMap == nil {
		settingsMap = make(map[string]any)
	}
	for k, defaultVal := range DefaultSettings {
		if _, exists := settingsMap[k]; !exists {
			var parsed any
			_ = json.Unmarshal([]byte(defaultVal), &parsed)
			settingsMap[k] = parsed
		}
	}

	for key, value := range settingsMap {
		valToStore := value
		if key == "ssl" {
			if sslMap, ok := value.(map[string]any); ok {
				sslCopy := make(map[string]any, len(sslMap))
				for k, v := range sslMap {
					sslCopy[k] = v
				}
				if kt, ok := sslCopy["key_text"].(string); ok && kt != "" && !security.LooksLikeFernetToken(kt) && d.secretKey != "" {
					if enc, err := security.EncryptCredential(kt, d.secretKey); err == nil {
						sslCopy["key_text"] = enc
					}
				}
				if ct, ok := sslCopy["cert_text"].(string); ok && ct != "" && !security.LooksLikeFernetToken(ct) && d.secretKey != "" {
					if enc, err := security.EncryptCredential(ct, d.secretKey); err == nil {
						sslCopy["cert_text"] = enc
					}
				}
				valToStore = sslCopy
			}
		}

		b, err := json.Marshal(valToStore)
		if err != nil {
			return fmt.Errorf("failed to marshal setting %s: %w", key, err)
		}

		_, err = tx.ExecContext(ctx,
			"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
			key, string(b),
		)
		if err != nil {
			return fmt.Errorf("failed to insert setting %s: %w", key, err)
		}
	}
	return nil
}

// MigrateFromDataJSON executes a first-boot migration if panel.db is missing/empty but data.json exists.
func MigrateFromDataJSON(dataFilePath, dbPath, secretKey string) error {
	cleanDataFile := filepath.Clean(dataFilePath)
	cleanDBPath := filepath.Clean(dbPath)

	// Case 1: panel.db already exists
	if _, err := os.Stat(cleanDBPath); err == nil {
		slog.Info("panel.db already exists, skipping data.json migration")
		return nil
	}

	// Case 2: data.json does not exist
	if _, err := os.Stat(cleanDataFile); errors.Is(err, os.ErrNotExist) {
		slog.Info("No data.json found; skipping migration for fresh install")
		return nil
	}

	slog.Info("data.json found without panel.db — executing one-time migration", "file", cleanDataFile)

	// #nosec G304 -- Trusted data directory migration
	dataBytes, err := os.ReadFile(cleanDataFile)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", cleanDataFile, err)
	}

	var rawMap map[string]any
	if err := json.Unmarshal(dataBytes, &rawMap); err != nil {
		return fmt.Errorf("failed to parse %s: %w", cleanDataFile, err)
	}

	// Validate data structure
	if err := validateMigrationData(rawMap); err != nil {
		return fmt.Errorf("invalid data.json structure: %w", err)
	}

	var backup models.BackupData
	if err := json.Unmarshal(dataBytes, &backup); err != nil {
		return fmt.Errorf("failed to unmarshal into BackupData: %w", err)
	}

	// Open or create SQLite DB
	db, err := Open(cleanDBPath, secretKey)
	if err != nil {
		_ = os.Remove(cleanDBPath)
		return fmt.Errorf("failed to initialize sqlite DB for migration: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.SaveData(ctx, &backup); err != nil {
		_ = db.Close()
		_ = os.Remove(cleanDBPath)
		return fmt.Errorf("migration SaveData failed: %w", err)
	}

	_ = db.SetSchemaVersion(ctx, 1)

	// Rename data.json -> data.json.bak only on success
	backupPath := cleanDataFile + ".bak"
	if err := os.Rename(cleanDataFile, backupPath); err != nil {
		slog.Warn("Failed to rename data.json to .bak", "err", err)
	} else {
		slog.Info("Migration complete. data.json renamed to data.json.bak", "bak", backupPath)
	}

	return nil
}

func validateMigrationData(data map[string]any) error {
	if srvs, ok := data["servers"].([]any); ok {
		for i, s := range srvs {
			sMap, ok := s.(map[string]any)
			if !ok {
				return fmt.Errorf("servers[%d] is not a dict", i)
			}
			if _, hasName := sMap["name"]; !hasName {
				return fmt.Errorf("servers[%d] missing 'name'", i)
			}
			if _, hasHost := sMap["host"]; !hasHost {
				return fmt.Errorf("servers[%d] missing 'host'", i)
			}
		}
	}

	if users, ok := data["users"].([]any); ok {
		for i, u := range users {
			uMap, ok := u.(map[string]any)
			if !ok {
				return fmt.Errorf("users[%d] is not a dict", i)
			}
			if _, hasID := uMap["id"]; !hasID {
				return fmt.Errorf("users[%d] missing 'id'", i)
			}
			if _, hasUsername := uMap["username"]; !hasUsername {
				return fmt.Errorf("users[%d] missing 'username'", i)
			}
		}
	}

	return nil
}

func getInt64(val any) int64 {
	switch v := val.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	default:
		return 0
	}
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
