package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// StatusChecker defines an interface for checking protocol installation on a remote host.
type StatusChecker interface {
	GetServerStatus(ctx context.Context, server *models.Server) (map[string]any, error)
}

// ProtocolResolver resolves a ProtocolManager by protocol name.
type ProtocolResolver interface {
	Get(proto string) (manager.ProtocolManager, bool)
}

// Reconciler executes startup synchronization to clean up orphaned connections and stale protocols.
type Reconciler struct {
	db       *database.DB
	registry ProtocolResolver
}

// New creates a new Reconciler instance.
func New(db *database.DB, registry ProtocolResolver) *Reconciler {
	return &Reconciler{
		db:       db,
		registry: registry,
	}
}

// CleanupStaleProtocols performs a two-phase cleanup:
// Phase 1 (DB-only): Removes user_connections for protocols no longer in server.protocols.
// Phase 2 (SSH-based): Verifies installed containers/binaries on each server; if missing, removes connections and protocol.
func (r *Reconciler) CleanupStaleProtocols(ctx context.Context) error {
	if r.db == nil {
		return errors.New("database is not configured")
	}

	servers, err := r.db.GetAllServers(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch servers for startup reconciliation: %w", err)
	}

	r.cleanupPhase1DBOnly(ctx, servers)

	if r.registry != nil {
		r.cleanupPhase2Remote(ctx, servers)
	}

	return nil
}

func (r *Reconciler) cleanupPhase1DBOnly(ctx context.Context, servers []models.Server) {
	for _, server := range servers {
		serverID := server.ID
		activeProtos := make(map[string]bool, len(server.Protocols))
		for proto := range server.Protocols {
			activeProtos[models.NormalizeProtocol(proto)] = true
		}

		conns, err := r.db.GetConnectionsByServerID(ctx, serverID)
		if err != nil {
			slog.Warn("Reconciliation Phase 1: failed to query connections", "server_id", serverID, "err", err)
			continue
		}

		orphanProtos := make(map[string]bool)
		for _, c := range conns {
			normalized := models.NormalizeProtocol(c.Protocol)
			if !activeProtos[normalized] {
				orphanProtos[c.Protocol] = true
			}
		}

		for proto := range orphanProtos {
			deleted, err := r.db.DeleteConnectionsByServerAndProtocol(ctx, serverID, proto)
			if err != nil {
				slog.Warn("Reconciliation Phase 1: error deleting orphaned connections",
					"server_id", serverID, "protocol", proto, "err", err)
			} else if deleted > 0 {
				slog.Info("Startup cleanup: removed orphaned connections (protocol not in server.protocols)",
					"server_id", serverID,
					"protocol", proto,
					"count", deleted,
				)
			}
		}
	}
}

func (r *Reconciler) cleanupPhase2Remote(ctx context.Context, servers []models.Server) {
	for _, server := range servers {
		serverID := server.ID
		if len(server.Protocols) == 0 {
			continue
		}

		srvCopy := server
		protocolsCopy := make(map[string]any, len(srvCopy.Protocols))
		for k, v := range srvCopy.Protocols {
			protocolsCopy[k] = v
		}

		var staleProtos []string
		for protoKey := range protocolsCopy {
			if r.isProtocolStale(ctx, &srvCopy, protoKey) {
				staleProtos = append(staleProtos, protoKey)
			}
		}

		if len(staleProtos) > 0 {
			for _, proto := range staleProtos {
				deleted, _ := r.db.DeleteConnectionsByServerAndProtocol(ctx, serverID, proto)
				delete(protocolsCopy, proto)
				slog.Info("Startup cleanup: removed stale protocol and associated connections",
					"server_id", serverID,
					"protocol", proto,
					"deleted_connections", deleted,
				)
			}
			_ = r.db.UpdateServer(ctx, serverID, map[string]any{"protocols": protocolsCopy})
		}
	}
}

func (r *Reconciler) isProtocolStale(ctx context.Context, server *models.Server, protoKey string) bool {
	proto := models.NormalizeProtocol(protoKey)
	mgr, ok := r.registry.Get(proto)
	if !ok {
		return false
	}

	checker, isChecker := mgr.(StatusChecker)
	if !isChecker {
		return false
	}

	status, err := checker.GetServerStatus(ctx, server)
	if err != nil {
		slog.Warn("Startup cleanup: failed to check protocol status on server",
			"server_id", server.ID,
			"protocol", proto,
			"err", err,
		)
		return false
	}

	if status == nil {
		return false
	}

	if errMsg, hasErr := status["error"]; hasErr && errMsg != nil && fmt.Sprint(errMsg) != "" {
		slog.Warn("Startup cleanup: protocol status reported error, skipping stale cleanup",
			"server_id", server.ID,
			"protocol", proto,
			"error", errMsg,
		)
		return false
	}

	existsVal, ok := status["container_exists"]
	if !ok {
		return false
	}
	exists, isBool := existsVal.(bool)
	if !isBool {
		return false
	}

	return !exists
}
