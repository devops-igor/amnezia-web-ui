package endpoint

import (
	"encoding/binary"
	"errors"
	"net"
	"sync"
)

var (
	ErrSubnetExhausted    = errors.New("ipam: subnet address pool exhausted")
	ErrInvalidSubnet      = errors.New("ipam: invalid subnet CIDR")
	ErrIPAlreadyAllocated = errors.New("ipam: IP address is already allocated")
	ErrIPNotInSubnet      = errors.New("ipam: IP address is not within subnet")
	ErrPeerNotFoundInIPAM = errors.New("ipam: peer has no assigned IP")
	ErrIPReserved         = errors.New("ipam: IP address is reserved (gateway/network/broadcast)")
)

// IPAM manages thread-safe, collision-free internal IP address leasing for VPN peers.
type IPAM struct {
	mu        sync.RWMutex
	cidr      string
	ipNet     *net.IPNet
	gatewayIP net.IP
	startIP   uint32
	endIP     uint32
	cursor    uint32
	allocated map[string]net.IP // peerKey -> IP
	reverse   map[string]string // IP.String() -> peerKey
}

// NewIPAM initializes an IP Address Manager for the provided CIDR subnet.
func NewIPAM(cidr string) (*IPAM, error) {
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return nil, ErrInvalidSubnet
	}

	ipv4 := ip.To4()
	mask := ipNet.Mask
	if len(mask) != 4 {
		return nil, ErrInvalidSubnet
	}

	netInt := binary.BigEndian.Uint32(ipv4.Mask(mask))
	maskInt := binary.BigEndian.Uint32(mask)
	bcastInt := netInt | ^maskInt

	// Range calculation:
	// netInt: network address (reserved)
	// netInt + 1: gateway address (reserved)
	// bcastInt: broadcast address (reserved)
	// usable: [netInt + 2, bcastInt - 1]
	if bcastInt <= netInt+2 {
		return nil, errors.New("ipam: subnet is too small for leasing")
	}

	gatewayInt := netInt + 1
	gwBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(gwBytes, gatewayInt)

	return &IPAM{
		cidr:      cidr,
		ipNet:     ipNet,
		gatewayIP: net.IP(gwBytes),
		startIP:   netInt + 2,
		endIP:     bcastInt - 1,
		cursor:    netInt + 2,
		allocated: make(map[string]net.IP),
		reverse:   make(map[string]string),
	}, nil
}

// Allocate leases a unique internal IP address to a peer. If the peer already holds a lease, that IP is returned.
func (i *IPAM) Allocate(peerKey string) (net.IP, error) {
	if peerKey == "" {
		return nil, errors.New("ipam: peer key cannot be empty")
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	// Return existing lease
	if ip, ok := i.allocated[peerKey]; ok {
		return ip, nil
	}

	total := int(i.endIP - i.startIP + 1)
	if len(i.allocated) >= total {
		return nil, ErrSubnetExhausted
	}

	// Find next free IP in round-robin sequential order
	for step := 0; step < total; step++ {
		candidateInt := i.cursor
		i.cursor++
		if i.cursor > i.endIP {
			i.cursor = i.startIP
		}

		ipBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(ipBytes, candidateInt)
		candidateIP := net.IP(ipBytes)
		ipStr := candidateIP.String()

		if _, exists := i.reverse[ipStr]; !exists {
			i.allocated[peerKey] = candidateIP
			i.reverse[ipStr] = peerKey
			return candidateIP, nil
		}
	}

	return nil, ErrSubnetExhausted
}

// Reserve pre-assigns a specific IP to a peer (e.g. restoring persistent sessions).
func (i *IPAM) Reserve(ip net.IP, peerKey string) error {
	ipv4 := ip.To4()
	if ipv4 == nil {
		return ErrInvalidSubnet
	}
	if peerKey == "" {
		return errors.New("ipam: peer key cannot be empty")
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	ipInt := binary.BigEndian.Uint32(ipv4)
	if ipInt < i.startIP || ipInt > i.endIP {
		if ip.Equal(i.gatewayIP) {
			return ErrIPReserved
		}
		return ErrIPNotInSubnet
	}

	ipStr := ipv4.String()
	if existingPeer, exists := i.reverse[ipStr]; exists {
		if existingPeer == peerKey {
			return nil
		}
		return ErrIPAlreadyAllocated
	}

	// If peer already had a different IP, release old IP
	if oldIP, ok := i.allocated[peerKey]; ok {
		delete(i.reverse, oldIP.String())
	}

	i.allocated[peerKey] = ipv4
	i.reverse[ipStr] = peerKey
	return nil
}

// Release frees the IP address leased to a peer.
func (i *IPAM) Release(peerKey string) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	ip, ok := i.allocated[peerKey]
	if !ok {
		return ErrPeerNotFoundInIPAM
	}

	delete(i.allocated, peerKey)
	delete(i.reverse, ip.String())
	return nil
}

// ReleaseIP frees an allocated IP by its net.IP address.
func (i *IPAM) ReleaseIP(ip net.IP) error {
	ipv4 := ip.To4()
	if ipv4 == nil {
		return ErrInvalidSubnet
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	ipStr := ipv4.String()
	peerKey, ok := i.reverse[ipStr]
	if !ok {
		return ErrPeerNotFoundInIPAM
	}

	delete(i.reverse, ipStr)
	delete(i.allocated, peerKey)
	return nil
}

// IsAllocated checks if an IP is currently leased.
func (i *IPAM) IsAllocated(ip net.IP) bool {
	ipv4 := ip.To4()
	if ipv4 == nil {
		return false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	_, exists := i.reverse[ipv4.String()]
	return exists
}

// AllocatedCount returns the count of actively leased IPs.
func (i *IPAM) AllocatedCount() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.allocated)
}

// TotalAvailable returns the total number of assignable IPs in the pool.
func (i *IPAM) TotalAvailable() int {
	return int(i.endIP - i.startIP + 1)
}

// Gateway returns the gateway IP (typically .1) for this subnet.
func (i *IPAM) Gateway() net.IP {
	return i.gatewayIP
}

// Subnet returns the parsed CIDR subnet.
func (i *IPAM) Subnet() *net.IPNet {
	return i.ipNet
}

// CIDR returns the original CIDR string.
func (i *IPAM) CIDR() string {
	return i.cidr
}

// GetAssignedIP retrieves the currently assigned IP for a peer.
func (i *IPAM) GetAssignedIP(peerKey string) (net.IP, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	ip, ok := i.allocated[peerKey]
	return ip, ok
}
