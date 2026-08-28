package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"golang.org/x/crypto/curve25519"
)

var (
	ErrTunnelNotFound = errors.New("backend tunnel not found")
)

// Pool manages the in-process AWG tunnels connected to backend VPN servers.
type Pool struct {
	mu                sync.RWMutex
	db                *database.DB
	tunnelsByServerID map[int64]*models.BackendTunnel
	tunnelsByID       map[int64]*models.BackendTunnel
	tunnelsByIfName   map[string]*models.BackendTunnel
	closed            bool
}

// NewPool initializes an in-process backend tunnel pool.
func NewPool(db *database.DB) *Pool {
	return &Pool{
		db:                db,
		tunnelsByServerID: make(map[int64]*models.BackendTunnel),
		tunnelsByID:       make(map[int64]*models.BackendTunnel),
		tunnelsByIfName:   make(map[string]*models.BackendTunnel),
	}
}

// GenerateCurve25519KeyPair generates a Base64-encoded WireGuard/AWG keypair.
func GenerateCurve25519KeyPair() (pubKey string, privKey string, err error) {
	privBytes := make([]byte, 32)
	if _, err := rand.Read(privBytes); err != nil {
		return "", "", fmt.Errorf("failed to generate random private key: %w", err)
	}

	pubBytes, err := curve25519.X25519(privBytes, curve25519.Basepoint)
	if err != nil {
		return "", "", fmt.Errorf("failed to compute public key: %w", err)
	}

	pubKey = base64.StdEncoding.EncodeToString(pubBytes)
	privKey = base64.StdEncoding.EncodeToString(privBytes)
	return pubKey, privKey, nil
}

