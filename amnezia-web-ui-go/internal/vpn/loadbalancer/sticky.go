package loadbalancer

import (
	"context"
	"fmt"
	"sync"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// StickySessionManager manages session affinity and handles automatic failover.
type StickySessionManager struct {
	mu           sync.RWMutex
	db           *database.DB
	baseBalancer LoadBalancer
	caps         CapacityConfig
	userAffinity map[string]int64 // userID -> backendTunnelID
	peerAffinity map[string]int64 // peerPublicKey -> backendTunnelID
}

// NewStickySessionManager creates a new StickySessionManager wrapping a base load balancer.
func NewStickySessionManager(db *database.DB, baseBalancer LoadBalancer, caps CapacityConfig) *StickySessionManager {
	return &StickySessionManager{
		db:           db,
		baseBalancer: baseBalancer,
		caps:         caps,
		userAffinity: make(map[string]int64),
		peerAffinity: make(map[string]int64),
	}
}

// GetOrAssignBackend retrieves the sticky backend for a request or assigns an optimal healthy backend.
func (sm *StickySessionManager) GetOrAssignBackend(ctx context.Context, req *RoutingRequest) (*models.BackendTunnel, bool, error) {
	if req == nil {
		return nil, false, fmt.Errorf("routing request is nil")
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check existing affinity by peerPublicKey first, then by userID
	var targetTunnelID int64
	var hasAffinity bool

	if req.PeerPublicKey != "" {
		if tid, ok := sm.peerAffinity[req.PeerPublicKey]; ok {
			targetTunnelID = tid
			hasAffinity = true
		}
	}
	if !hasAffinity && req.UserID != "" {
		if tid, ok := sm.userAffinity[req.UserID]; ok {
			targetTunnelID = tid
			hasAffinity = true
		}
	}

	// Verify if the sticky backend is still active and within capacity
	if hasAffinity {
		for _, t := range req.AvailableTunnels {
			if t.ID == targetTunnelID && t.Status == "active" {
				if sm.caps.MaxPeersPerBackend <= 0 || t.ActiveConnections < sm.caps.MaxPeersPerBackend {
					// Sticky affinity preserved
					return t, false, nil
				}
			}
		}
	}

	// Affinity missed or assigned backend degraded -> select new backend
	selected, err := sm.baseBalancer.SelectBackend(ctx, req)
	if err != nil {
		return nil, false, err
	}

	if req.UserID != "" {
		sm.userAffinity[req.UserID] = selected.ID
	}
	if req.PeerPublicKey != "" {
		sm.peerAffinity[req.PeerPublicKey] = selected.ID
	}

	return selected, true, nil
}

// AssignAffinity records an explicit affinity for a user.
func (sm *StickySessionManager) AssignAffinity(userID string, tunnelID int64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.userAffinity[userID] = tunnelID
}

// AssignPeerAffinity records an explicit affinity for a peer public key.
func (sm *StickySessionManager) AssignPeerAffinity(peerKey string, tunnelID int64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.peerAffinity[peerKey] = tunnelID
}

// ClearAffinity removes sticky affinity for a user.
func (sm *StickySessionManager) ClearAffinity(userID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.userAffinity, userID)
}

// ClearPeerAffinity removes sticky affinity for a peer public key.
func (sm *StickySessionManager) ClearPeerAffinity(peerKey string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.peerAffinity, peerKey)
}

// GetAffinity returns the assigned backend tunnel ID for a user.
func (sm *StickySessionManager) GetAffinity(userID string) (int64, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	tid, ok := sm.userAffinity[userID]
	return tid, ok
}

// HandleFailover migrates all sessions assigned to degradedTunnelID to healthy available backends.
func (sm *StickySessionManager) HandleFailover(ctx context.Context, degradedTunnelID int64, availableTunnels []*models.BackendTunnel) (int, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	healthy := FilterHealthy(availableTunnels, sm.caps.MaxPeersPerBackend)
	if len(healthy) == 0 {
		return 0, ErrNoActiveBackends
	}

	migratedCount := 0

	// 1. Migrate in-memory user affinities
	for uID, tid := range sm.userAffinity {
		if tid == degradedTunnelID {
			req := &RoutingRequest{
				UserID:           uID,
				AvailableTunnels: healthy,
			}
			newBackend, err := sm.baseBalancer.SelectBackend(ctx, req)
			if err == nil {
				sm.userAffinity[uID] = newBackend.ID
				migratedCount++
			}
		}
	}

	// 2. Migrate in-memory peer affinities
	for pKey, tid := range sm.peerAffinity {
		if tid == degradedTunnelID {
			req := &RoutingRequest{
				PeerPublicKey:    pKey,
				AvailableTunnels: healthy,
			}
			newBackend, err := sm.baseBalancer.SelectBackend(ctx, req)
			if err == nil {
				sm.peerAffinity[pKey] = newBackend.ID
			}
		}
	}

	// 3. Migrate active sessions in DB if db handle is provided
	if sm.db != nil {
		activeSessions, err := sm.db.GetActiveVPNSessions(ctx)
		if err == nil {
			for _, sess := range activeSessions {
				if sess.BackendTunnelID == degradedTunnelID {
					req := &RoutingRequest{
						UserID:           sess.UserID,
						PeerPublicKey:    sess.PeerPublicKey,
						AvailableTunnels: healthy,
					}
					newBackend, err := sm.baseBalancer.SelectBackend(ctx, req)
					if err == nil {
						sess.BackendTunnelID = newBackend.ID
						_ = sm.db.CreateVPNSession(ctx, &sess)
					}
				}
			}
		}
	}

	return migratedCount, nil
}
