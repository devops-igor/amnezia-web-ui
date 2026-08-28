package forwarder

import (
	"context"
	"sync"
	"testing"
	"time"
)

type mockPacketDev struct {
	mu       sync.Mutex
	pkts     [][]byte
	notifyCh chan struct{}
}

func newMockPacketDev() *mockPacketDev {
	return &mockPacketDev{
		notifyCh: make(chan struct{}, 100),
	}
}

func (m *mockPacketDev) Read(p []byte) (int, error) {
	return 0, nil
}

func (m *mockPacketDev) Write(p []byte) (int, error) {
	m.mu.Lock()
	buf := make([]byte, len(p))
	copy(buf, p)
	m.pkts = append(m.pkts, buf)
	m.mu.Unlock()
	select {
	case m.notifyCh <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (m *mockPacketDev) Close() error {
	return nil
}

func (m *mockPacketDev) getPackets() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([][]byte, len(m.pkts))
	for i, p := range m.pkts {
		cp := make([]byte, len(p))
		copy(cp, p)
		copied[i] = cp
	}
	return copied
}

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

func TestForwarderRateLimitingThrottling(t *testing.T) {
	accountant := NewTrafficAccountant(nil, 0)
	fwd := NewForwarder(accountant, 10)

	sessID := "sess-rl-1"
	connID := "conn-rl-1"
	peerKey := "peer-rl-1"
	assignedIP := "10.100.0.25"
	backendID := int64(500)

	// Rate limit: 200 bytes per second (burst cap 200 bytes)
	fwd.RegisterSessionWithLimit(sessID, connID, peerKey, assignedIP, backendID, 200, 200)

	down, up, err := fwd.GetPeerRateLimit(peerKey)
	if err != nil || down != 200 || up != 200 {
		t.Fatalf("GetPeerRateLimit mismatch: down=%d, up=%d, err=%v", down, up, err)
	}

	// 1. Upstream test (Client -> Backend)
	pkt1 := make([]byte, 150)
	if err := fwd.RouteClientToBackend(peerKey, pkt1); err != nil {
		t.Fatalf("first packet under limit should succeed, got: %v", err)
	}

	// Second packet exceeds available tokens (50 remaining < 100)
	pkt2 := make([]byte, 100)
	if err := fwd.RouteClientToBackend(peerKey, pkt2); err != ErrRateLimitExceeded {
		t.Fatalf("expected ErrRateLimitExceeded, got: %v", err)
	}

	// Wait for replenishment: 600ms adds ~120 tokens (total ~170 tokens)
	time.Sleep(600 * time.Millisecond)

	pkt3 := make([]byte, 80)
	if err := fwd.RouteClientToBackend(peerKey, pkt3); err != nil {
		t.Fatalf("packet after refill should succeed, got: %v", err)
	}

	// 2. Downstream test (Backend -> Client)
	pktDown1 := make([]byte, 150)
	if err := fwd.RouteBackendToClient(backendID, pktDown1, assignedIP); err != nil {
		t.Fatalf("first downstream packet under limit should succeed, got: %v", err)
	}

	pktDown2 := make([]byte, 100)
	if err := fwd.RouteBackendToClient(backendID, pktDown2, assignedIP); err != ErrRateLimitExceeded {
		t.Fatalf("expected downstream ErrRateLimitExceeded, got: %v", err)
	}

	// 3. Update rate limit to unlimited (0)
	if err := fwd.SetPeerRateLimit(peerKey, 0, 0); err != nil {
		t.Fatalf("SetPeerRateLimit failed: %v", err)
	}

	pktLarge := make([]byte, 1000)
	if err := fwd.RouteClientToBackend(peerKey, pktLarge); err != nil {
		t.Fatalf("large packet after disabling rate limit should succeed, got: %v", err)
	}
	if err := fwd.RouteBackendToClient(backendID, pktLarge, assignedIP); err != nil {
		t.Fatalf("large downstream packet after disabling rate limit should succeed, got: %v", err)
	}

	// 4. Edge cases
	if _, _, err := fwd.GetPeerRateLimit("non-existent"); err != ErrSessionNotRegistered {
		t.Errorf("expected ErrSessionNotRegistered for ghost peer")
	}
	if err := fwd.SetPeerRateLimit("non-existent", 100, 100); err != ErrSessionNotRegistered {
		t.Errorf("expected ErrSessionNotRegistered on set ghost peer")
	}
}

func TestForwarderPacketPumps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	accountant := NewTrafficAccountant(nil, 0)
	fwd := NewForwarder(accountant, 10)

	clientDev := newMockPacketDev()
	backendDev := newMockPacketDev()
	peerDev := newMockPacketDev()

	backendID := int64(700)
	peerKey := "peer-pump-1"
	assignedIP := "10.100.0.50"

	fwd.AttachClientDevice(clientDev)
	fwd.AttachBackendDevice(backendID, backendDev)
	fwd.RegisterSession("s-pump", "c-pump", peerKey, assignedIP, backendID)

	// Start packet pumps
	fwd.StartPumps(ctx)
	// Double start noop
	fwd.StartPumps(ctx)

	// 1. Client -> Backend routing should be pumped to backendDev.Write
	pktClient := []byte("pumped-client-packet")
	if err := fwd.RouteClientToBackend(peerKey, pktClient); err != nil {
		t.Fatalf("RouteClientToBackend failed: %v", err)
	}

	select {
	case <-backendDev.notifyCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for backend pump to write packet")
	}

	bePackets := backendDev.getPackets()
	if len(bePackets) != 1 || string(bePackets[0]) != string(pktClient) {
		t.Fatalf("backend device packet mismatch: %+v", bePackets)
	}

	// 2. Backend -> Client routing should be pumped to default clientDev.Write
	pktBackend := []byte("pumped-backend-packet")
	if err := fwd.RouteBackendToClient(backendID, pktBackend, assignedIP); err != nil {
		t.Fatalf("RouteBackendToClient failed: %v", err)
	}

	select {
	case <-clientDev.notifyCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for client pump to write packet")
	}

	clPackets := clientDev.getPackets()
	if len(clPackets) != 1 || string(clPackets[0]) != string(pktBackend) {
		t.Fatalf("client device packet mismatch: %+v", clPackets)
	}

	// 3. Attach peer-specific device and test routing to it
	fwd.AttachPeerDevice(peerKey, peerDev)
	pktPeer := []byte("peer-specific-packet")
	if err := fwd.RouteBackendToClient(backendID, pktPeer, assignedIP); err != nil {
		t.Fatalf("RouteBackendToClient to peer dev failed: %v", err)
	}

	select {
	case <-peerDev.notifyCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for peer-specific device packet")
	}

	peerPackets := peerDev.getPackets()
	if len(peerPackets) != 1 || string(peerPackets[0]) != string(pktPeer) {
		t.Fatalf("peer device packet mismatch: %+v", peerPackets)
	}

	// Detach backend device
	fwd.DetachBackendDevice(backendID)

	// Stop pumps
	fwd.StopPumps()
	// Double stop noop
	fwd.StopPumps()

	if err := fwd.Stop(); err != nil {
		t.Fatalf("fwd.Stop failed: %v", err)
	}
}

func TestTokenBucketDirect(t *testing.T) {
	// Nil bucket allows all
	var nilTb *TokenBucket
	if !nilTb.Allow(1000) {
		t.Errorf("nil TokenBucket should allow all")
	}

	// Rate <= 0 returns nil
	tbZero := NewTokenBucket(0)
	if tbZero != nil {
		t.Errorf("expected nil token bucket for rate <= 0")
	}

	// Custom capacity
	tb := NewTokenBucket(100, 500)
	if !tb.Allow(400) {
		t.Errorf("expected Allow(400) on capacity 500")
	}
	if tb.Allow(200) {
		t.Errorf("expected false on Allow(200) when only 100 left")
	}
}
