package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// GetSettingsHandler returns panel configuration with sensitive credentials masked.
func (h *Handlers) GetSettingsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var appearance models.AppearanceSettings
	var syncCfg models.SyncSettings
	var captchaCfg models.CaptchaSettings
	var sslCfg models.SSLSettings
	var limitsCfg models.ConnectionLimits
	telegramCfg := make(map[string]any)

	_ = h.db.GetSetting(ctx, "appearance", &appearance)
	_ = h.db.GetSetting(ctx, "sync", &syncCfg)
	_ = h.db.GetSetting(ctx, "captcha", &captchaCfg)
	_ = h.db.GetSetting(ctx, "telegram", &telegramCfg)
	_ = h.db.GetSetting(ctx, "ssl", &sslCfg)
	_ = h.db.GetSetting(ctx, "limits", &limitsCfg)

	// Strip sensitive private keys & certificate text from API response
	sslCfg.KeyText = ""
	sslCfg.CertText = ""

	// Mask sync API key if configured
	if syncCfg.RemnawaveAPIKey != "" {
		syncCfg.RemnawaveAPIKey = "********"
	}

	// Mask telegram bot tokens if present
	if bt, ok := telegramCfg["bot_token"].(string); ok && bt != "" {
		telegramCfg["bot_token"] = "********"
	}
	if tok, ok := telegramCfg["token"].(string); ok && tok != "" {
		telegramCfg["token"] = "********"
	}

	h.JSON(w, http.StatusOK, map[string]any{
		"appearance": appearance,
		"sync":       syncCfg,
		"captcha":    captchaCfg,
		"telegram":   telegramCfg,
		"ssl":        sslCfg,
		"limits":     limitsCfg,
	})
}

func (h *Handlers) preserveSecretsOnSave(ctx context.Context, req *models.SaveSettingsRequest) {
	// 1. SSL: Preserve existing KeyText / CertText if incoming are empty or masked
	var existingSSL models.SSLSettings
	_ = h.db.GetSetting(ctx, "ssl", &existingSSL)
	if req.SSL.KeyText == "" || req.SSL.KeyText == "********" {
		req.SSL.KeyText = existingSSL.KeyText
	}
	if req.SSL.CertText == "" || req.SSL.CertText == "********" {
		req.SSL.CertText = existingSSL.CertText
	}

	// 2. Sync: Preserve existing RemnawaveAPIKey if incoming is empty or masked
	var existingSync models.SyncSettings
	_ = h.db.GetSetting(ctx, "sync", &existingSync)
	if req.Sync.RemnawaveAPIKey == "" || req.Sync.RemnawaveAPIKey == "********" {
		req.Sync.RemnawaveAPIKey = existingSync.RemnawaveAPIKey
	}

	// 3. Telegram: Preserve existing bot_token / token if incoming is empty or masked
	if req.Telegram != nil {
		existingTelegram := make(map[string]any)
		_ = h.db.GetSetting(ctx, "telegram", &existingTelegram)
		if bt, ok := req.Telegram["bot_token"].(string); !ok || bt == "" || bt == "********" {
			if oldBt, ok := existingTelegram["bot_token"].(string); ok && oldBt != "" {
				req.Telegram["bot_token"] = oldBt
			}
		}
		if tok, ok := req.Telegram["token"].(string); !ok || tok == "" || tok == "********" {
			if oldTok, ok := existingTelegram["token"].(string); ok && oldTok != "" {
				req.Telegram["token"] = oldTok
			}
		}
	}
}

func (h *Handlers) persistSettings(ctx context.Context, req *models.SaveSettingsRequest) error {
	if err := h.db.SetSetting(ctx, "appearance", req.Appearance); err != nil {
		return err
	}
	if err := h.db.SetSetting(ctx, "sync", req.Sync); err != nil {
		return err
	}
	if err := h.db.SetSetting(ctx, "captcha", req.Captcha); err != nil {
		return err
	}
	if req.Telegram != nil {
		if err := h.db.SetSetting(ctx, "telegram", req.Telegram); err != nil {
			return err
		}
	}
	if err := h.db.SetSetting(ctx, "ssl", req.SSL); err != nil {
		return err
	}
	return h.db.SetSetting(ctx, "limits", req.Limits)
}

