package forwarder

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrSessionNotRegistered = errors.New("session route not registered")
	ErrBackendNotFound      = errors.New("backend queue not found")
	ErrQueueFull            = errors.New("packet queue is full")
	ErrRateLimitExceeded    = errors.New("rate limit exceeded")
)

// PacketDevice abstracts physical Linux TUN / network interfaces and in-memory test devices.
type PacketDevice interface {
	Read(p []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	Close() error
}

// TokenBucket implements a token bucket rate limiter for per-peer bandwidth throttling.
type TokenBucket struct {
	rate       float64 // bytes per second
	capacity   float64 // burst capacity in bytes
	tokens     float64
	lastUpdate time.Time
	mu         sync.Mutex
}

// NewTokenBucket creates a new token bucket with rate in bytes per second.
func NewTokenBucket(rateBps int64, capacity ...int64) *TokenBucket {
	if rateBps <= 0 {
		return nil
	}
	capVal := float64(rateBps)
	if len(capacity) > 0 && capacity[0] > 0 {
		capVal = float64(capacity[0])
	}
	if capVal < float64(rateBps) {
		capVal = float64(rateBps)
	}
	return &TokenBucket{
		rate:       float64(rateBps),
		capacity:   capVal,
		tokens:     capVal,
		lastUpdate: time.Now(),
	}
}

// Allow returns true if n bytes can be consumed from the bucket, and deducts the tokens.
func (tb *TokenBucket) Allow(n int64) bool {
	if tb == nil {
		return true
	}
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastUpdate).Seconds()
	tb.lastUpdate = now

	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}

	if tb.tokens >= float64(n) {
		tb.tokens -= float64(n)
		return true
	}
	return false
}

type sessionRoute struct {
	sessionID       string
	connectionID    string
	peerKey         string
	assignedIP      string
	backendTunnelID int64
	clientQueue     chan []byte
	limitDownBps    int64
	limitUpBps      int64
	tbDown          *TokenBucket
	tbUp            *TokenBucket
}

// Forwarder manages packet routing and bidirectional relay between peer sessions and backend tunnels.
type Forwarder struct {
	mu               sync.RWMutex
	accountant       *TrafficAccountant
	routesByPeer     map[string]*sessionRoute // peerKey -> route
	routesByIP       map[string]*sessionRoute // assignedIP -> route
	backendQueues    map[int64]chan []byte    // backendTunnelID -> queue
	clientDevices    map[string]PacketDevice  // peerKey -> device
	backendDevices   map[int64]PacketDevice   // backendTunnelID -> device
	defaultClientDev PacketDevice             // default client packet device
	bufSize          int
	totalRxBytes     atomic.Int64
	totalTxBytes     atomic.Int64
	running          bool
	stopCh           chan struct{}
	pumpsRunning     bool
	pumpsStopCh      chan struct{}
	pumpsWg          sync.WaitGroup
}

// NewForwarder creates a new Forwarder.
func NewForwarder(accountant *TrafficAccountant, bufSize ...int) *Forwarder {
	qSize := 256
	if len(bufSize) > 0 && bufSize[0] > 0 {
		qSize = bufSize[0]
	}
	return &Forwarder{
		accountant:     accountant,
		routesByPeer:   make(map[string]*sessionRoute),
		routesByIP:     make(map[string]*sessionRoute),
		backendQueues:  make(map[int64]chan []byte),
		clientDevices:  make(map[string]PacketDevice),
		backendDevices: make(map[int64]PacketDevice),
		bufSize:        qSize,
		stopCh:         make(chan struct{}),
	}
}

// RegisterSession registers a peer session route with unlimited bandwidth.
func (f *Forwarder) RegisterSession(sessionID, connectionID, peerKey, assignedIP string, backendTunnelID int64) {
	f.RegisterSessionWithLimit(sessionID, connectionID, peerKey, assignedIP, backendTunnelID, 0, 0)
}

// RegisterSessionWithLimit registers a peer session route and configures initial rate limits (in bytes/sec).
func (f *Forwarder) RegisterSessionWithLimit(sessionID, connectionID, peerKey, assignedIP string, backendTunnelID int64, limitDownBps, limitUpBps int64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Ensure backend queue exists
	if _, ok := f.backendQueues[backendTunnelID]; !ok {
		f.backendQueues[backendTunnelID] = make(chan []byte, f.bufSize)
	}

	var tbDown, tbUp *TokenBucket
	if limitDownBps > 0 {
		tbDown = NewTokenBucket(limitDownBps)
	}
	if limitUpBps > 0 {
		tbUp = NewTokenBucket(limitUpBps)
	}

	route := &sessionRoute{
		sessionID:       sessionID,
		connectionID:    connectionID,
		peerKey:         peerKey,
		assignedIP:      assignedIP,
		backendTunnelID: backendTunnelID,
		clientQueue:     make(chan []byte, f.bufSize),
		limitDownBps:    limitDownBps,
		limitUpBps:      limitUpBps,
		tbDown:          tbDown,
		tbUp:            tbUp,
	}

	f.routesByPeer[peerKey] = route
	if assignedIP != "" {
		f.routesByIP[assignedIP] = route
	}

	if f.pumpsRunning {
		f.pumpsWg.Add(1)
		go f.pumpClientQueue(f.pumpsStopCh, route)
	}
}

