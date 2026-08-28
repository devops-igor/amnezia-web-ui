package forwarder

import (
	"context"
	"testing"
)

func TestForwarderBasicRouting(t *testing.T) {
	accountant := NewTrafficAccountant(nil, 0)
	fwd := NewForwarder(accountant, 5)

	sessID := "sess-fwd-1"
	connID := "conn-fwd-1"
	peerKey := "peer-fwd-1"
	assignedIP := "10.100.0.10"
	backendID := int64(100)

	// 1. Unregistered routing returns error
	if err := fwd.RouteClientToBackend(peerKey, []byte("test")); err != ErrSessionNotRegistered {
		t.Errorf("expected ErrSessionNotRegistered, got %v", err)
	}
	if err := fwd.RouteBackendToClient(backendID, []byte("test"), assignedIP); err != ErrSessionNotRegistered {
		t.Errorf("expected ErrSessionNotRegistered, got %v", err)
	}

	// 2. Register Session
	fwd.RegisterSession(sessID, connID, peerKey, assignedIP, backendID)

	beChan, ok := fwd.GetBackendPacketChannel(backendID)
	if !ok || beChan == nil {
		t.Fatalf("expected backend packet channel for %d", backendID)
	}

	clientChan, ok := fwd.GetClientPacketChannel(peerKey)
	if !ok || clientChan == nil {
		t.Fatalf("expected client packet channel for %s", peerKey)
	}

	// 3. Client -> Backend routing
	pktOut := []byte("packet-from-client")
	if err := fwd.RouteClientToBackend(peerKey, pktOut); err != nil {
		t.Fatalf("RouteClientToBackend failed: %v", err)
	}

	select {
	case receivedPkt := <-beChan:
		if string(receivedPkt) != string(pktOut) {
			t.Errorf("received packet mismatch: %s", string(receivedPkt))
		}
	default:
		t.Fatalf("packet not received on backend queue")
	}

	// 4. Backend -> Client routing
	pktIn := []byte("packet-from-backend")
	if err := fwd.RouteBackendToClient(backendID, pktIn, assignedIP); err != nil {
		t.Fatalf("RouteBackendToClient failed: %v", err)
	}

	select {
	case receivedPkt := <-clientChan:
		if string(receivedPkt) != string(pktIn) {
			t.Errorf("received packet mismatch: %s", string(receivedPkt))
		}
	default:
		t.Fatalf("packet not received on client queue")
	}

	// 5. Check stats
	rx, tx, active := fwd.GetStats()
	if rx != int64(len(pktOut)) || tx != int64(len(pktIn)) || active != 1 {
		t.Errorf("GetStats mismatch: rx=%d, tx=%d, active=%d", rx, tx, active)
	}
}

func TestForwarderEdgeCasesAndLifecycle(t *testing.T) {
	ctx := context.Background()
	accountant := NewTrafficAccountant(nil, 0)
	fwd := NewForwarder(accountant, 5)

	sessID := "sess-fwd-2"
	connID := "conn-fwd-2"
	peerKey := "peer-fwd-2"
	assignedIP := "10.100.0.12"
	backendID := int64(100)

	fwd.RegisterSession(sessID, connID, peerKey, assignedIP, backendID)

	// Update Backend Route
	newBackendID := int64(200)
	if err := fwd.UpdateSessionBackend(peerKey, newBackendID); err != nil {
		t.Fatalf("UpdateSessionBackend failed: %v", err)
	}
	if err := fwd.UpdateSessionBackend("ghost", newBackendID); err != ErrSessionNotRegistered {
		t.Errorf("expected ErrSessionNotRegistered for ghost, got %v", err)
	}

	newBEChan, ok := fwd.GetBackendPacketChannel(newBackendID)
	if !ok || newBEChan == nil {
		t.Fatalf("expected new backend channel")
	}

	pktFailover := []byte("packet-failover")
	if err := fwd.RouteClientToBackend(peerKey, pktFailover); err != nil {
		t.Fatalf("RouteClientToBackend after failover failed: %v", err)
	}
	select {
	case p := <-newBEChan:
		if string(p) != string(pktFailover) {
			t.Errorf("mismatch failover packet: %s", string(p))
		}
	default:
		t.Fatalf("packet not on new backend queue")
	}

	// Queue Full test
	fwdSmall := NewForwarder(accountant, 1)
	fwdSmall.RegisterSession("s2", "c2", "peer2", "10.100.0.11", 300)
	_ = fwdSmall.RouteClientToBackend("peer2", []byte("p1"))
	if err := fwdSmall.RouteClientToBackend("peer2", []byte("p2")); err != ErrQueueFull {
		t.Errorf("expected ErrQueueFull on client to backend overflow, got %v", err)
	}
	_ = fwdSmall.RouteBackendToClient(300, []byte("p1"), "10.100.0.11")
	if err := fwdSmall.RouteBackendToClient(300, []byte("p2"), "10.100.0.11"); err != ErrQueueFull {
		t.Errorf("expected ErrQueueFull on backend to client overflow, got %v", err)
	}

	// Unregister
	fwd.UnregisterSession(peerKey)
	if _, ok := fwd.GetClientPacketChannel(peerKey); ok {
		t.Errorf("expected client channel to be unregistered")
	}

	// Lifecycle Start and Stop
	fwd.Start(ctx)
	if !fwd.IsRunning() {
		t.Errorf("expected forwarder running")
	}
	if err := fwd.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if fwd.IsRunning() {
		t.Errorf("expected forwarder not running after Stop")
	}
}