// SaveSettingsHandler updates persistent settings sections in the database.
func (h *Handlers) SaveSettingsHandler(w http.ResponseWriter, r *http.Request) {
	var req models.SaveSettingsRequest
	if err := h.DecodeJSON(r, &req); err != nil {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Invalid request body")
		return
	}

	ctx := r.Context()
	h.preserveSecretsOnSave(ctx, &req)

	if err := h.persistSettings(ctx, &req); err != nil {
		h.JSONError(w, http.StatusInternalServerError, "database_error", "Failed to save settings: "+err.Error())
		return
	}

	h.audit(r, "settings.save", nil)
	h.JSONOK(w)
}

// SyncNowHandler triggers immediate synchronization with external RemnaWave instance.
// TODO(issue-380): Implement actual RemnaWave API sync integration in Phase 6 (external services).
// This is currently a stub that reports the count of RemnaWave-linked users pending sync.
func (h *Handlers) SyncNowHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pending := 0
	if h.db != nil {
		if users, err := h.db.GetAllUsers(ctx); err == nil {
			for _, u := range users {
				if u.RemnaWaveUUID != nil && *u.RemnaWaveUUID != "" {
					pending++
				}
			}
		}
	}

	h.audit(r, "settings.sync_now", map[string]any{"pending_users": pending})
	h.JSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"synced_users": 0,
		"message":      "RemnaWave sync is scheduled for Phase 6 integration; no users synced yet",
	})
}

// SyncDeleteHandler removes all users and connections synced from RemnaWave.
func (h *Handlers) SyncDeleteHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, err := h.db.GetAllUsers(ctx)
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to get users")
		return
	}

	deletedCount := 0
	for _, u := range users {
		if u.RemnaWaveUUID != nil && *u.RemnaWaveUUID != "" {
			if _, err := h.db.DeleteConnectionsByUser(ctx, u.ID); err != nil {
				h.JSONError(w, http.StatusInternalServerError, "database_error", "Failed to delete user connections")
				return
			}
			if _, err := h.db.DeleteUser(ctx, u.ID); err != nil {
				h.JSONError(w, http.StatusInternalServerError, "database_error", "Failed to delete user")
				return
			}
			deletedCount++
		}
	}

	h.audit(r, "settings.sync_delete", map[string]any{"deleted_users": deletedCount})
	h.JSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"deleted": deletedCount,
	})
}

