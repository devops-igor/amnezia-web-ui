package endpoint

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

var (
	ErrSessionNotFound = errors.New("vpn session not found")
)

// SessionManager tracks active VPN peer sessions in memory and SQLite.
type SessionManager struct {
	mu             sync.RWMutex
	db             *database.DB
	ipam           *IPAM
	sessionsByPeer map[string]*models.VPNSession // peerPublicKey -> session
	sessionsByID   map[string]*models.VPNSession // sessionID -> session
	activeCount    atomic.Int64
}

// NewSessionManager initializes a new VPN Session Manager.
func NewSessionManager(db *database.DB, ipam *IPAM) *SessionManager {
	return &SessionManager{
		db:             db,
		ipam:           ipam,
		sessionsByPeer: make(map[string]*models.VPNSession),
		sessionsByID:   make(map[string]*models.VPNSession),
	}
}

// CreateSession allocates a new VPN session and persists it.
func (sm *SessionManager) CreateSession(ctx context.Context, userID, peerPublicKey, assignedIP string, backendTunnelID int64) (*models.VPNSession, error) {
	if userID == "" || peerPublicKey == "" || assignedIP == "" {
		return nil, errors.New("missing required session fields")
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// If session already exists for this peer, close it before creating a new one
	if oldSess, ok := sm.sessionsByPeer[peerPublicKey]; ok {
		delete(sm.sessionsByID, oldSess.ID)
		delete(sm.sessionsByPeer, peerPublicKey)
		sm.activeCount.Add(-1)
	}

	uuidBytes := make([]byte, 16)
	_, _ = rand.Read(uuidBytes)
	uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x40
	uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80
	sessionID := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuidBytes[0:4], uuidBytes[4:6], uuidBytes[6:8], uuidBytes[8:10], uuidBytes[10:16])

	now := time.Now().UTC()
	sess := &models.VPNSession{
		ID:              sessionID,
		UserID:          userID,
		BackendTunnelID: backendTunnelID,
		PeerPublicKey:   peerPublicKey,
		AssignedIP:      assignedIP,
		ConnectedAt:     now,
		LastSeen:        now,
		RxBytes:         0,
		TxBytes:         0,
		Status:          "connected",
	}

	if sm.db != nil {
		if err := sm.db.CreateVPNSession(ctx, sess); err != nil {
			return nil, fmt.Errorf("failed to persist vpn session: %w", err)
		}
	}

	sm.sessionsByPeer[peerPublicKey] = sess
	sm.sessionsByID[sessionID] = sess
	sm.activeCount.Add(1)

	return sess, nil
}

// GetSession retrieves a session by peer public key.
func (sm *SessionManager) GetSession(peerPublicKey string) (*models.VPNSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	sess, ok := sm.sessionsByPeer[peerPublicKey]
	return sess, ok
}

// GetSessionByID retrieves a session by session ID.
func (sm *SessionManager) GetSessionByID(sessionID string) (*models.VPNSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	sess, ok := sm.sessionsByID[sessionID]
	return sess, ok
}

// GetSessionsByUserID retrieves all active sessions belonging to a user ID.
func (sm *SessionManager) GetSessionsByUserID(userID string) []*models.VPNSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var result []*models.VPNSession
	for _, sess := range sm.sessionsByID {
		if sess.UserID == userID && sess.Status == "connected" {
			result = append(result, sess)
		}
	}
	return result
}

// UpdateActivity updates traffic counters and last seen timestamp for a session in memory.
func (sm *SessionManager) UpdateActivity(peerPublicKey string, rxBytes, txBytes int64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sess, ok := sm.sessionsByPeer[peerPublicKey]; ok {
		sess.RxBytes += rxBytes
		sess.TxBytes += txBytes
		sess.LastSeen = time.Now().UTC()
	}
}

// TouchSession refreshes the last seen timestamp of a session.
func (sm *SessionManager) TouchSession(peerPublicKey string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sess, ok := sm.sessionsByPeer[peerPublicKey]; ok {
		sess.LastSeen = time.Now().UTC()
	}
}

// CloseSession transitions a session to the specified status and releases IPAM allocation.
func (sm *SessionManager) CloseSession(ctx context.Context, sessionID string, status string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sess, ok := sm.sessionsByID[sessionID]
	if !ok {
		return ErrSessionNotFound
	}

	if status == "" {
		status = "disconnected"
	}

	sess.Status = status
	sess.LastSeen = time.Now().UTC()

	if sm.db != nil {
		_ = sm.db.CreateVPNSession(ctx, sess)
	}

	if sm.ipam != nil {
		_ = sm.ipam.Release(sess.PeerPublicKey)
	}

	delete(sm.sessionsByID, sessionID)
	delete(sm.sessionsByPeer, sess.PeerPublicKey)
	sm.activeCount.Add(-1)

	return nil
}

// CheckTimeouts checks for sessions that have exceeded the idleTimeout and closes them.
func (sm *SessionManager) CheckTimeouts(ctx context.Context, idleTimeout time.Duration) ([]*models.VPNSession, error) {
	if idleTimeout <= 0 {
		return nil, nil
	}

	sm.mu.Lock()
	var timedOut []*models.VPNSession
	now := time.Now().UTC()

	for _, sess := range sm.sessionsByID {
		if now.Sub(sess.LastSeen) > idleTimeout {
			timedOut = append(timedOut, sess)
		}
	}

	for _, sess := range timedOut {
		sess.Status = "disconnected"
		if sm.db != nil {
			_ = sm.db.CreateVPNSession(ctx, sess)
		}
		if sm.ipam != nil {
			_ = sm.ipam.Release(sess.PeerPublicKey)
		}
		delete(sm.sessionsByID, sess.ID)
		delete(sm.sessionsByPeer, sess.PeerPublicKey)
		sm.activeCount.Add(-1)
	}
	sm.mu.Unlock()

	return timedOut, nil
}

// Drain marks all active sessions as draining.
func (sm *SessionManager) Drain(ctx context.Context, timeout time.Duration) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for _, sess := range sm.sessionsByID {
		sess.Status = "draining"
		if sm.db != nil {
			_ = sm.db.CreateVPNSession(ctx, sess)
		}
	}
	return nil
}

// ListActiveSessions returns a copy of all active sessions.
func (sm *SessionManager) ListActiveSessions() []*models.VPNSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var result []*models.VPNSession
	for _, s := range sm.sessionsByID {
		copySess := *s
		result = append(result, &copySess)
	}
	return result
}

// ActiveCount returns the number of active sessions.
func (sm *SessionManager) ActiveCount() int {
	return int(sm.activeCount.Load())
}

// SyncFromDB restores active sessions from the database on startup.
func (sm *SessionManager) SyncFromDB(ctx context.Context) error {
	if sm.db == nil {
		return nil
	}

	sessions, err := sm.db.GetActiveVPNSessions(ctx)
	if err != nil {
		return fmt.Errorf("failed to load active sessions: %w", err)
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	for i := range sessions {
		sess := sessions[i]
		sm.sessionsByID[sess.ID] = &sess
		sm.sessionsByPeer[sess.PeerPublicKey] = &sess
		sm.activeCount.Add(1)

		if sm.ipam != nil && sess.AssignedIP != "" {
			if ip := net.ParseIP(sess.AssignedIP); ip != nil {
				_ = sm.ipam.Reserve(ip, sess.PeerPublicKey)
			}
		}
	}

	return nil
}
