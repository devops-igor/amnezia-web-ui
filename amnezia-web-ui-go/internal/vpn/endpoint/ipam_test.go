package endpoint

import (
	"fmt"
	"net"
	"testing"
)

func TestIPAMInitialization(t *testing.T) {
	// Valid /16
	ipam, err := NewIPAM("10.100.0.0/16")
	if err != nil {
		t.Fatalf("NewIPAM failed: %v", err)
	}
	if ipam.Gateway().String() != "10.100.0.1" {
		t.Errorf("expected gateway 10.100.0.1, got %s", ipam.Gateway().String())
	}
	if ipam.TotalAvailable() != 65533 { // 65536 - 3 (network, gateway, broadcast)
		t.Errorf("expected 65533 available IPs, got %d", ipam.TotalAvailable())
	}
	if ipam.CIDR() != "10.100.0.0/16" {
		t.Errorf("CIDR() mismatch: %s", ipam.CIDR())
	}
	if ipam.Subnet() == nil {
		t.Errorf("Subnet() is nil")
	}

	// Valid /24
	ipam24, err := NewIPAM("192.168.50.0/24")
	if err != nil {
		t.Fatalf("NewIPAM /24 failed: %v", err)
	}
	if ipam24.Gateway().String() != "192.168.50.1" {
		t.Errorf("expected gateway 192.168.50.1, got %s", ipam24.Gateway().String())
	}
	if ipam24.TotalAvailable() != 253 { // 256 - 3
		t.Errorf("expected 253 available IPs, got %d", ipam24.TotalAvailable())
	}

	// Invalid CIDRs
	if _, err := NewIPAM("invalid-cidr"); err != ErrInvalidSubnet {
		t.Errorf("expected ErrInvalidSubnet, got %v", err)
	}
	if _, err := NewIPAM("2001:db8::/64"); err != ErrInvalidSubnet {
		t.Errorf("expected ErrInvalidSubnet for IPv6, got %v", err)
	}
	if _, err := NewIPAM("10.0.0.0/31"); err == nil {
		t.Errorf("expected error for /31 subnet")
	}
}

func TestIPAMAllocationAndRelease(t *testing.T) {
	ipam, err := NewIPAM("10.100.0.0/24")
	if err != nil {
		t.Fatalf("NewIPAM failed: %v", err)
	}

	// Empty peer key
	if _, err := ipam.Allocate(""); err == nil {
		t.Errorf("expected error for empty peer key")
	}

	// Allocate peer 1
	ip1, err := ipam.Allocate("peer1")
	if err != nil {
		t.Fatalf("Allocate peer1 failed: %v", err)
	}
	if ip1.String() != "10.100.0.2" {
		t.Errorf("expected first IP to be 10.100.0.2, got %s", ip1.String())
	}
	if !ipam.IsAllocated(ip1) {
		t.Errorf("expected IsAllocated(ip1) to be true")
	}
	if ipam.AllocatedCount() != 1 {
		t.Errorf("expected allocated count 1, got %d", ipam.AllocatedCount())
	}

	// Same peer should get same IP
	ip1Repeat, err := ipam.Allocate("peer1")
	if err != nil || !ip1Repeat.Equal(ip1) {
		t.Errorf("expected repeat allocation to return %s, got %v (err: %v)", ip1, ip1Repeat, err)
	}

	// Allocate peer 2
	ip2, err := ipam.Allocate("peer2")
	if err != nil {
		t.Fatalf("Allocate peer2 failed: %v", err)
	}
	if ip2.String() != "10.100.0.3" {
		t.Errorf("expected second IP to be 10.100.0.3, got %s", ip2.String())
	}

	// GetAssignedIP
	retrievedIP, ok := ipam.GetAssignedIP("peer2")
	if !ok || !retrievedIP.Equal(ip2) {
		t.Errorf("GetAssignedIP(peer2) mismatch: %v (ok: %v)", retrievedIP, ok)
	}
	if _, ok := ipam.GetAssignedIP("ghost"); ok {
		t.Errorf("expected ghost peer to not have assigned IP")
	}

	// Release peer 1
	if err := ipam.Release("peer1"); err != nil {
		t.Fatalf("Release peer1 failed: %v", err)
	}
	if ipam.IsAllocated(ip1) {
		t.Errorf("expected ip1 to be released")
	}
	if ipam.AllocatedCount() != 1 {
		t.Errorf("expected allocated count 1, got %d", ipam.AllocatedCount())
	}
	if err := ipam.Release("peer1"); err != ErrPeerNotFoundInIPAM {
		t.Errorf("expected ErrPeerNotFoundInIPAM releasing non-existent lease, got %v", err)
	}

	// ReleaseIP peer 2
	if err := ipam.ReleaseIP(ip2); err != nil {
		t.Fatalf("ReleaseIP peer2 failed: %v", err)
	}
	if ipam.IsAllocated(ip2) {
		t.Errorf("expected ip2 to be released")
	}
	if ipam.AllocatedCount() != 0 {
		t.Errorf("expected allocated count 0, got %d", ipam.AllocatedCount())
	}
	if err := ipam.ReleaseIP(ip2); err != ErrPeerNotFoundInIPAM {
		t.Errorf("expected ErrPeerNotFoundInIPAM releasing non-existent IP, got %v", err)
	}
	if err := ipam.ReleaseIP(net.ParseIP("2001:db8::1")); err != ErrInvalidSubnet {
		t.Errorf("expected ErrInvalidSubnet for IPv6 ReleaseIP, got %v", err)
	}
}