// DownloadBackupHandler exports complete panel database state as a JSON backup.
func (h *Handlers) DownloadBackupHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	servers, _ := h.db.GetAllServers(ctx)
	users, _ := h.db.GetAllUsers(ctx)
	conns, _ := h.db.GetAllConnections(ctx)

	serverMaps := make([]map[string]any, len(servers))
	for i, s := range servers {
		serverMaps[i] = map[string]any{
			"id":         s.ID,
			"name":       s.Name,
			"host":       s.Host,
			"ssh_user":   s.SSHUser,
			"ssh_port":   s.SSHPort,
			"protocols":  s.Protocols,
			"created_at": s.CreatedAt,
		}
	}

	userMaps := make([]map[string]any, len(users))
	for i, u := range users {
		userMaps[i] = map[string]any{
			"id":                     u.ID,
			"username":               u.Username,
			"email":                  u.Email,
			"telegramId":             u.TelegramID,
			"description":            u.Description,
			"role":                   u.Role,
			"enabled":                u.Enabled,
			"traffic_limit":          u.TrafficLimit,
			"traffic_used":           u.TrafficUsed,
			"traffic_total":          u.TrafficTotal,
			"traffic_total_rx":       u.TrafficTotalRx,
			"traffic_total_tx":       u.TrafficTotalTx,
			"monthly_rx":             u.MonthlyRx,
			"monthly_tx":             u.MonthlyTx,
			"traffic_reset_strategy": u.TrafficResetStrategy,
			"share_enabled":          u.ShareEnabled,
			"share_token":            u.ShareToken,
			"remnawave_uuid":         u.RemnaWaveUUID,
			"created_at":             u.CreatedAt,
			"awg_mimicry":            u.AWGMimicry,
		}
	}

	connMaps := make([]map[string]any, len(conns))
	for i, c := range conns {
		connMaps[i] = map[string]any{
			"id":               c.ID,
			"user_id":          c.UserID,
			"server_id":        c.ServerID,
			"protocol":         c.Protocol,
			"client_id":        c.ClientID,
			"name":             c.Name,
			"awg_mimicry":      c.AWGMimicry,
			"traffic_total_rx": c.TrafficTotalRx,
			"traffic_total_tx": c.TrafficTotalTx,
			"traffic_total":    c.TrafficTotal,
			"created_at":       c.CreatedAt,
		}
	}

	settingsMap, _ := h.db.GetAllSettings(ctx)

	// Export SSH host-key fingerprints (TOFU) and leaderboard snapshots for parity
	// with the Python reference backup format.
	allServers, _ := h.db.GetAllServers(ctx)
	knownHosts := make([]map[string]any, 0, len(allServers))
	for _, s := range allServers {
		fp, err := h.db.GetKnownHostFingerprint(ctx, s.ID)
		if err != nil || fp == "" {
			continue
		}
		kh, err := h.db.GetKnownHost(ctx, s.ID)
		if err != nil || kh == nil {
			continue
		}
		entry := map[string]any{
			"server_id":   s.ID,
			"fingerprint": fp,
		}
		if !kh.FirstSeen.IsZero() {
			entry["first_seen"] = kh.FirstSeen
		}
		knownHosts = append(knownHosts, entry)
	}

	leaderboardSnapshots := make([]map[string]any, 0)
	entries, _ := h.db.GetLeaderboardSnapshot(ctx, time.Now().Year(), int(time.Now().Month()))
	for _, e := range entries {
		leaderboardSnapshots = append(leaderboardSnapshots, map[string]any{
			"username": e.Username,
			"total":    e.Total,
		})
	}

	backup := models.BackupData{
		Servers:               serverMaps,
		Users:                 userMaps,
		UserConnections:       connMaps,
		ConnectionCreationLog: make([]map[string]any, 0),
		KnownHosts:            knownHosts,
		LeaderboardSnapshots:  leaderboardSnapshots,
		Settings:              settingsMap,
	}

	// Marker matching the Python reference: credentials are intentionally excluded.
	w.Header().Set("X-Backup-Credentials-Excluded", "true")

	backupBytes, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		h.JSONError(w, http.StatusInternalServerError, "internal_error", "Failed to serialize backup")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\"amnezia_backup.json\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(backupBytes)
}

// RestoreBackupHandler imports and merges database records from a JSON backup.
func (h *Handlers) RestoreBackupHandler(w http.ResponseWriter, r *http.Request) {
	var bodyBytes []byte

	// Handle multipart upload or raw JSON
	if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		file, _, err := r.FormFile("file")
		if err != nil {
			file, _, err = r.FormFile("backup")
		}
		if err != nil {
			h.JSONError(w, http.StatusBadRequest, "validation_failed", "No file uploaded")
			return
		}
		defer file.Close()
		bodyBytes, _ = io.ReadAll(io.LimitReader(file, 52428800)) // 50MB max
	} else {
		bodyBytes, _ = io.ReadAll(io.LimitReader(r.Body, 52428800))
	}

	if len(bodyBytes) == 0 {
		h.JSONError(w, http.StatusBadRequest, "validation_failed", "Empty backup file")
		return
	}

	var backup models.BackupData
	if err := json.Unmarshal(bodyBytes, &backup); err != nil {
		h.JSONError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON format")
		return
	}

	ctx := r.Context()

	restored := h.restoreBackupData(ctx, &backup)

	h.audit(r, "settings.backup_restore", map[string]any{
		"servers":  restored["servers"],
		"users":    restored["users"],
		"conns":    restored["conns"],
		"settings": restored["settings"],
	})

	h.JSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"restored": restored,
	})
}

