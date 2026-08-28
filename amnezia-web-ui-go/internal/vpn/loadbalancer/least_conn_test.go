package loadbalancer

import (
	"context"
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestLeastConnectionsBalancer(t *testing.T) {
	ctx := context.Background()
	caps := CapacityConfig{MaxTotalPeers: 100, MaxPeersPerBackend: 40}
	lb := NewLeastConnectionsBalancer(caps)

	if lb.GetAlgorithm() != models.LBLeastConnections {
		t.Errorf("GetAlgorithm mismatch: %s", lb.GetAlgorithm())
	}

	// 1. No active backends
	reqEmpty := &RoutingRequest{
		AvailableTunnels: []*models.BackendTunnel{
			{ID: 1, Status: "degraded", ActiveConnections: 0},
			{ID: 2, Status: "disabled", ActiveConnections: 0},
		},
	}
	if _, err := lb.SelectBackend(ctx, reqEmpty); err != ErrNoActiveBackends {
		t.Errorf("expected ErrNoActiveBackends, got %v", err)
	}

	// 2. Selection of lowest active connections
	tunnels := []*models.BackendTunnel{
		{ID: 1, Status: "active", ActiveConnections: 15, LatencyMS: 50},
		{ID: 2, Status: "active", ActiveConnections: 5, LatencyMS: 60},
		{ID: 3, Status: "active", ActiveConnections: 20, LatencyMS: 10},
	}
	lb.UpdateBackends(tunnels)

	req := &RoutingRequest{AvailableTunnels: tunnels}
	best, err := lb.SelectBackend(ctx, req)
	if err != nil || best.ID != 2 {
		t.Fatalf("expected tunnel 2 (5 connections), got %+v, err: %v", best, err)
	}

	// 3. Tie-breaker 1: Latency
	tunnelsTie := []*models.BackendTunnel{
		{ID: 1, Status: "active", ActiveConnections: 10, LatencyMS: 80},
		{ID: 2, Status: "active", ActiveConnections: 10, LatencyMS: 20}, // Lowest latency
		{ID: 3, Status: "active", ActiveConnections: 10, LatencyMS: 50},
	}
	bestTie, err := lb.SelectBackend(ctx, &RoutingRequest{AvailableTunnels: tunnelsTie})
	if err != nil || bestTie.ID != 2 {
		t.Fatalf("expected tunnel 2 on latency tie-break, got %+v, err: %v", bestTie, err)
	}

	// 4. Tie-breaker 2: ID
	tunnelsIDTie := []*models.BackendTunnel{
		{ID: 2, Status: "active", ActiveConnections: 10, LatencyMS: 20},
		{ID: 1, Status: "active", ActiveConnections: 10, LatencyMS: 20}, // Lowest ID
	}
	bestIDTie, err := lb.SelectBackend(ctx, &RoutingRequest{AvailableTunnels: tunnelsIDTie})
	if err != nil || bestIDTie.ID != 1 {
		t.Fatalf("expected tunnel 1 on ID tie-break, got %+v, err: %v", bestIDTie, err)
	}

	// 5. Global Capacity Limit
	tightCaps := CapacityConfig{MaxTotalPeers: 15}
	lbTight := NewLeastConnectionsBalancer(tightCaps)
	tunnelsOver := []*models.BackendTunnel{
		{ID: 1, Status: "active", ActiveConnections: 10},
		{ID: 2, Status: "active", ActiveConnections: 6},
	} // Total = 16 >= 15
	if _, err := lbTight.SelectBackend(ctx, &RoutingRequest{AvailableTunnels: tunnelsOver}); err != ErrCapacityExceeded {
		t.Errorf("expected ErrCapacityExceeded, got %v", err)
	}

	// Using stored tunnels when AvailableTunnels is empty
	lb.UpdateBackends(tunnels)
	bestStored, err := lb.SelectBackend(ctx, &RoutingRequest{})
	if err != nil || bestStored.ID != 2 {
		t.Fatalf("expected tunnel 2 from stored tunnels, got %+v, err: %v", bestStored, err)
	}
}
