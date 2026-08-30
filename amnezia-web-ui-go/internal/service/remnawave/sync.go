package remnawave

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/service/userops"
)

// MassOperationExecutor defines interface for executing batched user creations and toggles.
type MassOperationExecutor interface {
	PerformMassOperations(ctx context.Context, req userops.MassOperationRequest) error
}

// Syncer handles periodic and on-demand synchronization with an external RemnaWave instance.
type Syncer struct {
	db      *database.DB
	client  HTTPClient
	userOps MassOperationExecutor
}

// NewSyncer creates a new RemnaWave Syncer.
func NewSyncer(db *database.DB, client HTTPClient, userOps MassOperationExecutor) *Syncer {
	if client == nil {
		client = NewClient()
	}
	return &Syncer{
		db:      db,
		client:  client,
		userOps: userOps,
	}
}

// Sync synchronizes local users with the external RemnaWave service.
func (s *Syncer) Sync(ctx context.Context) (int, string, error) {
	if s.db == nil {
		return 0, "", errors.New("database is not configured")
	}

	syncSettings, err := s.db.GetRemnaWaveSettings(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("failed to load remnawave sync settings: %w", err)
	}

	if syncSettings == nil || !syncSettings.RemnawaveSyncUsers {
		return 0, "Synchronization is disabled in settings", nil
	}

	if syncSettings.RemnawaveURL == "" || syncSettings.RemnawaveAPIKey == "" {
		return 0, "Remnawave URL or API Key not configured", nil
	}

	rwUsers, err := s.client.GetUsers(ctx, syncSettings.RemnawaveURL, syncSettings.RemnawaveAPIKey, 50)
	if err != nil {
		return 0, fmt.Sprintf("Remnawave API error: %v", err), err
	}

	rwUUIDs := make(map[string]bool, len(rwUsers))
	for _, u := range rwUsers {
		rwUUIDs[u.UUID] = true
	}

	// 1. Handle deletion (users that have remnawave_uuid in DB but are no longer in RemnaWave)
	if err := s.reconcileDeletedUsers(ctx, rwUUIDs); err != nil {
		return 0, "", err
	}

	// 2. Sync / Create users
	syncedCount := 0
	var toToggle []userops.UserToggle
	var toCreateConns []userops.ConnectionCreateRequest

	for _, rwU := range rwUsers {
		localU, err := s.db.GetUserByRemnaWaveUUID(ctx, rwU.UUID)
		if err != nil {
			slog.Warn("Error querying local user by RemnaWave UUID", "uuid", rwU.UUID, "err", err)
		}
		if localU == nil {
			localU, _ = s.db.GetUserByUsername(ctx, rwU.Username)
		}

		isActive := rwU.Status == "ACTIVE"

		if localU != nil {
			s.syncExistingUser(ctx, localU, rwU, isActive, &toToggle)
			syncedCount++
		} else {
			if s.createNewUser(ctx, syncSettings, rwU, isActive, &toCreateConns) {
				syncedCount++
			}
		}
	}

	// 3. Execute all collected mass operations
	if (len(toToggle) > 0 || len(toCreateConns) > 0) && s.userOps != nil {
		slog.Info("Executing mass operations for RemnaWave sync",
			"toggle_count", len(toToggle),
			"create_count", len(toCreateConns),
		)
		_ = s.userOps.PerformMassOperations(ctx, userops.MassOperationRequest{
			ToggleUIDs:  toToggle,
			CreateConns: toCreateConns,
		})
	}

	return syncedCount, "Successfully synchronized with Remnawave", nil
}

func (s *Syncer) reconcileDeletedUsers(ctx context.Context, rwUUIDs map[string]bool) error {
	allUsers, err := s.db.GetAllUsers(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch local users: %w", err)
	}

	var toDeleteIDs []string
	for _, u := range allUsers {
		if u.RemnaWaveUUID != nil && *u.RemnaWaveUUID != "" {
			if !rwUUIDs[*u.RemnaWaveUUID] {
				toDeleteIDs = append(toDeleteIDs, u.ID)
			}
		}
	}

	if len(toDeleteIDs) > 0 && s.userOps != nil {
		slog.Info("Removing local users deleted in RemnaWave", "count", len(toDeleteIDs))
		_ = s.userOps.PerformMassOperations(ctx, userops.MassOperationRequest{
			DeleteUIDs: toDeleteIDs,
		})
	}
	return nil
}

func (s *Syncer) syncExistingUser(ctx context.Context, localU *models.User, rwU User, isActive bool, toToggle *[]userops.UserToggle) {
	updates := map[string]any{
		"username":       rwU.Username,
		"remnawave_uuid": rwU.UUID,
	}
	if rwU.TelegramID != nil {
		updates["telegramId"] = rwU.TelegramIDString()
	}
	if rwU.Email != "" {
		updates["email"] = rwU.Email
	}
	if rwU.Description != "" {
		updates["description"] = rwU.Description
	}

	if localU.Enabled != isActive {
		*toToggle = append(*toToggle, userops.UserToggle{UserID: localU.ID, Enabled: isActive})
	}

	_, _ = s.db.UpdateUser(ctx, localU.ID, updates)
}

func (s *Syncer) createNewUser(ctx context.Context, syncSettings *models.SyncSettings, rwU User, isActive bool, toCreateConns *[]userops.ConnectionCreateRequest) bool {
	tokenBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	shareToken := base64.RawURLEncoding.EncodeToString(tokenBytes)

	proto := syncSettings.RemnawaveProtocol
	if proto == "" {
		proto = "awg"
	}

	newUser := &models.User{
		Username:             rwU.Username,
		Role:                 models.RoleUser,
		Email:                stringPtrOrNil(rwU.Email),
		TelegramID:           rwU.TelegramIDString(),
		Description:          stringPtrOrNil(rwU.Description),
		Enabled:              isActive,
		RemnaWaveUUID:        &rwU.UUID,
		ShareEnabled:         false,
		ShareToken:           &shareToken,
		TrafficResetStrategy: models.ResetStrategyNever,
		AWGMimicry:           models.AWGMimicryAuto,
		CreatedAt:            time.Now().UTC(),
	}

	newID, err := s.db.CreateUser(ctx, newUser)
	if err != nil {
		slog.Error("Failed to create user from RemnaWave sync", "username", rwU.Username, "err", err)
		return false
	}

	if syncSettings.RemnawaveCreateConns && syncSettings.RemnawaveServerID > 0 {
		*toCreateConns = append(*toCreateConns, userops.ConnectionCreateRequest{
			UserID:   newID,
			ServerID: syncSettings.RemnawaveServerID,
			Protocol: proto,
			Name:     fmt.Sprintf("%s_vpn", rwU.Username),
		})
	}
	return true
}

func stringPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
