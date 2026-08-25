package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/security"
)

// GetSetting retrieves a setting value by key, deserializing into target and decrypting SSL credentials.
func (d *DB) GetSetting(ctx context.Context, key string, target any) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var val sql.NullString
	err := d.sqlDB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&val)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("failed to get setting %s: %w", key, err)
	}

	if !val.Valid || val.String == "" {
		return nil
	}

	if key == "ssl" {
		var sslMap map[string]any
		if err := json.Unmarshal([]byte(val.String), &sslMap); err == nil {
			if kt, ok := sslMap["key_text"].(string); ok && kt != "" {
				sslMap["key_text"] = security.DecryptCredentialSafe(kt, d.secretKey)
			}
			if ct, ok := sslMap["cert_text"].(string); ok && ct != "" {
				sslMap["cert_text"] = security.DecryptCredentialSafe(ct, d.secretKey)
			}
			b, err := json.Marshal(sslMap)
			if err != nil {
				return err
			}
			return json.Unmarshal(b, target)
		}
	}

	return json.Unmarshal([]byte(val.String), target)
}

// GetAllSettings retrieves all settings as a map, deserializing JSON and decrypting SSL certificates.
func (d *DB) GetAllSettings(ctx context.Context) (map[string]any, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.sqlDB.QueryContext(ctx, "SELECT key, value FROM settings")
	if err != nil {
		return nil, fmt.Errorf("failed to query settings: %w", err)
	}
	defer rows.Close()

	result := make(map[string]any)
	for rows.Next() {
		var k string
		var v sql.NullString
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		if !v.Valid || v.String == "" {
			result[k] = nil
			continue
		}

		var parsed any
		if err := json.Unmarshal([]byte(v.String), &parsed); err == nil {
			if k == "ssl" {
				if sslMap, ok := parsed.(map[string]any); ok {
					if kt, ok := sslMap["key_text"].(string); ok && kt != "" {
						sslMap["key_text"] = security.DecryptCredentialSafe(kt, d.secretKey)
					}
					if ct, ok := sslMap["cert_text"].(string); ok && ct != "" {
						sslMap["cert_text"] = security.DecryptCredentialSafe(ct, d.secretKey)
					}
					parsed = sslMap
				}
			}
			result[k] = parsed
		} else {
			result[k] = v.String
		}
	}

	return result, rows.Err()
}

// SetSetting sets or updates a setting key with JSON serialization and SSL encryption.
func (d *DB) SetSetting(ctx context.Context, key string, value any) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	valToStore := value
	if key == "ssl" {
		valToStore = d.prepareSSLSettingForStore(value)
	}

	b, err := json.Marshal(valToStore)
	if err != nil {
		return fmt.Errorf("failed to marshal setting %s: %w", key, err)
	}

	_, err = d.sqlDB.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, string(b),
	)
	if err != nil {
		return fmt.Errorf("failed to set setting %s: %w", key, err)
	}

	return nil
}

// UpdateSetting is an alias for SetSetting.
func (d *DB) UpdateSetting(ctx context.Context, key string, value any) error {
	return d.SetSetting(ctx, key, value)
}

// SetSettingsBulk batch-updates multiple settings in a single transaction.
func (d *DB) SetSettingsBulk(ctx context.Context, settings map[string]any) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin bulk settings tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for key, value := range settings {
		valToStore := value
		if key == "ssl" {
			valToStore = d.prepareSSLSettingForStore(value)
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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit bulk settings: %w", err)
	}

	return nil
}

// SaveAllSettings is an alias for SetSettingsBulk.
func (d *DB) SaveAllSettings(ctx context.Context, settingsMap map[string]any) error {
	return d.SetSettingsBulk(ctx, settingsMap)
}

// DeleteSetting removes a setting key.
func (d *DB) DeleteSetting(ctx context.Context, key string) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	_, err := d.sqlDB.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", key)
	if err != nil {
		return fmt.Errorf("failed to delete setting %s: %w", key, err)
	}
	return nil
}

