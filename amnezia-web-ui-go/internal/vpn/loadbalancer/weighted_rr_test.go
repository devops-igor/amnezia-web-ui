package loadbalancer

import (
	"context"
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestWeightedRoundRobinBalancer(t *testing.T) {
	ctx := context.Background()
	weights := map[int64]int{
		1: 50, // Server 1: 50%
		2: 25, // Server 2: 25%
		3: 25, // Server 3: 25%
	}
	caps := CapacityConfig{MaxTotalPeers: 1000, MaxPeersPerBackend: 500}
	lb := NewWeightedRoundRobinBalancer(weights, caps)

	if lb.GetAlgorithm() != models.LBWeighted {
		t.Errorf("GetAlgorithm mismatch: %s", lb.GetAlgorithm())
	}

	tunnels := []*models.BackendTunnel{
		{ID: 101, ServerID: 1, Status: "active"},
		{ID: 102, ServerID: 2, Status: "active"},
		{ID: 103, ServerID: 3, Status: "active"},
	}

	// Over 100 requests, check distribution ratio
	counts := make(map[int64]int)
	for i := 0; i < 100; i++ {
		req := &RoutingRequest{AvailableTunnels: tunnels}
		selected, err := lb.SelectBackend(ctx, req)
		if err != nil {
			t.Fatalf("SelectBackend failed at %d: %v", i, err)
		}
		counts[selected.ServerID]++
	}

	if counts[1] != 50 || counts[2] != 25 || counts[3] != 25 {
		t.Errorf("unexpected WRR distribution: server 1=%d, server 2=%d, server 3=%d (expected 50, 25, 25)",
			counts[1], counts[2], counts[3])
	}

	// Dynamic weight change
	newWeights := map[int64]int{
		1: 80,
		2: 20,
		3: 0, // Ignored
	}
	lb.SetWeights(newWeights)

	tunnels2 := []*models.BackendTunnel{
		{ID: 101, ServerID: 1, Status: "active"},
		{ID: 102, ServerID: 2, Status: "active"},
	}
	counts2 := make(map[int64]int)
	for i := 0; i < 100; i++ {
		req := &RoutingRequest{AvailableTunnels: tunnels2}
		selected, err := lb.SelectBackend(ctx, req)
		if err != nil {
			t.Fatalf("SelectBackend failed at %d: %v", i, err)
		}
		counts2[selected.ServerID]++
	}

	if counts2[1] != 80 || counts2[2] != 20 {
		t.Errorf("unexpected updated WRR distribution: 1=%d, 2=%d (expected 80, 20)", counts2[1], counts2[2])
	}

	// No active backends
	if _, err := lb.SelectBackend(ctx, &RoutingRequest{AvailableTunnels: nil}); err != ErrNoActiveBackends {
		t.Errorf("expected ErrNoActiveBackends, got %v", err)
	}

	// Capacity exceeded
	tightCaps := CapacityConfig{MaxTotalPeers: 10}
	lbTight := NewWeightedRoundRobinBalancer(weights, tightCaps)
	tunnelsOver := []*models.BackendTunnel{
		{ID: 101, ServerID: 1, Status: "active", ActiveConnections: 12},
	}
	if _, err := lbTight.SelectBackend(ctx, &RoutingRequest{AvailableTunnels: tunnelsOver}); err != ErrCapacityExceeded {
		t.Errorf("expected ErrCapacityExceeded, got %v", err)
	}

	// Stored tunnels
	lb.UpdateBackends(tunnels2)
	storedSelected, err := lb.SelectBackend(ctx, &RoutingRequest{})
	if err != nil || storedSelected == nil {
		t.Fatalf("expected selection from stored tunnels, got %v, err: %v", storedSelected, err)
	}
}
