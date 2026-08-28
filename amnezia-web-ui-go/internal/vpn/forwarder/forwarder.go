package forwarder

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrSessionNotRegistered = errors.New("session route not registered")
	ErrBackendNotFound      = errors.New("backend queue not found")
	ErrQueueFull            = errors.New("packet queue is full")
)

type sessionRoute struct {
	sessionID       string
	connectionID    string
	peerKey         string
	assignedIP      string
	backendTunnelID int64
	clientQueue     chan []byte
}

// Forwarder manages packet routing and bidirectional relay between peer sessions and backend tunnels.
type Forwarder struct {
	mu            sync.RWMutex
	accountant    *TrafficAccountant
	routesByPeer  map[string]*sessionRoute // peerKey -> route
	routesByIP    map[string]*sessionRoute // assignedIP -> route
	backendQueues map[int64]chan []byte    // backendTunnelID -> queue
	bufSize       int
	totalRxBytes  atomic.Int64
	totalTxBytes  atomic.Int64
	running       bool
	stopCh        chan struct{}
}

// NewForwarder creates a new Forwarder.
func NewForwarder(accountant *TrafficAccountant, bufSize ...int) *Forwarder {
	qSize := 256
	if len(bufSize) > 0 && bufSize[0] > 0 {
		qSize = bufSize[0]
	}
	return &Forwarder{
		accountant:    accountant,
		routesByPeer:  make(map[string]*sessionRoute),
		routesByIP:    make(map[string]*sessionRoute),
		backendQueues: make(map[int64]chan []byte),
		bufSize:       qSize,
		stopCh:        make(chan struct{}),
	}
}

// RegisterSession registers a peer session route and initializes its packet queues.
func (f *Forwarder) RegisterSession(sessionID, connectionID, peerKey, assignedIP string, backendTunnelID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Ensure backend queue exists
	if _, ok := f.backendQueues[backendTunnelID]; !ok {
		f.backendQueues[backendTunnelID] = make(chan []byte, f.bufSize)
	}

	route := &sessionRoute{
		sessionID:       sessionID,
		connectionID:    connectionID,
		peerKey:         peerKey,
		assignedIP:      assignedIP,
		backendTunnelID: backendTunnelID,
		clientQueue:     make(chan []byte, f.bufSize),
	}

	f.routesByPeer[peerKey] = route
	if assignedIP != "" {
		f.routesByIP[assignedIP] = route
	}
}

// UnregisterSession removes a peer session route and cleans up its channel.
func (f *Forwarder) UnregisterSession(peerKey string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if route, ok := f.routesByPeer[peerKey]; ok {
		delete(f.routesByIP, route.assignedIP)
		delete(f.routesByPeer, peerKey)
	}
}

// UpdateSessionBackend updates the assigned backend tunnel for a session (e.g. during failover).
func (f *Forwarder) UpdateSessionBackend(peerKey string, newBackendTunnelID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	route, ok := f.routesByPeer[peerKey]
	if !ok {
		return ErrSessionNotRegistered
	}

	if _, ok := f.backendQueues[newBackendTunnelID]; !ok {
		f.backendQueues[newBackendTunnelID] = make(chan []byte, f.bufSize)
	}

	route.backendTunnelID = newBackendTunnelID
	return nil
}

// RouteClientToBackend routes a packet from a client peer toward their assigned backend tunnel.
func (f *Forwarder) RouteClientToBackend(peerKey string, packet []byte) error {
	f.mu.RLock()
	route, ok := f.routesByPeer[peerKey]
	if !ok {
		f.mu.RUnlock()
		return ErrSessionNotRegistered
	}
	beQueue, ok := f.backendQueues[route.backendTunnelID]
	if !ok {
		f.mu.RUnlock()
		return ErrBackendNotFound
	}
	sID := route.sessionID
	cID := route.connectionID
	f.mu.RUnlock()

	pktLen := int64(len(packet))
	f.totalRxBytes.Add(pktLen)
	if f.accountant != nil {
		f.accountant.RecordRx(sID, cID, pktLen)
	}

	pktCopy := make([]byte, len(packet))
	copy(pktCopy, packet)

	select {
	case beQueue <- pktCopy:
		return nil
	default:
		return ErrQueueFull
	}
}

// RouteBackendToClient routes a packet arriving from a backend tunnel to the destination client peer.
func (f *Forwarder) RouteBackendToClient(backendTunnelID int64, packet []byte, destIP string) error {
	f.mu.RLock()
	route, ok := f.routesByIP[destIP]
	if !ok {
		f.mu.RUnlock()
		return ErrSessionNotRegistered
	}
	clientQueue := route.clientQueue
	sID := route.sessionID
	cID := route.connectionID
	f.mu.RUnlock()

	pktLen := int64(len(packet))
	f.totalTxBytes.Add(pktLen)
	if f.accountant != nil {
		f.accountant.RecordTx(sID, cID, pktLen)
	}

	pktCopy := make([]byte, len(packet))
	copy(pktCopy, packet)

	select {
	case clientQueue <- pktCopy:
		return nil
	default:
		return ErrQueueFull
	}
}

// GetClientPacketChannel returns the outbound packet channel for a client peer.
func (f *Forwarder) GetClientPacketChannel(peerKey string) (<-chan []byte, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	route, ok := f.routesByPeer[peerKey]
	if !ok {
		return nil, false
	}
	return route.clientQueue, true
}

// GetBackendPacketChannel returns the packet channel for a backend tunnel.
func (f *Forwarder) GetBackendPacketChannel(backendTunnelID int64) (<-chan []byte, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	ch, ok := f.backendQueues[backendTunnelID]
	return ch, ok
}

// GetStats returns current aggregated traffic and active route counts.
func (f *Forwarder) GetStats() (rx int64, tx int64, activeRoutes int) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	rx = f.totalRxBytes.Load()
	tx = f.totalTxBytes.Load()
	activeRoutes = len(f.routesByPeer)
	return
}

// Start marks the forwarder active.
func (f *Forwarder) Start(ctx context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = true
	if f.accountant != nil {
		f.accountant.Start(ctx)
	}
}

// Stop terminates the forwarder and flushes accountant.
func (f *Forwarder) Stop() error {
	f.mu.Lock()
	f.running = false
	f.mu.Unlock()

	if f.accountant != nil {
		return f.accountant.Stop()
	}
	return nil
}

// IsRunning returns true if the forwarder is active.
func (f *Forwarder) IsRunning() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.running
}
