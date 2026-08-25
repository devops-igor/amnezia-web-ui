package vpn

import (
	"context"
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestLeastConnectionsLoadBalancer(t *testing.T) {
	lb := NewLeastConnectionsLoadBalancer()
	ctx := context.Background()

	// No tunnels
	_, err := lb.SelectBackend(ctx, nil)
	if err == nil {
		t.Errorf("expected error with no tunnels")
	}

	// Degraded / disabled tunnels only
	tunnels := []*BackendTunnel{
		{ID: 1, InterfaceName: "awg-be-1", Status: TunnelStatusDegraded, ActiveConnections: 0},
		{ID: 2, InterfaceName: "awg-be-2", Status: TunnelStatusDisabled, ActiveConnections: 0},
	}
	_, err = lb.SelectBackend(ctx, tunnels)
	if err == nil {
		t.Errorf("expected error when no active tunnels exist")
	}

	// Active tunnels selection
	tunnels = append(tunnels,
		&BackendTunnel{ID: 3, InterfaceName: "awg-be-3", Status: TunnelStatusActive, ActiveConnections: 5},
		&BackendTunnel{ID: 4, InterfaceName: "awg-be-4", Status: TunnelStatusActive, ActiveConnections: 2},
		&BackendTunnel{ID: 5, InterfaceName: "awg-be-5", Status: TunnelStatusActive, ActiveConnections: 8},
	)

	best, err := lb.SelectBackend(ctx, tunnels)
	if err != nil {
		t.Fatalf("SelectBackend failed: %v", err)
	}

	if best.ID != 4 {
		t.Errorf("expected tunnel ID 4 (2 active connections), got ID %d (%d connections)", best.ID, best.ActiveConnections)
	}
}

func TestVPNService(t *testing.T) {
	svc := NewService(models.LBLeastConnections)
	ctx := context.Background()

	tunnels := []*BackendTunnel{
		{ID: 1, InterfaceName: "awg-be-1", Status: TunnelStatusActive, ActiveConnections: 10},
		{ID: 2, InterfaceName: "awg-be-2", Status: TunnelStatusActive, ActiveConnections: 3},
	}

	best, err := svc.SelectTunnel(ctx, tunnels)
	if err != nil {
		t.Fatalf("SelectTunnel failed: %v", err)
	}
	if best.ID != 2 {
		t.Errorf("expected tunnel 2, got %d", best.ID)
	}
}