// restoreBackupData restores settings, servers, users, and connections from a backup,
// returning counts of restored records per entity type.
func (h *Handlers) restoreBackupData(ctx context.Context, backup *models.BackupData) map[string]int {
	restoredSettings := h.restoreBackupSettings(ctx, backup.Settings)
	restoredServers, serverIDMap := h.restoreBackupServers(ctx, backup.Servers)
	restoredUsers := h.restoreBackupUsers(ctx, backup.Users)
	restoredConns := h.restoreBackupConnections(ctx, backup.UserConnections, serverIDMap)
	h.restoreBackupKnownHosts(ctx, backup.KnownHosts)

	return map[string]int{
		"servers":  restoredServers,
		"users":    restoredUsers,
		"conns":    restoredConns,
		"settings": restoredSettings,
	}
}

var allowedSettingsKeys = map[string]bool{
	"appearance":     true,
	"sync":           true,
	"captcha":        true,
	"telegram":       true,
	"ssl":            true,
	"limits":         true,
	"vpn_config":     true,
	"schema_version": true,
}

func (h *Handlers) restoreBackupSettings(ctx context.Context, settings map[string]any) int {
	restored := 0
	for k, v := range settings {
		if !allowedSettingsKeys[k] {
			continue
		}
		if err := h.db.SetSetting(ctx, k, v); err == nil {
			restored++
		}
	}
	return restored
}

func (h *Handlers) restoreBackupServers(ctx context.Context, servers []map[string]any) (int, map[int64]int64) {
	restored := 0
	for _, sMap := range servers {
		s := &models.Server{
			Name:      strVal(sMap["name"]),
			Host:      strVal(sMap["host"]),
			SSHUser:   strVal(sMap["ssh_user"]),
			SSHPort:   intVal(sMap["ssh_port"]),
			SSHPass:   strVal(sMap["ssh_pass"]),
			SSHKey:    strVal(sMap["ssh_key"]),
			Protocols: mapFromInterface(sMap["protocols"]),
			CreatedAt: time.Now(),
		}
		if id := int64Val(sMap["id"]); id > 0 {
			s.ID = id
		}
		if s.Name != "" && s.Host != "" {
			if _, err := h.db.CreateServer(ctx, s); err == nil {
				restored++
			}
		}
	}

	serverIDMap := make(map[int64]int64)
	if len(servers) > 0 {
		allServers, _ := h.db.GetAllServers(ctx)
		for _, s := range allServers {
			serverIDMap[s.ID] = s.ID
		}
		for _, sMap := range servers {
			oldID := int64Val(sMap["id"])
			if oldID == 0 {
				continue
			}
			for _, s := range allServers {
				if s.Host == strVal(sMap["host"]) && s.Name == strVal(sMap["name"]) {
					serverIDMap[oldID] = s.ID
					break
				}
			}
		}
	}
	return restored, serverIDMap
}

func (h *Handlers) restoreBackupUsers(ctx context.Context, users []map[string]any) int {
	restored := 0
	for _, uMap := range users {
		u := h.userFromBackupMap(uMap)
		if len(u.Username) > 0 {
			if _, err := h.db.CreateUser(ctx, u); err == nil {
				restored++
			}
		}
	}
	return restored
}

