package endpoint

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// PacketDevice abstracts physical Linux TUN devices and in-memory test devices.
type PacketDevice interface {
	Read(p []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	Close() error
	Name() string
	MTU() int
}

// ChannelPacketDevice is an in-memory PacketDevice implementation using Go channels.
type ChannelPacketDevice struct {
	name   string
	mtu    int
	in     chan []byte
	out    chan []byte
	closed atomic.Bool
	stopCh chan struct{}
}

// NewChannelPacketDevice creates a new in-memory channel packet device.
func NewChannelPacketDevice(name string, mtu int, bufSize int) *ChannelPacketDevice {
	if mtu <= 0 {
		mtu = 1420
	}
	if bufSize <= 0 {
		bufSize = 256
	}
	return &ChannelPacketDevice{
		name:   name,
		mtu:    mtu,
		in:     make(chan []byte, bufSize),
		out:    make(chan []byte, bufSize),
		stopCh: make(chan struct{}),
	}
}

func (c *ChannelPacketDevice) Read(p []byte) (int, error) {
	if c.closed.Load() {
		return 0, errors.New("device closed")
	}
	select {
	case <-c.stopCh:
		return 0, errors.New("device closed")
	case pkt, ok := <-c.in:
		if !ok {
			return 0, errors.New("device closed")
		}
		n := copy(p, pkt)
		return n, nil
	}
}

func (c *ChannelPacketDevice) Write(p []byte) (int, error) {
	if c.closed.Load() {
		return 0, errors.New("device closed")
	}
	buf := make([]byte, len(p))
	copy(buf, p)
	select {
	case <-c.stopCh:
		return 0, errors.New("device closed")
	case c.out <- buf:
		return len(p), nil
	default:
		return 0, errors.New("channel buffer full")
	}
}

func (c *ChannelPacketDevice) InjectPacket(p []byte) error {
	if c.closed.Load() {
		return errors.New("device closed")
	}
	buf := make([]byte, len(p))
	copy(buf, p)
	select {
	case c.in <- buf:
		return nil
	default:
		return errors.New("inbound queue full")
	}
}

func (c *ChannelPacketDevice) ReceivePacket() ([]byte, error) {
	if c.closed.Load() {
		return nil, errors.New("device closed")
	}
	select {
	case pkt, ok := <-c.out:
		if !ok {
			return nil, errors.New("device closed")
		}
		return pkt, nil
	case <-c.stopCh:
		return nil, errors.New("device closed")
	}
}

func (c *ChannelPacketDevice) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		close(c.stopCh)
	}
	return nil
}

func (c *ChannelPacketDevice) Name() string { return c.name }
func (c *ChannelPacketDevice) MTU() int     { return c.mtu }

// ListenerConfig defines settings for the AWG endpoint listener.
type ListenerConfig struct {
	ListenPort  int
	SubnetCIDR  string
	MTU         int
	PrivateKey  string
	PublicKey   string
	IdleTimeout time.Duration
}

// Listener manages the AWG endpoint UDP listener and peer lifecycle.
type Listener struct {
	mu         sync.RWMutex
	config     ListenerConfig
	db         *database.DB
	auth       Authenticator
	ipam       *IPAM
	sessionMgr *SessionManager
	udpConn    *net.UDPConn
	tunDev     PacketDevice
	running    bool
	draining   bool
	stopCh     chan struct{}
	wg         sync.WaitGroup
	rxBytes    atomic.Int64
	txBytes    atomic.Int64
}

// NewListener initializes a new AWG endpoint listener.
func NewListener(cfg ListenerConfig, db *database.DB, auth Authenticator, ipam *IPAM, sessionMgr *SessionManager) (*Listener, error) {
	if cfg.ListenPort <= 0 {
		cfg.ListenPort = 51820
	}
	if cfg.SubnetCIDR == "" {
		cfg.SubnetCIDR = "10.100.0.0/16"
	}
	if cfg.MTU <= 0 {
		cfg.MTU = 1420
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 3 * time.Minute
	}

	if auth == nil && db != nil {
		auth = NewDBAuthenticator(db)
	}

	if ipam == nil {
		var err error
		ipam, err = NewIPAM(cfg.SubnetCIDR)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize IPAM: %w", err)
		}
	}

	if sessionMgr == nil {
		sessionMgr = NewSessionManager(db, ipam)
	}

	return &Listener{
		config:     cfg,
		db:         db,
		auth:       auth,
		ipam:       ipam,
		sessionMgr: sessionMgr,
		tunDev:     NewChannelPacketDevice("awg0", cfg.MTU, 512),
		stopCh:     make(chan struct{}),
	}, nil
}

// SetPacketDevice overrides the default packet device (e.g. Linux TUN interface).
func (el *Listener) SetPacketDevice(dev PacketDevice) {
	el.mu.Lock()
	defer el.mu.Unlock()
	el.tunDev = dev
}

