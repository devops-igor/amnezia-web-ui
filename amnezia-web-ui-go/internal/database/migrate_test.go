package database

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestMigrateHelpers(t *testing.T) {
	if v := getInt64(float64(42.0)); v != 42 {
		t.Errorf("getInt64(float64) = %d, want 42", v)
	}
	if v := getInt64(int64(100)); v != 100 {
		t.Errorf("getInt64(int64) = %d, want 100", v)
	}
	if v := getInt64(int(200)); v != 200 {
		t.Errorf("getInt64(int) = %d, want 200", v)
	}
	if v := getInt64("not-a-number"); v != 0 {
		t.Errorf("getInt64(string) = %d, want 0", v)
	}
	if v := getInt64(nil); v != 0 {
		t.Errorf("getInt64(nil) = %d, want 0", v)
	}

	if v := nullString(""); v != nil {
		t.Errorf("nullString(\"\") = %v, want nil", v)
	}
	if v := nullString("value"); v != "value" {
		t.Errorf("nullString(\"value\") = %v, want \"value\"", v)
	}
}

func TestMigrateValidateData(t *testing.T) {
	cases := []struct {
		name    string
		data    map[string]any
		wantErr bool
	}{
		{"server not dict", map[string]any{"servers": []any{"string"}}, true},
		{"server missing name", map[string]any{"servers": []any{map[string]any{"host": "1.1.1.1"}}}, true},
		{"server missing host", map[string]any{"servers": []any{map[string]any{"name": "s1"}}}, true},
		{"user not dict", map[string]any{"users": []any{123}}, true},
		{"user missing id", map[string]any{"users": []any{map[string]any{"username": "alice"}}}, true},
		{"user missing username", map[string]any{"users": []any{map[string]any{"id": "u-1"}}}, true},
		{"valid data", map[string]any{
			"servers": []any{map[string]any{"name": "s1", "host": "1.1.1.1"}},
			"users":   []any{map[string]any{"id": "u-1", "username": "alice"}},
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMigrationData(tc.data)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateMigrationData(%s) error = %v, wantErr = %v", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestMigrateFromDataJSONEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	secretKey := "migration-edge-secret-key-12345678"

	// Case 1: panel.db already exists
	existingDBPath := filepath.Join(tmpDir, "existing_panel.db")
	_ = os.WriteFile(existingDBPath, []byte("fake sqlite"), 0600)
	if err := MigrateFromDataJSON(filepath.Join(tmpDir, "non_existent_data.json"), existingDBPath, secretKey); err != nil {
		t.Errorf("expected nil when db exists, got: %v", err)
	}

	// Case 2: data.json does not exist
	nonExistentDBPath := filepath.Join(tmpDir, "fresh_panel.db")
	if err := MigrateFromDataJSON(filepath.Join(tmpDir, "does_not_exist.json"), nonExistentDBPath, secretKey); err != nil {
		t.Errorf("expected nil when data.json is missing, got: %v", err)
	}

	// Case 3: data.json is invalid JSON
	corruptDataPath := filepath.Join(tmpDir, "corrupt_data.json")
	_ = os.WriteFile(corruptDataPath, []byte("{invalid-json"), 0600)
	corruptDBPath := filepath.Join(tmpDir, "corrupt_panel.db")
	if err := MigrateFromDataJSON(corruptDataPath, corruptDBPath, secretKey); err == nil {
		t.Errorf("expected error when data.json has invalid JSON")
	}

	// Case 4: data.json has invalid structure
	invalidStructDataPath := filepath.Join(tmpDir, "invalid_struct.json")
	invalidStructData := map[string]any{
		"servers": []any{map[string]any{"bad_key": 1}},
	}
	b, _ := json.Marshal(invalidStructData)
	_ = os.WriteFile(invalidStructDataPath, b, 0600)
	invalidStructDBPath := filepath.Join(tmpDir, "invalid_struct_panel.db")
	if err := MigrateFromDataJSON(invalidStructDataPath, invalidStructDBPath, secretKey); err == nil {
		t.Errorf("expected error when data.json has invalid structure")
	}
}

func TestLoadDataAndSaveDataBasic(t *testing.T) {
	db, secretKey := setupTestDB(t)
	ctx := context.Background()

	s1ID, _ := db.CreateServer(ctx, &models.Server{
		Name:      "Full Server 1",
		Host:      "10.20.30.40",
		SSHUser:   "admin",
		SSHPort:   22,
		SSHPass:   "Pass1!",
		SSHKey:    "Key1!",
		Protocols: map[string]any{"awg": map[string]any{"port": 51820}},
	})

	uEmail := "alice@full.com"
	uTel := "999888777"
	uDesc := "Full Alice"
	uShareToken := "full-share-token"
	uSharePass := "$2b$12$hash"
	uRemna := "remna-full-uuid"
	uMonthReset := "2026-08-01T00:00:00Z"
	uLastReset := "2026-08-01T00:00:00Z"
	uExp := time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC)

	u1ID, _ := db.CreateUser(ctx, &models.User{
		Username:               "full_alice",
		Email:                  &uEmail,
		TelegramID:             &uTel,
		Description:            &uDesc,
		PasswordHash:           "$2b$12$fullhash",
		Role:                   models.RoleUser,
		Enabled:                true,
		TrafficLimit:           50000000,
		TrafficUsed:            10000000,
		TrafficTotal:           20000000,
		TrafficTotalRx:         8000000,
		TrafficTotalTx:         12000000,
		MonthlyRx:              5000000,
		MonthlyTx:              5000000,
		MonthlyResetAt:         &uMonthReset,
		TrafficResetStrategy:   models.ResetStrategyMonthly,
		ShareEnabled:           true,
		ShareToken:             &uShareToken,
		SharePasswordHash:      &uSharePass,
		RemnaWaveUUID:          &uRemna,
		CreatedAt:              time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		LastResetAt:            &uLastReset,
		ExpirationDate:         &uExp,
		ExpiresAt:              &uExp,
		AWGMimicry:             models.AWGMimicrySIP,
		PasswordChangeRequired: false,
		Limits:                 map[string]any{"max_conns": 5},
	})

	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		UserID:         u1ID,
		ServerID:       s1ID,
		Protocol:       "awg",
		ClientID:       "full-client-key",
		Name:           "Full Client AWG",
		AWGMimicry:     models.AWGMimicrySIP,
		LastRx:         100,
		LastTx:         200,
		TrafficDeltaRx: 50,
		TrafficDeltaTx: 50,
		TrafficTotalRx: 500,
		TrafficTotalTx: 500,
		TrafficTotal:   1000,
		CreatedAt:      time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	})

	_ = db.LogConnectionCreation(ctx, u1ID)
	_ = db.SaveKnownHost(ctx, s1ID, "SHA256:full_known_host_fingerprint")
	_, _ = db.SaveLeaderboardSnapshot(ctx, 2026, 8)
	_ = db.SetSetting(ctx, "custom_setting_full", "full_val")
	_ = db.SaveSSLSettings(ctx, &models.SSLSettings{
		Enabled:  true,
		Domain:   "full.ssl.com",
		CertText: "FULL_CERT",
		KeyText:  "FULL_KEY",
	})

	backup, err := db.LoadData(ctx)
	if err != nil {
		t.Fatalf("LoadData failed: %v", err)
	}

	tmpDir := t.TempDir()
	freshDB, err := Open(filepath.Join(tmpDir, "fresh_load_save.db"), secretKey)
	if err != nil {
		t.Fatalf("failed to open fresh DB: %v", err)
	}
	defer freshDB.Close()

	if err := freshDB.SaveData(ctx, backup); err != nil {
		t.Fatalf("SaveData into fresh DB failed: %v", err)
	}

	freshServers, _ := freshDB.GetAllServers(ctx)
	if len(freshServers) != 1 || freshServers[0].Name != "Full Server 1" || freshServers[0].SSHPass != "Pass1!" {
		t.Errorf("fresh server mismatch: %+v", freshServers)
	}
}

