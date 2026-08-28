package loadbalancer

import (
	"context"
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestRoundRobinBalancer(t *testing.T) {
	ctx := context.Background()
	caps := CapacityConfig{MaxTotalPeers: 100, MaxPeersPerBackend: 50}
	lb := NewRoundRobinBalancer(caps)

	if lb.GetAlgorithm() != models.LBRoundRobin {
		t.Errorf("GetAlgorithm mismatch: %s", lb.GetAlgorithm())
	}

	tunnels := []*models.BackendTunnel{
		{ID: 1, Status: "active"},
		{ID: 2, Status: "degraded"}, // Skipped
		{ID: 3, Status: "active"},
		{ID: 4, Status: "active"},
	}

	// Sequence across 6 requests should be: 1 -> 3 -> 4 -> 1 -> 3 -> 4
	expectedIDs := []int64{1, 3, 4, 1, 3, 4}
	for i, expID := range expectedIDs {
		req := &RoutingRequest{AvailableTunnels: tunnels}
		selected, err := lb.SelectBackend(ctx, req)
		if err != nil || selected.ID != expID {
			t.Fatalf("step %d: expected tunnel ID %d, got %+v (err: %v)", i, expID, selected, err)
		}
	}

	// No active backends
	if _, err := lb.SelectBackend(ctx, &RoutingRequest{AvailableTunnels: nil}); err != ErrNoActiveBackends {
		t.Errorf("expected ErrNoActiveBackends, got %v", err)
	}

	// Capacity exceeded
	tightCaps := CapacityConfig{MaxTotalPeers: 5}
	lbTight := NewRoundRobinBalancer(tightCaps)
	tunnelsOver := []*models.BackendTunnel{
		{ID: 1, Status: "active", ActiveConnections: 6},
	}
	if _, err := lbTight.SelectBackend(ctx, &RoutingRequest{AvailableTunnels: tunnelsOver}); err != ErrCapacityExceeded {
		t.Errorf("expected ErrCapacityExceeded, got %v", err)
	}

	// Stored tunnels
	lb.UpdateBackends(tunnels)
	storedSelected, err := lb.SelectBackend(ctx, &RoutingRequest{})
	if err != nil || storedSelected == nil {
		t.Fatalf("expected selection from stored tunnels, got %v, err: %v", storedSelected, err)
	}
}
