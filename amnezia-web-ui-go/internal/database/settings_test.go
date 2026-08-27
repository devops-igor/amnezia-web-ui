package database

import (
	"context"
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
)

func TestSettingsEmptyAndBasicTypes(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	var missingVal string
	if err := db.GetSetting(ctx, "non_existent_key", &missingVal); err != nil {
		t.Errorf("GetSetting(non_existent) error = %v, want nil", err)
	}

	_, _ = db.sqlDB.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES ('empty_val', '')")
	var targetEmpty string
	if err := db.GetSetting(ctx, "empty_val", &targetEmpty); err != nil {
		t.Errorf("GetSetting(empty_val) error = %v, want nil", err)
	}

	type customStruct struct {
		FieldA string `json:"field_a"`
		FieldB int    `json:"field_b"`
	}

	testCases := []struct {
		key   string
		value any
	}{
		{"str_key", "simple string value"},
		{"int_key", 12345},
		{"bool_key", true},
		{"struct_key", customStruct{FieldA: "hello", FieldB: 42}},
		{"map_key", map[string]any{"nested": "value", "count": 10}},
	}

	for _, tc := range testCases {
		if err := db.SetSetting(ctx, tc.key, tc.value); err != nil {
			t.Fatalf("SetSetting(%s) failed: %v", tc.key, err)
		}
	}

	if err := db.UpdateSetting(ctx, "str_key", "updated string value"); err != nil {
		t.Fatalf("UpdateSetting failed: %v", err)
	}
	var retrievedStr string
	_ = db.GetSetting(ctx, "str_key", &retrievedStr)
	if retrievedStr != "updated string value" {
		t.Errorf("UpdateSetting value mismatch: %q", retrievedStr)
	}
}

func TestSettingsSSLVariants(t *testing.T) {
	db, secretKey := setupTestDB(t)
	ctx := context.Background()

	ssl1 := &models.SSLSettings{
		Enabled:   true,
		Domain:    "ssl1.example.com",
		CertText:  "-----BEGIN CERT1-----\n...",
		KeyText:   "-----BEGIN KEY1-----\n...",
		PanelPort: 443,
	}
	if err := db.SaveSSLSettings(ctx, ssl1); err != nil {
		t.Fatalf("SaveSSLSettings failed: %v", err)
	}
	retrievedSSL1, err := db.GetSSLSettings(ctx)
	if err != nil || retrievedSSL1 == nil || retrievedSSL1.KeyText != ssl1.KeyText {
		t.Errorf("GetSSLSettings ssl1 mismatch: %+v, err=%v", retrievedSSL1, err)
	}

	ssl2 := models.SSLSettings{
		Enabled:   true,
		Domain:    "ssl2.example.com",
		CertText:  "-----BEGIN CERT2-----\n...",
		KeyText:   "-----BEGIN KEY2-----\n...",
		PanelPort: 8443,
	}
	if err := db.SetSetting(ctx, "ssl", ssl2); err != nil {
		t.Fatalf("SetSetting with models.SSLSettings failed: %v", err)
	}

	sslMap := map[string]any{
		"enabled":    true,
		"domain":     "ssl3.example.com",
		"cert_text":  "-----BEGIN CERT3-----\n...",
		"key_text":   "-----BEGIN KEY3-----\n...",
		"panel_port": 9443,
	}
	if err := db.SetSetting(ctx, "ssl", sslMap); err != nil {
		t.Fatalf("SetSetting with sslMap failed: %v", err)
	}

	encKey, _ := security.EncryptCredential("FERNET_KEY", secretKey)
	encCert, _ := security.EncryptCredential("FERNET_CERT", secretKey)
	sslPreEnc := &models.SSLSettings{
		Enabled:  true,
		Domain:   "ssl4.example.com",
		CertText: encCert,
		KeyText:  encKey,
	}
	if err := db.SaveSSLSettings(ctx, sslPreEnc); err != nil {
		t.Fatalf("SaveSSLSettings pre-encrypted failed: %v", err)
	}

	var nilSSL *models.SSLSettings
	if prepNil := db.prepareSSLSettingForStore(nilSSL); prepNil != nil {
		t.Errorf("prepareSSLSettingForStore(nil) = %v, want nil", prepNil)
	}
	if prepInt := db.prepareSSLSettingForStore(123); prepInt != 123 {
		t.Errorf("prepareSSLSettingForStore(123) = %v, want 123", prepInt)
	}
}

