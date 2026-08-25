package vpn

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// BackendTunnelStatus represents the runtime status of an in-process backend AWG tunnel.
type BackendTunnelStatus string

const (
	TunnelStatusConnecting BackendTunnelStatus = "connecting"
	TunnelStatusActive     BackendTunnelStatus = "active"
	TunnelStatusDegraded   BackendTunnelStatus = "degraded"
	TunnelStatusDisabled   BackendTunnelStatus = "disabled"
)

// SessionStatus represents the status of a user VPN session connected to the portal.
type SessionStatus string

const (
	SessionStatusConnected    SessionStatus = "connected"
	SessionStatusDisconnected SessionStatus = "disconnected"
	SessionStatusDraining     SessionStatus = "draining"
)

// BackendTunnel represents a tunnel connecting the portal to a remote VPN backend server.
type BackendTunnel struct {
	ID                int64               `json:"id"`
	ServerID          int64               `json:"server_id"`
	InterfaceName     string              `json:"interface_name"`
	PublicKey         string              `json:"public_key"`
	PrivateKey        string              `json:"private_key,omitempty"`
	Endpoint          string              `json:"endpoint"`
	Status            BackendTunnelStatus `json:"status"`
	LastHealthCheck   *time.Time          `json:"last_health_check,omitempty"`
	LatencyMs         int                 `json:"latency_ms"`
	ActiveConnections int                 `json:"active_connections"`
	CreatedAt         time.Time           `json:"created_at"`
}

// Session represents an active peer connected to the portal VPN endpoint.
type Session struct {
	ID              string        `json:"id"`
	UserID          string        `json:"user_id"`
	BackendTunnelID int64         `json:"backend_tunnel_id"`
	PeerPublicKey   string        `json:"peer_public_key"`
	AssignedIP      string        `json:"assigned_ip"`
	ConnectedAt     time.Time     `json:"connected_at"`
	LastSeen        time.Time     `json:"last_seen"`
	RxBytes         int64         `json:"rx_bytes"`
	TxBytes         int64         `json:"tx_bytes"`
	Status          SessionStatus `json:"status"`
}

// LoadBalancer selects an optimal backend tunnel for incoming user connections.
type LoadBalancer interface {
	SelectBackend(ctx context.Context, tunnels []*BackendTunnel) (*BackendTunnel, error)
}

// LeastConnectionsLoadBalancer implements least-connections routing.
type LeastConnectionsLoadBalancer struct {
	mu sync.RWMutex
}

// NewLeastConnectionsLoadBalancer creates a new least-connections load balancer.
func NewLeastConnectionsLoadBalancer() *LeastConnectionsLoadBalancer {
	return &LeastConnectionsLoadBalancer{}
}

// SelectBackend chooses the active backend with the minimum active connections.
func (lb *LeastConnectionsLoadBalancer) SelectBackend(ctx context.Context, tunnels []*BackendTunnel) (*BackendTunnel, error) {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	var best *BackendTunnel
	for _, t := range tunnels {
		if t.Status != TunnelStatusActive {
			continue
		}
		if best == nil || t.ActiveConnections < best.ActiveConnections {
			best = t
		}
	}

	if best == nil {
		return nil, errors.New("no active backend tunnels available")
	}

	return best, nil
}

// Service coordinates VPN endpoint and tunnel lifecycle.
type Service struct {
	mu        sync.RWMutex
	algorithm models.LoadBalancingAlgorithm
	lb        LoadBalancer
}

// NewService creates a VPN service.
func NewService(algo models.LoadBalancingAlgorithm) *Service {
	return &Service{
		algorithm: algo,
		lb:        NewLeastConnectionsLoadBalancer(),
	}
}

// SelectTunnel picks a backend tunnel using the configured load balancing algorithm.
func (s *Service) SelectTunnel(ctx context.Context, tunnels []*BackendTunnel) (*BackendTunnel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lb == nil {
		return nil, fmt.Errorf("load balancer not initialized")
	}
	return s.lb.SelectBackend(ctx, tunnels)
}