// GetSSLSettings retrieves decrypted SSL configuration.
func (d *DB) GetSSLSettings(ctx context.Context) (*models.SSLSettings, error) {
	var ssl models.SSLSettings
	if err := d.GetSetting(ctx, "ssl", &ssl); err != nil {
		return nil, err
	}
	return &ssl, nil
}

// SaveSSLSettings encrypts cert/key and saves SSL configuration.
func (d *DB) SaveSSLSettings(ctx context.Context, ssl *models.SSLSettings) error {
	return d.SetSetting(ctx, "ssl", ssl)
}

// GetRemnaWaveSettings retrieves RemnaWave sync configuration.
func (d *DB) GetRemnaWaveSettings(ctx context.Context) (*models.SyncSettings, error) {
	var sync models.SyncSettings
	if err := d.GetSetting(ctx, "sync", &sync); err != nil {
		return nil, err
	}
	return &sync, nil
}

// GetSchemaVersion retrieves the recorded schema version integer.
func (d *DB) GetSchemaVersion(ctx context.Context) (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var val sql.NullString
	err := d.sqlDB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'schema_version'").Scan(&val)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	if !val.Valid || val.String == "" {
		return 0, nil
	}

	var strVersion string
	if err := json.Unmarshal([]byte(val.String), &strVersion); err == nil {
		if v, err := strconv.Atoi(strVersion); err == nil {
			return v, nil
		}
	}

	if v, err := strconv.Atoi(val.String); err == nil {
		return v, nil
	}

	return 0, nil
}

// SetSchemaVersion records the current database schema version.
func (d *DB) SetSchemaVersion(ctx context.Context, version int) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	valStr := fmt.Sprintf(`"%d"`, version)
	_, err := d.sqlDB.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES ('schema_version', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		valStr,
	)
	if err != nil {
		return fmt.Errorf("failed to set schema version: %w", err)
	}
	return nil
}

// GetMigrationFlag retrieves a migration flag string.
func (d *DB) GetMigrationFlag(ctx context.Context, key string) (string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var val sql.NullString
	err := d.sqlDB.QueryRowContext(ctx, "SELECT value FROM migration_flags WHERE key = ?", key).Scan(&val)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if !val.Valid {
		return "", nil
	}
	return val.String, nil
}

// SetMigrationFlag sets a migration flag value.
func (d *DB) SetMigrationFlag(ctx context.Context, key, val string) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	_, err := d.sqlDB.ExecContext(ctx,
		"INSERT INTO migration_flags (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, val,
	)
	if err != nil {
		return fmt.Errorf("failed to set migration flag %s: %w", key, err)
	}
	return nil
}

func (d *DB) encryptSSLCredential(val string) string {
	if val != "" && !security.LooksLikeFernetToken(val) {
		if enc, err := security.EncryptCredential(val, d.secretKey); err == nil {
			return enc
		}
	}
	return val
}

func (d *DB) prepareSSLSettingForStore(value any) any {
	switch v := value.(type) {
	case map[string]any:
		sslCopy := make(map[string]any, len(v))
		for k, val := range v {
			sslCopy[k] = val
		}
		if kt, ok := sslCopy["key_text"].(string); ok {
			sslCopy["key_text"] = d.encryptSSLCredential(kt)
		}
		if ct, ok := sslCopy["cert_text"].(string); ok {
			sslCopy["cert_text"] = d.encryptSSLCredential(ct)
		}
		return sslCopy
	case *models.SSLSettings:
		if v == nil {
			return nil
		}
		sslCopy := *v
		sslCopy.KeyText = d.encryptSSLCredential(sslCopy.KeyText)
		sslCopy.CertText = d.encryptSSLCredential(sslCopy.CertText)
		return sslCopy
	case models.SSLSettings:
		sslCopy := v
		sslCopy.KeyText = d.encryptSSLCredential(sslCopy.KeyText)
		sslCopy.CertText = d.encryptSSLCredential(sslCopy.CertText)
		return sslCopy
	default:
		return value
	}
}
