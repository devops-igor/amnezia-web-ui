package userops

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"golang.org/x/sync/errgroup"
)

// ErrUserNotFound indicates the requested user does not exist.
var ErrUserNotFound = errors.New("user not found")

// ClientToggler represents protocol managers supporting client enable/disable toggles.
type ClientToggler interface {
	ToggleClient(ctx context.Context, server *models.Server, clientID string, enable bool) error
}

// ProtocolResolver retrieves a protocol manager by protocol name.
type ProtocolResolver interface {
	Get(proto string) (manager.ProtocolManager, bool)
}

// ConnectionCreateRequest specifies a connection to be provisioned on a remote server.
type ConnectionCreateRequest struct {
	UserID   string
	ServerID int64
	Protocol string
	Name     string
}

// UserToggle specifies a user enable/disable request.
type UserToggle struct {
	UserID  string
	Enabled bool
}

// MassOperationRequest encapsulates batched user deletion, toggle, and connection creations.
type MassOperationRequest struct {
	DeleteUIDs  []string
	ToggleUIDs  []UserToggle
	CreateConns []ConnectionCreateRequest
}

type serverOperations struct {
	deletes []models.UserConnection
	toggles []connToggle
	creates []ConnectionCreateRequest
}

type connToggle struct {
	conn    models.UserConnection
	enabled bool
}

// Service coordinates SSH-based user operations and mass batching.
type Service struct {
	db       *database.DB
	registry ProtocolResolver
}

// New creates a new user operations Service.
func New(db *database.DB, registry ProtocolResolver) *Service {
	return &Service{
		db:       db,
		registry: registry,
	}
}

// NewUserOpsService creates a new Service.
func NewUserOpsService(db *database.DB, registry ProtocolResolver) *Service {
	return New(db, registry)
}

// DeleteUser deletes a user and removes all their server connections grouped by server_id.
// This ensures at most 1 SSH connection is established per unique server.
func (s *Service) DeleteUser(ctx context.Context, userID string) (bool, error) {
	if s.db == nil {
		return false, errors.New("database is not configured")
	}

	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("failed to fetch user %s: %w", userID, err)
	}
	if user == nil {
		return false, nil
	}

	err = s.PerformMassOperations(ctx, MassOperationRequest{
		DeleteUIDs: []string{userID},
	})
	if err != nil {
		return false, err
	}

	return true, nil
}

// ToggleUser enables or disables a user and toggles active server connections.
func (s *Service) ToggleUser(ctx context.Context, userID string, enabled bool) (bool, error) {
	if s.db == nil {
		return false, errors.New("database is not configured")
	}

	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("failed to fetch user %s: %w", userID, err)
	}
	if user == nil {
		return false, nil
	}

	err = s.PerformMassOperations(ctx, MassOperationRequest{
		ToggleUIDs: []UserToggle{{UserID: userID, Enabled: enabled}},
	})
	if err != nil {
		return false, err
	}

	return true, nil
}