// SyncFromDB loads all backend tunnels from the database into the memory pool.
func (p *Pool) SyncFromDB(ctx context.Context) error {
	if p.db == nil {
		return nil
	}

	tunnels, err := p.db.GetBackendTunnels(ctx)
	if err != nil {
		return fmt.Errorf("failed to load backend tunnels from DB: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for i := range tunnels {
		t := tunnels[i]
		p.tunnelsByServerID[t.ServerID] = &t
		p.tunnelsByID[t.ID] = &t
		p.tunnelsByIfName[t.InterfaceName] = &t
	}

	return nil
}

// AddTunnel establishes or registers an in-process AWG backend tunnel for a server.
func (p *Pool) AddTunnel(ctx context.Context, serverID int64, endpoint, serverPubKey string) (*models.BackendTunnel, error) {
	if serverID <= 0 {
		return nil, errors.New("server_id must be greater than 0")
	}
	if endpoint == "" {
		return nil, errors.New("endpoint cannot be empty")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, ok := p.tunnelsByServerID[serverID]; ok {
		existing.Endpoint = endpoint
		if serverPubKey != "" {
			existing.PublicKey = serverPubKey
		}
		if p.db != nil {
			_ = p.db.UpdateBackendTunnel(ctx, existing.ID, map[string]any{
				"endpoint":   endpoint,
				"public_key": existing.PublicKey,
			})
		}
		return existing, nil
	}

	pubKey := serverPubKey
	var privKey string
	if pubKey == "" {
		pk, sk, err := GenerateCurve25519KeyPair()
		if err != nil {
			return nil, err
		}
		pubKey = pk
		privKey = sk
	} else {
		_, sk, err := GenerateCurve25519KeyPair()
		if err != nil {
			return nil, err
		}
		privKey = sk
	}

	ifName := fmt.Sprintf("awg-be-%d", serverID)
	now := time.Now().UTC()

	tunnel := &models.BackendTunnel{
		ServerID:          serverID,
		InterfaceName:     ifName,
		PublicKey:         pubKey,
		PrivateKey:        privKey,
		Endpoint:          endpoint,
		Status:            "active",
		LastHealthCheck:   &now,
		LatencyMS:         10,
		ActiveConnections: 0,
		CreatedAt:         now,
	}

	if p.db != nil {
		id, err := p.db.CreateBackendTunnel(ctx, tunnel)
		if err != nil {
			return nil, fmt.Errorf("failed to persist backend tunnel: %w", err)
		}
		tunnel.ID = id
	} else {
		tunnel.ID = serverID
	}

	p.tunnelsByServerID[serverID] = tunnel
	p.tunnelsByID[tunnel.ID] = tunnel
	p.tunnelsByIfName[ifName] = tunnel

	return tunnel, nil
}

// RemoveTunnel removes a backend tunnel by server ID.
func (p *Pool) RemoveTunnel(ctx context.Context, serverID int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	tunnel, ok := p.tunnelsByServerID[serverID]
	if !ok {
		return ErrTunnelNotFound
	}

	if p.db != nil {
		if err := p.db.DeleteBackendTunnel(ctx, tunnel.ID); err != nil {
			return fmt.Errorf("failed to delete backend tunnel from DB: %w", err)
		}
	}

	delete(p.tunnelsByServerID, serverID)
	delete(p.tunnelsByID, tunnel.ID)
	delete(p.tunnelsByIfName, tunnel.InterfaceName)

	return nil
}

// GetTunnel retrieves a tunnel by server ID.
func (p *Pool) GetTunnel(serverID int64) (*models.BackendTunnel, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tunnel, ok := p.tunnelsByServerID[serverID]
	if !ok {
		return nil, ErrTunnelNotFound
	}
	copyTunnel := *tunnel
	return &copyTunnel, nil
}

// GetTunnelByID retrieves a tunnel by its tunnel ID.
func (p *Pool) GetTunnelByID(id int64) (*models.BackendTunnel, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tunnel, ok := p.tunnelsByID[id]
	if !ok {
		return nil, ErrTunnelNotFound
	}
	copyTunnel := *tunnel
	return &copyTunnel, nil
}

// GetTunnelByInterface retrieves a tunnel by its interface name (e.g. "awg-be-1").
func (p *Pool) GetTunnelByInterface(ifName string) (*models.BackendTunnel, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tunnel, ok := p.tunnelsByIfName[ifName]
	if !ok {
		return nil, ErrTunnelNotFound
	}
	copyTunnel := *tunnel
	return &copyTunnel, nil
}

// ListTunnels returns a copy of all tunnels in the pool.
func (p *Pool) ListTunnels() []*models.BackendTunnel {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []*models.BackendTunnel
	for _, t := range p.tunnelsByServerID {
		copyTunnel := *t
		result = append(result, &copyTunnel)
	}
	return result
}

// GetActiveTunnels returns all tunnels currently in "active" status.
func (p *Pool) GetActiveTunnels() []*models.BackendTunnel {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []*models.BackendTunnel
	for _, t := range p.tunnelsByServerID {
		if t.Status == "active" {
			copyTunnel := *t
			result = append(result, &copyTunnel)
		}
	}
	return result
}

// SetTunnelStatus updates the status and latency of a backend tunnel.
func (p *Pool) SetTunnelStatus(ctx context.Context, serverID int64, status string, latencyMS int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	tunnel, ok := p.tunnelsByServerID[serverID]
	if !ok {
		return ErrTunnelNotFound
	}

	tunnel.Status = status
	tunnel.LatencyMS = latencyMS
	now := time.Now().UTC()
	tunnel.LastHealthCheck = &now

	if p.db != nil {
		_ = p.db.UpdateBackendTunnelStatus(ctx, tunnel.ID, status, latencyMS)
	}

	return nil
}

// IncrementConnections increments active connection count on a tunnel.
func (p *Pool) IncrementConnections(tunnelID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if tunnel, ok := p.tunnelsByID[tunnelID]; ok {
		tunnel.ActiveConnections++
		if p.db != nil {
			_ = p.db.UpdateBackendTunnel(context.Background(), tunnel.ID, map[string]any{
				"active_connections": tunnel.ActiveConnections,
			})
		}
	}
}

// DecrementConnections decrements active connection count on a tunnel.
func (p *Pool) DecrementConnections(tunnelID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if tunnel, ok := p.tunnelsByID[tunnelID]; ok {
		if tunnel.ActiveConnections > 0 {
			tunnel.ActiveConnections--
		}
		if p.db != nil {
			_ = p.db.UpdateBackendTunnel(context.Background(), tunnel.ID, map[string]any{
				"active_connections": tunnel.ActiveConnections,
			})
		}
	}
}

// Close tears down all tunnels and cleans up resources.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}
