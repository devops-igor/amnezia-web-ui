package loadbalancer

import (
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestFilterHealthy(t *testing.T) {
	tunnels := []*models.BackendTunnel{
		nil,
		{ID: 1, InterfaceName: "awg-be-1", Status: "active", ActiveConnections: 10},
		{ID: 2, InterfaceName: "awg-be-2", Status: "degraded", ActiveConnections: 0},
		{ID: 3, InterfaceName: "awg-be-3", Status: "disabled", ActiveConnections: 0},
		{ID: 4, InterfaceName: "awg-be-4", Status: "ACTIVE", ActiveConnections: 100},
	}

	// No per-backend limit
	filtered := FilterHealthy(tunnels, 0)
	if len(filtered) != 2 || filtered[0].ID != 1 || filtered[1].ID != 4 {
		t.Errorf("expected 2 active tunnels, got len=%d", len(filtered))
	}

	// Per-backend limit 50 (tunnel 4 exceeds limit)
	filteredLimited := FilterHealthy(tunnels, 50)
	if len(filteredLimited) != 1 || filteredLimited[0].ID != 1 {
		t.Errorf("expected 1 active tunnel under capacity, got len=%d", len(filteredLimited))
	}
}

func TestNewLoadBalancerFactory(t *testing.T) {
	caps := CapacityConfig{MaxTotalPeers: 100, MaxPeersPerBackend: 50}

	// Least connections
	lb1, err := NewLoadBalancer(models.LBLeastConnections, nil, caps)
	if err != nil || lb1.GetAlgorithm() != models.LBLeastConnections {
		t.Errorf("expected LBLeastConnections, got %v, err: %v", lb1, err)
	}

	// Default empty
	lbDef, err := NewLoadBalancer("", nil, caps)
	if err != nil || lbDef.GetAlgorithm() != models.LBLeastConnections {
		t.Errorf("expected default LBLeastConnections, got %v, err: %v", lbDef, err)
	}

	// Weighted
	lb2, err := NewLoadBalancer(models.LBWeighted, map[int64]int{1: 50}, caps)
	if err != nil || lb2.GetAlgorithm() != models.LBWeighted {
		t.Errorf("expected LBWeighted, got %v, err: %v", lb2, err)
	}

	// Round-Robin
	lb3, err := NewLoadBalancer(models.LBRoundRobin, nil, caps)
	if err != nil || lb3.GetAlgorithm() != models.LBRoundRobin {
		t.Errorf("expected LBRoundRobin, got %v, err: %v", lb3, err)
	}

	// Invalid algorithm
	if _, err := NewLoadBalancer("unknown-algo", nil, caps); err != ErrInvalidAlgorithm {
		t.Errorf("expected ErrInvalidAlgorithm, got %v", err)
	}
}