// SetPeerRateLimit sets per-peer bandwidth throttling limits in bytes per second.
func (f *Forwarder) SetPeerRateLimit(peerKey string, limitDownBps, limitUpBps int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	route, ok := f.routesByPeer[peerKey]
	if !ok {
		return ErrSessionNotRegistered
	}

	route.limitDownBps = limitDownBps
	route.limitUpBps = limitUpBps
	if limitDownBps > 0 {
		route.tbDown = NewTokenBucket(limitDownBps)
	} else {
		route.tbDown = nil
	}
	if limitUpBps > 0 {
		route.tbUp = NewTokenBucket(limitUpBps)
	} else {
		route.tbUp = nil
	}

	return nil
}

// GetPeerRateLimit returns the configured rate limits for a peer in bytes per second.
func (f *Forwarder) GetPeerRateLimit(peerKey string) (limitDownBps, limitUpBps int64, err error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	route, ok := f.routesByPeer[peerKey]
	if !ok {
		return 0, 0, ErrSessionNotRegistered
	}

	return route.limitDownBps, route.limitUpBps, nil
}

// UnregisterSession removes a peer session route and cleans up its channel.
func (f *Forwarder) UnregisterSession(peerKey string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if route, ok := f.routesByPeer[peerKey]; ok {
		delete(f.routesByIP, route.assignedIP)
		delete(f.routesByPeer, peerKey)
		delete(f.clientDevices, peerKey)
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
	tbUp := route.tbUp
	f.mu.RUnlock()

	pktLen := int64(len(packet))
	if tbUp != nil && !tbUp.Allow(pktLen) {
		return ErrRateLimitExceeded
	}

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
	tbDown := route.tbDown
	f.mu.RUnlock()

	pktLen := int64(len(packet))
	if tbDown != nil && !tbDown.Allow(pktLen) {
		return ErrRateLimitExceeded
	}

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

// AttachClientDevice attaches a default client-facing packet device (e.g. TUN interface).
func (f *Forwarder) AttachClientDevice(dev PacketDevice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaultClientDev = dev
}

// AttachPeerDevice attaches a peer-specific packet device.
func (f *Forwarder) AttachPeerDevice(peerKey string, dev PacketDevice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if dev != nil {
		f.clientDevices[peerKey] = dev
	} else {
		delete(f.clientDevices, peerKey)
	}
}

// AttachBackendDevice attaches a backend tunnel packet device.
func (f *Forwarder) AttachBackendDevice(backendTunnelID int64, dev PacketDevice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.backendQueues[backendTunnelID]; !ok {
		f.backendQueues[backendTunnelID] = make(chan []byte, f.bufSize)
	}
	if dev != nil {
		f.backendDevices[backendTunnelID] = dev
	} else {
		delete(f.backendDevices, backendTunnelID)
	}

	if f.pumpsRunning && dev != nil {
		f.pumpsWg.Add(1)
		go f.pumpBackendQueue(f.pumpsStopCh, backendTunnelID, f.backendQueues[backendTunnelID], dev)
	}
}

// DetachBackendDevice detaches a backend tunnel packet device.
func (f *Forwarder) DetachBackendDevice(backendTunnelID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.backendDevices, backendTunnelID)
}

// StartPumps launches background packet pumps connecting forwarder queues to attached packet devices.
func (f *Forwarder) StartPumps(ctx context.Context) {
	f.mu.Lock()
	if f.pumpsRunning {
		f.mu.Unlock()
		return
	}
	f.pumpsRunning = true
	f.pumpsStopCh = make(chan struct{})
	stopCh := f.pumpsStopCh

	// Start pump for each backend device
	for beID, dev := range f.backendDevices {
		if q, ok := f.backendQueues[beID]; ok && dev != nil {
			f.pumpsWg.Add(1)
			go f.pumpBackendQueue(stopCh, beID, q, dev)
		}
	}

	// Start pump for each client route
	for _, route := range f.routesByPeer {
		f.pumpsWg.Add(1)
		go f.pumpClientQueue(stopCh, route)
	}
	f.mu.Unlock()
}

// StopPumps terminates background packet pump routines.
func (f *Forwarder) StopPumps() {
	f.mu.Lock()
	if !f.pumpsRunning {
		f.mu.Unlock()
		return
	}
	f.pumpsRunning = false
	close(f.pumpsStopCh)
	f.mu.Unlock()

	f.pumpsWg.Wait()
}

func (f *Forwarder) pumpBackendQueue(stopCh <-chan struct{}, backendTunnelID int64, queue chan []byte, dev PacketDevice) {
	defer f.pumpsWg.Done()
	for {
		select {
		case <-stopCh:
			return
		case pkt, ok := <-queue:
			if !ok {
				return
			}
			if dev != nil {
				_, _ = dev.Write(pkt)
			}
		}
	}
}

func (f *Forwarder) pumpClientQueue(stopCh <-chan struct{}, route *sessionRoute) {
	defer f.pumpsWg.Done()
	for {
		select {
		case <-stopCh:
			return
		case pkt, ok := <-route.clientQueue:
			if !ok {
				return
			}
			f.mu.RLock()
			dev, ok := f.clientDevices[route.peerKey]
			if !ok || dev == nil {
				dev = f.defaultClientDev
			}
			f.mu.RUnlock()

			if dev != nil {
				_, _ = dev.Write(pkt)
			}
		}
	}
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

// Stop terminates the forwarder, pumps, and flushes accountant.
func (f *Forwarder) Stop() error {
	f.StopPumps()

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
