package endpoint

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestChannelPacketDevice(t *testing.T) {
	dev := NewChannelPacketDevice("test-tun", 1420, 10)
	if dev.Name() != "test-tun" || dev.MTU() != 1420 {
		t.Errorf("device property mismatch: name=%s, mtu=%d", dev.Name(), dev.MTU())
	}

	pkt := []byte("hello-vpn-packet")
	if err := dev.InjectPacket(pkt); err != nil {
		t.Fatalf("InjectPacket failed: %v", err)
	}

	readBuf := make([]byte, 1500)
	n, err := dev.Read(readBuf)
	if err != nil || n != len(pkt) || string(readBuf[:n]) != string(pkt) {
		t.Fatalf("Read mismatch: n=%d, err=%v, data=%s", n, err, string(readBuf[:n]))
	}

	// Test Write and ReceivePacket
	writePkt := []byte("response-vpn-packet")
	n, err = dev.Write(writePkt)
	if err != nil || n != len(writePkt) {
		t.Fatalf("Write failed: %v", err)
	}

	recvPkt, err := dev.ReceivePacket()
	if err != nil || string(recvPkt) != string(writePkt) {
		t.Fatalf("ReceivePacket mismatch: %s, err=%v", string(recvPkt), err)
	}

	// Close device
	if err := dev.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Operations after close
	if err := dev.InjectPacket(pkt); err == nil {
		t.Errorf("expected error injecting to closed device")
	}
	if _, err := dev.Write(pkt); err == nil {
		t.Errorf("expected error writing to closed device")
	}
	if _, err := dev.Read(readBuf); err == nil {
		t.Errorf("expected error reading from closed device")
	}
	if _, err := dev.ReceivePacket(); err == nil {
		t.Errorf("expected error receiving from closed device")
	}
}

func getFreeUDPPort(t *testing.T) int {
	t.Helper()
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ResolveUDPAddr failed: %v", err)
	}
	tempConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatalf("ListenUDP failed: %v", err)
	}
	port := tempConn.LocalAddr().(*net.UDPAddr).Port
	_ = tempConn.Close()
	return port
}

func TestEndpointListenerBasic(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	cfg := ListenerConfig{
		ListenPort:  getFreeUDPPort(t),
		SubnetCIDR:  "10.100.0.0/24",
		MTU:         1420,
		IdleTimeout: 1 * time.Minute,
	}

	el, err := NewListener(cfg, db, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	if el.IPAM() == nil || el.SessionManager() == nil {
		t.Errorf("expected initialized IPAM and SessionManager")
	}

	customDev := NewChannelPacketDevice("mock0", 1420, 100)
	el.SetPacketDevice(customDev)

	// Start
	if err := el.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !el.IsRunning() {
		t.Errorf("expected IsRunning() to be true")
	}
	if el.GetListenAddr() == nil {
		t.Errorf("GetListenAddr() is nil")
	}

	// Double start should error
	if err := el.Start(ctx); err == nil {
		t.Errorf("expected error on double Start")
	}

	// Stop
	if err := el.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if el.IsRunning() {
		t.Errorf("expected IsRunning() to be false after Stop")
	}
	if err := el.Stop(); err != nil {
		t.Errorf("double Stop failed: %v", err)
	}
}

func TestEndpointListenerRegistrationAndDrain(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	cfg := ListenerConfig{
		ListenPort:  getFreeUDPPort(t),
		SubnetCIDR:  "10.100.0.0/24",
		MTU:         1420,
		IdleTimeout: 1 * time.Minute,
	}

	el, err := NewListener(cfg, db, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewListener failed: %v", err)
	}

	sID, _ := db.CreateServer(ctx, &models.Server{Name: "VPN Host", Host: "10.0.0.1"})
	tID, _ := db.CreateBackendTunnel(ctx, &models.BackendTunnel{
		ServerID:      sID,
		InterfaceName: "awg-be-1",
		PublicKey:     "pubkey",
		PrivateKey:    "privkey",
		Endpoint:      "10.0.0.1:51820",
	})
	uID, _ := db.CreateUser(ctx, &models.User{Username: "el_user", Enabled: true})
	peerKey := "client-public-key-test"
	_, _ = db.CreateConnection(ctx, &models.UserConnection{
		UserID:   uID,
		ServerID: sID,
		Protocol: "awg",
		ClientID: peerKey,
	})

	// Authenticate and Register Peer
	sess, err := el.AuthenticateAndRegisterPeer(ctx, peerKey, tID)
	if err != nil {
		t.Fatalf("AuthenticateAndRegisterPeer failed: %v", err)
	}
	if sess.PeerPublicKey != peerKey || sess.AssignedIP == "" {
		t.Errorf("session mismatch: %+v", sess)
	}

	// Non-registered peer
	if _, err := el.AuthenticateAndRegisterPeer(ctx, "unregistered", tID); err == nil {
		t.Errorf("expected error for unregistered peer")
	}

	// Traffic stats
	el.RecordTraffic(100, 200)
	rx, tx, active := el.GetStats()
	if rx != 100 || tx != 200 || active != 1 {
		t.Errorf("GetStats mismatch: rx=%d, tx=%d, active=%d", rx, tx, active)
	}

	// Disconnect Peer
	if err := el.DisconnectPeer(ctx, peerKey); err != nil {
		t.Fatalf("DisconnectPeer failed: %v", err)
	}
	if _, _, active := el.GetStats(); active != 0 {
		t.Errorf("expected 0 active sessions after disconnect, got %d", active)
	}
	if err := el.DisconnectPeer(ctx, "ghost"); err != ErrPeerNotFound {
		t.Errorf("expected ErrPeerNotFound on ghost peer, got %v", err)
	}

	// Reconnect and Drain
	_, _ = el.AuthenticateAndRegisterPeer(ctx, peerKey, tID)
	if err := el.Drain(ctx, 2*time.Second); err != nil {
		t.Fatalf("Drain failed: %v", err)
	}
	if !el.IsDraining() {
		t.Errorf("expected IsDraining() to be true")
	}

	// Registering while draining should reject
	if _, err := el.AuthenticateAndRegisterPeer(ctx, peerKey, tID); err == nil {
		t.Errorf("expected new registration rejected while draining")
	}
}
