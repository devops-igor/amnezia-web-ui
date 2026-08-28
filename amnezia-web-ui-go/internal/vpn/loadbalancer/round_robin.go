package loadbalancer

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// RoundRobinBalancer uniformly distributes connections across active backends in sequential order.
type RoundRobinBalancer struct {
	mu      sync.RWMutex
	counter atomic.Uint64
	caps    CapacityConfig
	tunnels []*models.BackendTunnel
}

// NewRoundRobinBalancer creates a round-robin load balancer.
func NewRoundRobinBalancer(caps CapacityConfig) *RoundRobinBalancer {
	return &RoundRobinBalancer{
		caps: caps,
	}
}

// UpdateBackends updates the internal list of available backend tunnels.
func (rb *RoundRobinBalancer) UpdateBackends(tunnels []*models.BackendTunnel) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.tunnels = tunnels
}

// GetAlgorithm returns the algorithm identifier.
func (rb *RoundRobinBalancer) GetAlgorithm() models.LoadBalancingAlgorithm {
	return models.LBRoundRobin
}

// SelectBackend chooses the next healthy backend sequentially.
func (rb *RoundRobinBalancer) SelectBackend(ctx context.Context, req *RoutingRequest) (*models.BackendTunnel, error) {
	rb.mu.RLock()
	candidates := req.AvailableTunnels
	if len(candidates) == 0 {
		candidates = rb.tunnels
	}
	caps := rb.caps
	rb.mu.RUnlock()

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

	idx := rb.counter.Add(1) - 1
	n := uint64(len(healthy))
	selected := healthy[int(idx%n)] // #nosec G115 - idx % n is strictly bounded by len(healthy)

	return selected, nil
}
