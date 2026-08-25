package manager

import (
	"context"
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestRegistry(t *testing.T) {
	reg := NewRegistry()

	awgMgr := NewMockProtocolManager("awg")
	xrayMgr := NewMockProtocolManager("xray")

	reg.Register(awgMgr)
	reg.Register(xrayMgr)

	if mgr, ok := reg.Get("awg"); !ok || mgr == nil {
		t.Fatalf("expected to find awg manager")
	}

	// Legacy alias normalization check
	if mgr, ok := reg.Get("awg2"); !ok || mgr == nil {
		t.Fatalf("expected to find awg manager via alias awg2")
	}

	if _, ok := reg.Get("unknown"); ok {
		t.Errorf("expected not to find unknown protocol")
	}

	list := reg.List()
	if len(list) != 2 {
		t.Errorf("expected 2 registered managers, got %d", len(list))
	}
}

func TestMockProtocolManager(t *testing.T) {
	mgr := NewMockProtocolManager("awg")
	ctx := context.Background()
	server := &models.Server{ID: 1, Name: "Test Server", Host: "1.2.3.4"}

	if err := mgr.Install(ctx, server, nil); err != nil {
		t.Errorf("Install failed: %v", err)
	}

	clients, err := mgr.GetClients(ctx, server)
	if err != nil || len(clients) != 0 {
		t.Errorf("GetClients failed: %v", err)
	}

	client, err := mgr.AddClient(ctx, server, nil)
	if err != nil || client["client_id"] != "mock-client-1" {
		t.Errorf("AddClient failed: %v", err)
	}

	cfg, err := mgr.GetClientConfig(ctx, server, "mock-client-1")
	if err != nil || cfg == "" {
		t.Errorf("GetClientConfig failed: %v", err)
	}

	if err := mgr.RemoveClient(ctx, server, "mock-client-1"); err != nil {
		t.Errorf("RemoveClient failed: %v", err)
	}

	if err := mgr.Uninstall(ctx, server); err != nil {
		t.Errorf("Uninstall failed: %v", err)
	}

	// Nil server checks
	if err := mgr.Install(ctx, nil, nil); err == nil {
		t.Errorf("expected error on nil server")
	}
}