func TestSaveDataVariedRepresentations(t *testing.T) {
	db, secretKey := setupTestDB(t)
	ctx := context.Background()

	tmpDir := t.TempDir()
	freshDB, err := Open(filepath.Join(tmpDir, "fresh_varied.db"), secretKey)
	if err != nil {
		t.Fatalf("failed to open fresh DB: %v", err)
	}
	defer freshDB.Close()

	variedData := &models.BackupData{
		Servers: []map[string]any{
			{
				"name":       "Server No ID",
				"host":       "192.168.1.1",
				"ssh_user":   "user_ssh",
				"ssh_port":   float64(2222),
				"ssh_pass":   "PlainPassNoID",
				"ssh_key":    "PlainKeyNoID",
				"protocols":  map[string]any{"awg": map[string]any{"port": float64(51820)}},
				"created_at": "",
			},
		},
		Users: []map[string]any{
			{
				"id":                       "varied-u1",
				"username":                 "varied_user",
				"enabled":                  true,
				"share_enabled":            false,
				"traffic_limit":            float64(1000),
				"traffic_used":             float64(500),
				"traffic_total":            float64(500),
				"traffic_total_rx":         float64(200),
				"traffic_total_tx":         float64(300),
				"monthly_rx":               float64(200),
				"monthly_tx":               float64(300),
				"created_at":               "",
				"last_reset_at":            "",
				"expiration_date":          "",
				"expires_at":               "2028-01-01T00:00:00Z",
				"awg_mimicry":              "quic",
				"password_change_required": true,
				"limits":                   map[string]any{"rate_limit": 10},
			},
		},
		UserConnections: []map[string]any{
			{
				"id":               "varied-conn-1",
				"user_id":          "varied-u1",
				"server_id":        float64(1),
				"protocol":         "awg2",
				"client_id":        "varied-client-id",
				"name":             "Varied Client",
				"awg_mimicry":      "quic",
				"last_rx":          float64(10),
				"last_tx":          float64(20),
				"traffic_delta_rx": float64(5),
				"traffic_delta_tx": float64(5),
				"traffic_total_rx": float64(100),
				"traffic_total_tx": float64(200),
				"traffic_total":    float64(300),
				"created_at":       "",
			},
		},
		ConnectionCreationLog: []map[string]any{
			{"user_id": "varied-u1", "created_at": "2026-08-26T00:00:00Z"},
		},
		KnownHosts: []map[string]any{
			{"server_id": float64(1), "fingerprint": "SHA256:varied_known_host"},
		},
		LeaderboardSnapshots: []map[string]any{
			{
				"year":        float64(2026),
				"month":       float64(8),
				"username":    "varied_user",
				"rank":        float64(1),
				"download":    float64(300),
				"upload":      float64(200),
				"total":       float64(500),
				"snapshot_at": "",
			},
		},
		Settings: map[string]any{
			"appearance": map[string]any{"title": "Varied Panel"},
		},
	}

	if err := freshDB.SaveData(ctx, variedData); err != nil {
		t.Fatalf("SaveData with varied data failed: %v", err)
	}

	variedUsers, _ := freshDB.GetAllUsers(ctx)
	if len(variedUsers) != 1 || variedUsers[0].Username != "varied_user" {
		t.Errorf("varied user mismatch: %+v", variedUsers)
	}

	altData := &models.BackupData{
		Servers: []map[string]any{
			{
				"id":          10,
				"name":        "Alt Server",
				"host":        "192.168.2.1",
				"username":    "alt_user",
				"ssh_port":    22,
				"password":    "AltPass",
				"private_key": "AltKey",
			},
		},
		Users: []map[string]any{
			{
				"id":              "alt-u1",
				"username":        "alt_user_1",
				"expiration_date": "2029-01-01T00:00:00Z",
				"expires_at":      "",
			},
		},
		UserConnections: []map[string]any{
			{
				"id":        "alt-conn-1",
				"user_id":   "alt-u1",
				"server_id": 10,
				"protocol":  "wireguard",
			},
		},
		ConnectionCreationLog: []map[string]any{
			{"user_id": "alt-u1", "timestamp": "2026-08-26T01:00:00Z"},
		},
		Settings: nil,
	}

	if err := db.SaveData(ctx, altData); err != nil {
		t.Fatalf("SaveData with altData failed: %v", err)
	}
}

func TestSaveDataErrorAndRollback(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	badData := &models.BackupData{
		Settings: map[string]any{
			"bad_setting": make(chan int),
		},
	}
	if err := db.SaveData(ctx, badData); err == nil {
		t.Errorf("expected error when SaveData receives unmarshalable setting data")
	}
}