// Start binds the UDP port and starts the background loops.
func (el *Listener) Start(ctx context.Context) error {
	el.mu.Lock()
	if el.running {
		el.mu.Unlock()
		return errors.New("endpoint listener is already running")
	}

	addr := &net.UDPAddr{Port: el.config.ListenPort}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		el.mu.Unlock()
		return fmt.Errorf("failed to bind UDP port %d: %w", el.config.ListenPort, err)
	}

	el.udpConn = conn
	el.running = true
	el.draining = false
	el.stopCh = make(chan struct{})
	el.mu.Unlock()

	el.wg.Add(2)
	go el.udpReadLoop()
	go el.heartbeatLoop(ctx)

	return nil
}

// Stop gracefully stops the UDP listener and worker loops.
func (el *Listener) Stop() error {
	el.mu.Lock()
	if !el.running {
		el.mu.Unlock()
		return nil
	}
	el.running = false
	close(el.stopCh)
	if el.udpConn != nil {
		_ = el.udpConn.Close()
	}
	if el.tunDev != nil {
		_ = el.tunDev.Close()
	}
	el.mu.Unlock()

	el.wg.Wait()
	return nil
}

// Drain marks listener as draining and prepares active sessions for graceful disconnection.
func (el *Listener) Drain(ctx context.Context, timeout time.Duration) error {
	el.mu.Lock()
	el.draining = true
	el.mu.Unlock()

	if el.sessionMgr != nil {
		return el.sessionMgr.Drain(ctx, timeout)
	}
	return nil
}

// AuthenticateAndRegisterPeer authenticates peer credentials, leases an internal IP, and creates an active session.
func (el *Listener) AuthenticateAndRegisterPeer(ctx context.Context, peerPublicKey string, backendTunnelID int64) (*models.VPNSession, error) {
	el.mu.RLock()
	if el.draining {
		el.mu.RUnlock()
		return nil, errors.New("listener is draining: new connections rejected")
	}
	auth := el.auth
	ipam := el.ipam
	sm := el.sessionMgr
	el.mu.RUnlock()

	if auth == nil || ipam == nil || sm == nil {
		return nil, errors.New("endpoint listener subsystem not initialized")
	}

	user, _, err := auth.AuthenticatePeer(ctx, peerPublicKey)
	if err != nil {
		return nil, fmt.Errorf("peer authentication failed: %w", err)
	}

	assignedIP, err := ipam.Allocate(peerPublicKey)
	if err != nil {
		return nil, fmt.Errorf("ip allocation failed: %w", err)
	}

	sess, err := sm.CreateSession(ctx, user.ID, peerPublicKey, assignedIP.String(), backendTunnelID)
	if err != nil {
		_ = ipam.Release(peerPublicKey)
		return nil, fmt.Errorf("session creation failed: %w", err)
	}

	return sess, nil
}

// DisconnectPeer disconnects a peer and closes their session.
func (el *Listener) DisconnectPeer(ctx context.Context, peerPublicKey string) error {
	el.mu.RLock()
	sm := el.sessionMgr
	el.mu.RUnlock()

	if sm == nil {
		return nil
	}

	sess, ok := sm.GetSession(peerPublicKey)
	if !ok {
		return ErrPeerNotFound
	}

	return sm.CloseSession(ctx, sess.ID, "disconnected")
}

// GetListenAddr returns the bound UDP address or nil.
func (el *Listener) GetListenAddr() net.Addr {
	el.mu.RLock()
	defer el.mu.RUnlock()
	if el.udpConn != nil {
		return el.udpConn.LocalAddr()
	}
	return nil
}

// IsRunning returns true if the endpoint listener is active.
func (el *Listener) IsRunning() bool {
	el.mu.RLock()
	defer el.mu.RUnlock()
	return el.running
}

// IsDraining returns true if the listener is in draining mode.
func (el *Listener) IsDraining() bool {
	el.mu.RLock()
	defer el.mu.RUnlock()
	return el.draining
}

// GetStats returns current traffic bytes and active session counts.
func (el *Listener) GetStats() (rx int64, tx int64, active int) {
	rx = el.rxBytes.Load()
	tx = el.txBytes.Load()
	if el.sessionMgr != nil {
		active = el.sessionMgr.ActiveCount()
	}
	return
}

// RecordTraffic records raw packet ingress/egress bytes.
func (el *Listener) RecordTraffic(rx, tx int64) {
	if rx > 0 {
		el.rxBytes.Add(rx)
	}
	if tx > 0 {
		el.txBytes.Add(tx)
	}
}

// SessionManager returns the underlying session manager.
func (el *Listener) SessionManager() *SessionManager {
	return el.sessionMgr
}

// IPAM returns the underlying IP address manager.
func (el *Listener) IPAM() *IPAM {
	return el.ipam
}

func (el *Listener) udpReadLoop() {
	defer el.wg.Done()
	buf := make([]byte, 2048)

	for {
		select {
		case <-el.stopCh:
			return
		default:
		}

		if el.udpConn == nil {
			return
		}

		_ = el.udpConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := el.udpConn.ReadFrom(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			select {
			case <-el.stopCh:
				return
			default:
				continue
			}
		}

		if n > 0 {
			el.rxBytes.Add(int64(n))
		}
	}
}

func (el *Listener) heartbeatLoop(ctx context.Context) {
	defer el.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-el.stopCh:
			return
		case <-ticker.C:
			if el.sessionMgr != nil {
				_, _ = el.sessionMgr.CheckTimeouts(ctx, el.config.IdleTimeout)
			}
		}
	}
}
