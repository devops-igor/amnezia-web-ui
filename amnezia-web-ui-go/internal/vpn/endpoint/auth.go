package endpoint

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

var (
	ErrPeerNotFound         = errors.New("peer public key not registered")
	ErrUserNotFound         = errors.New("user not found")
	ErrUserDisabled         = errors.New("user account is disabled")
	ErrUserExpired          = errors.New("user account has expired")
	ErrTrafficLimitExceeded = errors.New("user traffic limit exceeded")
	ErrInvalidProtocol      = errors.New("invalid protocol for connection")
)

// Authenticator validates connecting peer public keys and retrieves user credentials.
type Authenticator interface {
	AuthenticatePeer(ctx context.Context, peerPublicKey string) (*models.User, *models.UserConnection, error)
}

// DBAuthenticator authenticates peers against SQLite user_connections and users tables.
type DBAuthenticator struct {
	db *database.DB
}

// NewDBAuthenticator creates a new database-backed authenticator.
func NewDBAuthenticator(db *database.DB) *DBAuthenticator {
	return &DBAuthenticator{db: db}
}

// AuthenticatePeer verifies if peerPublicKey is bound to an active, non-expired, enabled user.
func (a *DBAuthenticator) AuthenticatePeer(ctx context.Context, peerPublicKey string) (*models.User, *models.UserConnection, error) {
	if a.db == nil {
		return nil, nil, errors.New("database not initialized")
	}
	if peerPublicKey == "" {
		return nil, nil, ErrPeerNotFound
	}

	// Look up connection by token (client_id matching peer public key)
	conn, err := a.db.GetConnectionByToken(ctx, peerPublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to lookup connection: %w", err)
	}
	if conn == nil {
		return nil, nil, ErrPeerNotFound
	}

	if models.NormalizeProtocol(conn.Protocol) != "awg" {
		return nil, nil, ErrInvalidProtocol
	}

	user, err := a.db.GetUser(ctx, conn.UserID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to lookup user %s: %w", conn.UserID, err)
	}
	if user == nil {
		return nil, nil, ErrUserNotFound
	}

	if !user.Enabled {
		return nil, nil, ErrUserDisabled
	}

	now := time.Now().UTC()
	if user.ExpiresAt != nil && !user.ExpiresAt.IsZero() && now.After(*user.ExpiresAt) {
		return nil, nil, ErrUserExpired
	}
	if user.ExpirationDate != nil && !user.ExpirationDate.IsZero() && now.After(*user.ExpirationDate) {
		return nil, nil, ErrUserExpired
	}

	if user.TrafficLimit > 0 && user.TrafficUsed >= user.TrafficLimit {
		return nil, nil, ErrTrafficLimitExceeded
	}

	return user, conn, nil
}
