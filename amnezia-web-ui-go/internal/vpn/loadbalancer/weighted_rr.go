package loadbalancer

import (
	"context"
	"sync"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// WeightedRoundRobinBalancer distributes traffic proportionally based on backend weights using smooth weighted round-robin.
type WeightedRoundRobinBalancer struct {
	mu             sync.Mutex
	weights        map[int64]int
	currentWeights map[int64]int
	caps           CapacityConfig
	tunnels        []*models.BackendTunnel
}

// NewWeightedRoundRobinBalancer creates a new weighted round-robin load balancer.
func NewWeightedRoundRobinBalancer(weights map[int64]int, caps CapacityConfig) *WeightedRoundRobinBalancer {
	wCopy := make(map[int64]int)
	for k, v := range weights {
		if v > 0 {
			wCopy[k] = v
		}
	}
	return &WeightedRoundRobinBalancer{
		weights:        wCopy,
		currentWeights: make(map[int64]int),
		caps:           caps,
	}
}

// UpdateBackends updates the internal list of available backend tunnels.
func (wb *WeightedRoundRobinBalancer) UpdateBackends(tunnels []*models.BackendTunnel) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.tunnels = tunnels
}

// GetAlgorithm returns the algorithm identifier.
func (wb *WeightedRoundRobinBalancer) GetAlgorithm() models.LoadBalancingAlgorithm {
	return models.LBWeighted
}

// SetWeights updates the server weight mappings.
func (wb *WeightedRoundRobinBalancer) SetWeights(weights map[int64]int) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	wb.weights = make(map[int64]int)
	for k, v := range weights {
		if v > 0 {
			wb.weights[k] = v
		}
	}
	wb.currentWeights = make(map[int64]int)
}

// SelectBackend selects the next backend according to the smooth weighted round-robin algorithm.
func (wb *WeightedRoundRobinBalancer) SelectBackend(ctx context.Context, req *RoutingRequest) (*models.BackendTunnel, error) {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	candidates := req.AvailableTunnels
	if len(candidates) == 0 {
		candidates = wb.tunnels
	}

	healthy := FilterHealthy(candidates, wb.caps.MaxPeersPerBackend)
	if len(healthy) == 0 {
		return nil, ErrNoActiveBackends
	}

	if wb.caps.MaxTotalPeers > 0 {
		var totalConnections int
		for _, t := range candidates {
			totalConnections += t.ActiveConnections
		}
		if totalConnections >= wb.caps.MaxTotalPeers {
			return nil, ErrCapacityExceeded
		}
	}

	// Smooth Weighted Round-Robin (Nginx algorithm)
	totalWeight := 0
	var best *models.BackendTunnel
	maxCurrentWeight := -1 << 31

	for _, t := range healthy {
		effectiveWeight, ok := wb.weights[t.ServerID]
		if !ok || effectiveWeight <= 0 {
			effectiveWeight = 100 // Default weight
		}
		totalWeight += effectiveWeight

		wb.currentWeights[t.ID] += effectiveWeight
		if wb.currentWeights[t.ID] > maxCurrentWeight {
			maxCurrentWeight = wb.currentWeights[t.ID]
			best = t
		}
	}

	if best != nil {
		wb.currentWeights[best.ID] -= totalWeight
	}

	return best, nil
}
