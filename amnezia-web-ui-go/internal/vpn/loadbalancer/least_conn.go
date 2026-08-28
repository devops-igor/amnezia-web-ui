package loadbalancer

import (
	"context"
	"sync"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// LeastConnectionsBalancer routes connections to the active backend with the lowest active connections count.
type LeastConnectionsBalancer struct {
	mu      sync.RWMutex
	caps    CapacityConfig
	tunnels []*models.BackendTunnel
}

// NewLeastConnectionsBalancer creates a least-connections load balancer.
func NewLeastConnectionsBalancer(caps CapacityConfig) *LeastConnectionsBalancer {
	return &LeastConnectionsBalancer{
		caps: caps,
	}
}

// UpdateBackends updates the internal list of available backend tunnels.
func (lb *LeastConnectionsBalancer) UpdateBackends(tunnels []*models.BackendTunnel) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.tunnels = tunnels
}

// GetAlgorithm returns the algorithm identifier.
func (lb *LeastConnectionsBalancer) GetAlgorithm() models.LoadBalancingAlgorithm {
	return models.LBLeastConnections
}

// SelectBackend chooses the active backend with the minimum active connections.
func (lb *LeastConnectionsBalancer) SelectBackend(ctx context.Context, req *RoutingRequest) (*models.BackendTunnel, error) {
	lb.mu.RLock()
	candidates := req.AvailableTunnels
	if len(candidates) == 0 {
		candidates = lb.tunnels
	}
	caps := lb.caps
	lb.mu.RUnlock()

	healthy := FilterHealthy(candidates, caps.MaxPeersPerBackend)
	if len(healthy) == 0 {
		return nil, ErrNoActiveBackends
	}

	if caps.MaxTotalPeers > 0 {
		var totalConnections int
		for _, t := range candidates {
			totalConnections += t.ActiveConnections
		}
		if totalConnections >= caps.MaxTotalPeers {
			return nil, ErrCapacityExceeded
		}
	}

	var best *models.BackendTunnel
	for _, t := range healthy {
		if best == nil {
			best = t
			continue
		}
		if t.ActiveConnections < best.ActiveConnections {
			best = t
		} else if t.ActiveConnections == best.ActiveConnections {
			// Tie-breaker 1: lower latency
			if t.LatencyMS < best.LatencyMS {
				best = t
			} else if t.LatencyMS == best.LatencyMS && t.ID < best.ID {
				// Tie-breaker 2: lower ID for deterministic routing
				best = t
			}
		}
	}

	return best, nil
}