func (h *Handlers) restoreBackupConnections(ctx context.Context, connections []map[string]any, serverIDMap map[int64]int64) int {
	restored := 0
	for _, cMap := range connections {
		oldServerID := int64Val(cMap["server_id"])
		newServerID := serverIDMap[oldServerID]
		if newServerID == 0 {
			newServerID = oldServerID
		}

		c := &models.UserConnection{
			ID:             strVal(cMap["id"]),
			UserID:         strVal(cMap["user_id"]),
			ServerID:       newServerID,
			Protocol:       strVal(cMap["protocol"]),
			ClientID:       strVal(cMap["client_id"]),
			Name:           strVal(cMap["name"]),
			AWGMimicry:     models.AWGMimicryProfile(strVal(cMap["awg_mimicry"])),
			TrafficTotalRx: int64Val(cMap["traffic_total_rx"]),
			TrafficTotalTx: int64Val(cMap["traffic_total_tx"]),
			TrafficTotal:   int64Val(cMap["traffic_total"]),
			CreatedAt:      time.Now(),
		}
		if created := strVal(cMap["created_at"]); created != "" {
			if t, err := time.Parse(time.RFC3339, created); err == nil {
				c.CreatedAt = t
			}
		}
		if c.UserID != "" && c.ServerID > 0 && c.Protocol != "" {
			if _, err := h.db.CreateConnection(ctx, c); err == nil {
				restored++
			}
		}
	}
	return restored
}

func (h *Handlers) restoreBackupKnownHosts(ctx context.Context, knownHosts []map[string]any) {
	for _, khMap := range knownHosts {
		serverID := int64Val(khMap["server_id"])
		fp := strVal(khMap["fingerprint"])
		if serverID > 0 && fp != "" {
			_ = h.db.SaveKnownHost(ctx, serverID, fp)
		}
	}
}

// userFromBackupMap converts a raw backup user map into a models.User.
func (h *Handlers) userFromBackupMap(uMap map[string]any) *models.User {
	u := &models.User{
		ID:                   strVal(uMap["id"]),
		Username:             strVal(uMap["username"]),
		PasswordHash:         strVal(uMap["password_hash"]),
		Role:                 models.UserRole(strVal(uMap["role"])),
		Enabled:              boolVal(uMap["enabled"]),
		TrafficLimit:         int64Val(uMap["traffic_limit"]),
		TrafficUsed:          int64Val(uMap["traffic_used"]),
		TrafficTotal:         int64Val(uMap["traffic_total"]),
		TrafficTotalRx:       int64Val(uMap["traffic_total_rx"]),
		TrafficTotalTx:       int64Val(uMap["traffic_total_tx"]),
		MonthlyRx:            int64Val(uMap["monthly_rx"]),
		MonthlyTx:            int64Val(uMap["monthly_tx"]),
		TrafficResetStrategy: models.TrafficResetStrategy(strVal(uMap["traffic_reset_strategy"])),
		ShareEnabled:         boolVal(uMap["share_enabled"]),
		AWGMimicry:           models.AWGMimicryProfile(strVal(uMap["awg_mimicry"])),
		CreatedAt:            time.Now(),
	}
	if email := strVal(uMap["email"]); email != "" {
		u.Email = &email
	}
	if tg := strVal(uMap["telegramId"]); tg != "" {
		u.TelegramID = &tg
	}
	if desc := strVal(uMap["description"]); desc != "" {
		u.Description = &desc
	}
	if shareToken := strVal(uMap["share_token"]); shareToken != "" {
		u.ShareToken = &shareToken
	}
	if rwUUID := strVal(uMap["remnawave_uuid"]); rwUUID != "" {
		u.RemnaWaveUUID = &rwUUID
	}
	if created := strVal(uMap["created_at"]); created != "" {
		if t, err := time.Parse(time.RFC3339, created); err == nil {
			u.CreatedAt = t
		}
	}
	return u
}

// Helper functions for type-safe extraction from backup maps.
func strVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func int64Val(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		if n, err := strconv.ParseInt(n, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func intVal(v any) int {
	return int(int64Val(v))
}

func boolVal(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case float64:
		return b != 0
	case string:
		return b == "true" || b == "1"
	}
	return false
}

func mapFromInterface(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return make(map[string]any)
}