// PerformMassOperations executes multiple SSH operations grouped by server with bounded concurrency.
func (s *Service) PerformMassOperations(ctx context.Context, req MassOperationRequest) error {
	if s.db == nil {
		return errors.New("database is not configured")
	}

	serverOps := make(map[int64]*serverOperations)
	var opsMu sync.Mutex

	getOps := func(serverID int64) *serverOperations {
		opsMu.Lock()
		defer opsMu.Unlock()
		if ops, exists := serverOps[serverID]; exists {
			return ops
		}
		ops := &serverOperations{
			deletes: make([]models.UserConnection, 0),
			toggles: make([]connToggle, 0),
			creates: make([]ConnectionCreateRequest, 0),
		}
		serverOps[serverID] = ops
		return ops
	}

	// 1. Group deletes by server
	for _, uid := range req.DeleteUIDs {
		conns, err := s.db.GetConnectionsByUserID(ctx, uid)
		if err != nil {
			slog.Warn("Failed to fetch connections for user deletion", "user_id", uid, "err", err)
			continue
		}
		for _, c := range conns {
			ops := getOps(c.ServerID)
			ops.deletes = append(ops.deletes, c)
		}
	}

	// 2. Group toggles by server
	for _, t := range req.ToggleUIDs {
		conns, err := s.db.GetConnectionsByUserID(ctx, t.UserID)
		if err != nil {
			slog.Warn("Failed to fetch connections for user toggle", "user_id", t.UserID, "err", err)
			continue
		}
		for _, c := range conns {
			ops := getOps(c.ServerID)
			ops.toggles = append(ops.toggles, connToggle{conn: c, enabled: t.Enabled})
		}
	}

	// 3. Group creates by server
	for _, cReq := range req.CreateConns {
		ops := getOps(cReq.ServerID)
		ops.creates = append(ops.creates, cReq)
	}

	// 4. Run server operations concurrently
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(10)

	for serverID, ops := range serverOps {
		sID := serverID
		sOps := ops
		g.Go(func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic recovered in userops server worker", "server_id", sID, "panic", r)
				}
			}()
			s.runServerOperations(gCtx, sID, sOps)
			return nil
		})
	}

	_ = g.Wait()

	// 5. Final DB cleanup at user level
	for _, uid := range req.DeleteUIDs {
		conns, _ := s.db.GetConnectionsByUserID(ctx, uid)
		if len(conns) > 0 {
			slog.Warn("Skipping user record deletion due to remaining connections from failed remote removals", "user_id", uid, "remaining_connections", len(conns))
			continue
		}
		_, err := s.db.DeleteUser(ctx, uid)
		if err != nil {
			slog.Warn("Failed to delete user record", "user_id", uid, "err", err)
		}
	}

	for _, t := range req.ToggleUIDs {
		_, err := s.db.UpdateUser(ctx, t.UserID, map[string]any{"enabled": t.Enabled})
		if err != nil {
			slog.Warn("Failed to update user enabled status", "user_id", t.UserID, "err", err)
		}
	}

	return nil
}

func (s *Service) runServerOperations(ctx context.Context, serverID int64, ops *serverOperations) {
	server, err := s.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		slog.Warn("Server not found for mass operations", "server_id", serverID, "err", err)
		return
	}

	// 1. Deletes
	for _, c := range ops.deletes {
		proto := models.NormalizeProtocol(c.Protocol)
		removeFailed := false
		if s.registry != nil {
			if mgr, ok := s.registry.Get(proto); ok {
				if err := mgr.RemoveClient(ctx, server, c.ClientID); err != nil {
					slog.Warn("Failed to remove remote client during mass delete",
						"server_id", serverID,
						"protocol", proto,
						"client_id", c.ClientID,
						"err", err,
					)
					removeFailed = true
				}
			}
		}
		if !removeFailed {
			_, _ = s.db.DeleteConnection(ctx, c.ID)
		}
	}

	// 2. Toggles
	for _, t := range ops.toggles {
		proto := models.NormalizeProtocol(t.conn.Protocol)
		if s.registry != nil {
			if mgr, ok := s.registry.Get(proto); ok {
				if toggler, ok := mgr.(ClientToggler); ok {
					if err := toggler.ToggleClient(ctx, server, t.conn.ClientID, t.enabled); err != nil {
						slog.Warn("Failed to toggle remote client",
							"server_id", serverID,
							"protocol", proto,
							"client_id", t.conn.ClientID,
							"enabled", t.enabled,
							"err", err,
						)
					}
				}
			}
		}
	}

	// 3. Creates
	for _, cReq := range ops.creates {
		proto := models.NormalizeProtocol(cReq.Protocol)
		if s.registry != nil {
			if mgr, ok := s.registry.Get(proto); ok {
				clientParams := map[string]any{
					"name": cReq.Name,
				}
				res, err := mgr.AddClient(ctx, server, clientParams)
				if err != nil {
					slog.Error("Failed to add remote client during mass create",
						"server_id", serverID,
						"protocol", proto,
						"err", err,
					)
					continue
				}

				clientID, _ := res["client_id"].(string)
				if clientID != "" {
					newConn := &models.UserConnection{
						ID:        generateUUID(),
						UserID:    cReq.UserID,
						ServerID:  serverID,
						Protocol:  proto,
						ClientID:  clientID,
						Name:      cReq.Name,
						CreatedAt: time.Now().UTC(),
					}
					if _, err := s.db.CreateConnection(ctx, newConn); err != nil {
						slog.Error("Failed to insert user connection into DB", "conn_id", newConn.ID, "err", err)
					}
				}
			}
		}
	}
}

func generateUUID() string {
	uuidBytes := make([]byte, 16)
	_, _ = rand.Read(uuidBytes)
	uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x40 // v4
	uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80 // RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuidBytes[0:4], uuidBytes[4:6], uuidBytes[6:8], uuidBytes[8:10], uuidBytes[10:16])
}