func TestSettingsBulkAndRawStrings(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	bulkData := map[string]any{
		"bulk_k1": "v1",
		"bulk_k2": map[string]any{"nested_key": 99},
		"ssl": map[string]any{
			"enabled":   true,
			"domain":    "bulk.example.com",
			"cert_text": "CERT_BULK_TEST",
			"key_text":  "KEY_BULK_TEST",
		},
	}
	if err := db.SaveAllSettings(ctx, bulkData); err != nil {
		t.Fatalf("SaveAllSettings failed: %v", err)
	}
	allSettings, err := db.GetAllSettings(ctx)
	if err != nil || allSettings["bulk_k1"] != "v1" {
		t.Fatalf("GetAllSettings failed: %+v, err=%v", allSettings, err)
	}

	_, _ = db.sqlDB.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES ('raw_plain_text', 'plain non json string')")
	_, _ = db.sqlDB.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES ('null_setting_key', NULL)")
	allSettings2, _ := db.GetAllSettings(ctx)
	if allSettings2["raw_plain_text"] != "plain non json string" || allSettings2["null_setting_key"] != nil {
		t.Errorf("raw/null settings mismatch: %+v", allSettings2)
	}

	if err := db.DeleteSetting(ctx, "bulk_k1"); err != nil {
		t.Fatalf("DeleteSetting failed: %v", err)
	}
}

func TestSettingsRemnaWaveAndFlags(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	remnaSettings, err := db.GetRemnaWaveSettings(ctx)
	if err != nil || remnaSettings == nil || remnaSettings.RemnawaveProtocol != "awg" {
		t.Fatalf("GetRemnaWaveSettings failed: %+v, err=%v", remnaSettings, err)
	}

	syncConfig := &models.SyncSettings{
		RemnawaveURL:         "https://remna.example.com",
		RemnawaveAPIKey:      "api-key-test-12345",
		RemnawaveSync:        true,
		RemnawaveSyncUsers:   true,
		RemnawaveCreateConns: true,
		RemnawaveServerID:    1,
		RemnawaveProtocol:    "xray",
	}
	_ = db.SetSetting(ctx, "sync", syncConfig)
	updatedRemna, _ := db.GetRemnaWaveSettings(ctx)
	if updatedRemna == nil || updatedRemna.RemnawaveURL != "https://remna.example.com" {
		t.Errorf("updated RemnaWave settings mismatch: %+v", updatedRemna)
	}

	flagVal, err := db.GetMigrationFlag(ctx, "non_existent_flag")
	if err != nil || flagVal != "" {
		t.Errorf("GetMigrationFlag(non_existent) = %q, want empty", flagVal)
	}

	if err := db.SetMigrationFlag(ctx, "test_flag_key", "custom_flag_val"); err != nil {
		t.Fatalf("SetMigrationFlag failed: %v", err)
	}
	flagRet, _ := db.GetMigrationFlag(ctx, "test_flag_key")
	if flagRet != "custom_flag_val" {
		t.Errorf("GetMigrationFlag = %q, want 'custom_flag_val'", flagRet)
	}

	_, _ = db.sqlDB.ExecContext(ctx, "INSERT INTO migration_flags (key, value) VALUES ('null_flag', NULL)")
	nullFlagRet, _ := db.GetMigrationFlag(ctx, "null_flag")
	if nullFlagRet != "" {
		t.Errorf("GetMigrationFlag(null_flag) = %q, want empty", nullFlagRet)
	}
}

func TestSettingsSchemaVersionAndMarshalErrors(t *testing.T) {
	db, _ := setupTestDB(t)
	ctx := context.Background()

	if err := db.SetSchemaVersion(ctx, 3); err != nil {
		t.Fatalf("SetSchemaVersion failed: %v", err)
	}
	v, err := db.GetSchemaVersion(ctx)
	if err != nil || v != 3 {
		t.Errorf("GetSchemaVersion = %d, want 3", v)
	}

	_, _ = db.sqlDB.ExecContext(ctx, "UPDATE settings SET value = '5' WHERE key = 'schema_version'")
	vPlain, _ := db.GetSchemaVersion(ctx)
	if vPlain != 5 {
		t.Errorf("GetSchemaVersion with plain '5' = %d, want 5", vPlain)
	}

	_ = db.DeleteSetting(ctx, "schema_version")
	vMissing, _ := db.GetSchemaVersion(ctx)
	if vMissing != 0 {
		t.Errorf("GetSchemaVersion missing = %d, want 0", vMissing)
	}

	_, _ = db.sqlDB.ExecContext(ctx, "INSERT OR REPLACE INTO settings (key, value) VALUES ('schema_version', '\"not-an-int\"')")
	vInvalid, _ := db.GetSchemaVersion(ctx)
	if vInvalid != 0 {
		t.Errorf("GetSchemaVersion invalid = %d, want 0", vInvalid)
	}

	_, _ = db.sqlDB.ExecContext(ctx, "INSERT OR REPLACE INTO settings (key, value) VALUES ('schema_version', 'not-valid-json-string')")
	vInvalidJSON, _ := db.GetSchemaVersion(ctx)
	if vInvalidJSON != 0 {
		t.Errorf("GetSchemaVersion invalid json = %d, want 0", vInvalidJSON)
	}

	if err := db.SetSetting(ctx, "chan_key", make(chan int)); err == nil {
		t.Errorf("expected error setting unmarshalable value")
	}
	if err := db.SetSettingsBulk(ctx, map[string]any{"chan_key": make(chan int)}); err == nil {
		t.Errorf("expected error in SetSettingsBulk with unmarshalable value")
	}
}