func TestIPAMExhaustion(t *testing.T) {
	// Small /29 subnet: 8 total IPs. Usable: .2, .3, .4, .5, .6 (5 usable IPs)
	ipam, err := NewIPAM("10.0.0.0/29")
	if err != nil {
		t.Fatalf("NewIPAM /29 failed: %v", err)
	}
	if ipam.TotalAvailable() != 5 {
		t.Fatalf("expected 5 available IPs in /29, got %d", ipam.TotalAvailable())
	}

	for i := 0; i < 5; i++ {
		peer := fmt.Sprintf("peer-%d", i)
		ip, err := ipam.Allocate(peer)
		if err != nil {
			t.Fatalf("Allocate(%s) failed: %v", peer, err)
		}
		expectedLastByte := byte(i + 2)
		if ip[3] != expectedLastByte {
			t.Errorf("expected IP last byte %d, got %d", expectedLastByte, ip[3])
		}
	}

	// Pool should now be exhausted
	if _, err := ipam.Allocate("overflow-peer"); err != ErrSubnetExhausted {
		t.Errorf("expected ErrSubnetExhausted, got %v", err)
	}

	// Release one and re-allocate
	if err := ipam.Release("peer-2"); err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	newIP, err := ipam.Allocate("new-peer")
	if err != nil {
		t.Fatalf("Allocate after release failed: %v", err)
	}
	if newIP[3] != 4 { // released was peer-2 which was .4
		t.Errorf("expected re-allocated IP to be .4, got %s", newIP.String())
	}
}

func TestIPAMReservation(t *testing.T) {
	ipam, err := NewIPAM("10.100.0.0/24")
	if err != nil {
		t.Fatalf("NewIPAM failed: %v", err)
	}

	targetIP := net.ParseIP("10.100.0.50")
	if err := ipam.Reserve(targetIP, "reserved-peer"); err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	if !ipam.IsAllocated(targetIP) {
		t.Errorf("expected targetIP to be allocated")
	}

	// Reserve same peer and IP again (idempotent)
	if err := ipam.Reserve(targetIP, "reserved-peer"); err != nil {
		t.Errorf("Reserve same peer idempotent failed: %v", err)
	}

	// Conflict with another peer
	if err := ipam.Reserve(targetIP, "conflict-peer"); err != ErrIPAlreadyAllocated {
		t.Errorf("expected ErrIPAlreadyAllocated, got %v", err)
	}

	// Out of subnet
	if err := ipam.Reserve(net.ParseIP("192.168.1.1"), "out-peer"); err != ErrIPNotInSubnet {
		t.Errorf("expected ErrIPNotInSubnet, got %v", err)
	}

	// Gateway IP
	if err := ipam.Reserve(ipam.Gateway(), "gw-peer"); err != ErrIPReserved {
		t.Errorf("expected ErrIPReserved for gateway, got %v", err)
	}

	// Invalid input
	if err := ipam.Reserve(nil, "peer"); err != ErrInvalidSubnet {
		t.Errorf("expected ErrInvalidSubnet for nil IP, got %v", err)
	}
	if err := ipam.Reserve(targetIP, ""); err == nil {
		t.Errorf("expected error for empty peer key in Reserve")
	}
	if ipam.IsAllocated(nil) {
		t.Errorf("expected IsAllocated(nil) to be false")
	}
}
