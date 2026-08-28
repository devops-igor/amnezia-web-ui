package vpn

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/vpn/endpoint"
	"github.com/devops-igor/amnezia-web-ui-go/internal/vpn/forwarder"
	"github.com/devops-igor/amnezia-web-ui-go/internal/vpn/loadbalancer"
	"github.com/devops-igor/amnezia-web-ui-go/internal/vpn/tunnel"
)

// Status represents the overall runtime telemetry of the VPN endpoint and load balancing subsystem.
type Status struct {
	ListenerRunning   bool  `json:"listener_running"`
	ActiveTunnels     int   `json:"active_tunnels"`
	ConnectedSessions int   `json:"connected_sessions"`
	RxBytes           int64 `json:"rx_bytes"`
	TxBytes           int64 `json:"tx_bytes"`
}

// UserVPNState represents the real-time VPN connection state for a specific user.
type UserVPNState struct {
	Connected       bool               `json:"connected"`
	Session         *models.VPNSession `json:"session,omitempty"`
	BackendServerID int64              `json:"backend_server_id,omitempty"`
	BackendEndpoint string             `json:"backend_endpoint,omitempty"`
	LatencyMS       int64              `json:"latency_ms,omitempty"`
}

// BackendTunnelStatus type alias for backwards compatibility.
type BackendTunnelStatus = string

const (
	TunnelStatusConnecting = "connecting"
	TunnelStatusActive     = "active"
	TunnelStatusDegraded   = "degraded"
	TunnelStatusDisabled   = "disabled"
)

// BackendTunnel is an alias for models.BackendTunnel.
type BackendTunnel = models.BackendTunnel

// Session is an alias for models.VPNSession.
type Session = models.VPNSession

// LoadBalancer is an alias for loadbalancer.LoadBalancer.
type LoadBalancer = loadbalancer.LoadBalancer

// LeastConnectionsLoadBalancer is an alias for loadbalancer.LeastConnectionsBalancer.
type LeastConnectionsLoadBalancer = loadbalancer.LeastConnectionsBalancer

// NewLeastConnectionsLoadBalancer creates a least connections load balancer.
func NewLeastConnectionsLoadBalancer() *loadbalancer.LeastConnectionsBalancer {
	return loadbalancer.NewLeastConnectionsBalancer(loadbalancer.CapacityConfig{})
}

// Service orchestrates endpoint listener, backend tunnel pool, load balancing, and traffic forwarding.
type Service struct {
	mu            sync.RWMutex
	db            *database.DB
	cfg           *models.VPNConfig
	endpoint      *endpoint.Listener
	sessionMgr    *endpoint.SessionManager
	ipam          *endpoint.IPAM
	auth          *endpoint.DBAuthenticator
	pool          *tunnel.Pool
	prober        *tunnel.HealthProber
	reconnectMgr  *tunnel.ReconnectManager
	balancer      loadbalancer.LoadBalancer
	stickyMgr     *loadbalancer.StickySessionManager
	forwarder     *forwarder.Forwarder
	accountant    *forwarder.TrafficAccountant
	running       bool
	portalPubKey  string
	portalPrivKey string
}

// NewVPNService initializes the complete unified VPN subsystem.
func NewVPNService(db *database.DB, cfg *models.VPNConfig) (*Service, error) {
	if cfg == nil {
		if db != nil {
			var err error
			cfg, err = db.GetVPNConfig(context.Background())
			if err != nil {
				cfg = &models.VPNConfig{
					Algorithm:          models.LBLeastConnections,
					ListenPort:         51820,
					SubnetCIDR:         "10.100.0.0/16",
					HealthThresholdMS:  500,
					MaxTotalPeers:      1000,
					MaxPeersPerBackend: 250,
					Weights:            make(map[int64]int),
				}
			}
		} else {
			cfg = &models.VPNConfig{
				Algorithm:          models.LBLeastConnections,
				ListenPort:         51820,
				SubnetCIDR:         "10.100.0.0/16",
				HealthThresholdMS:  500,
				MaxTotalPeers:      1000,
				MaxPeersPerBackend: 250,
				Weights:            make(map[int64]int),
			}
		}
	}

	ipam, err := endpoint.NewIPAM(cfg.SubnetCIDR)
	if err != nil {
		return nil, fmt.Errorf("failed to init IPAM: %w", err)
	}

	var auth *endpoint.DBAuthenticator
	if db != nil {
		auth = endpoint.NewDBAuthenticator(db)
	}

	sessionMgr := endpoint.NewSessionManager(db, ipam)

	listenerCfg := endpoint.ListenerConfig{
		ListenPort:  cfg.ListenPort,
		SubnetCIDR:  cfg.SubnetCIDR,
		MTU:         1420,
		IdleTimeout: 3 * time.Minute,
	}

	epListener, err := endpoint.NewListener(listenerCfg, db, auth, ipam, sessionMgr)
	if err != nil {
		return nil, fmt.Errorf("failed to init endpoint listener: %w", err)
	}

	pool := tunnel.NewPool(db)

	healthCfg := tunnel.DefaultHealthConfig()
	healthCfg.LatencyThresholdMS = int64(cfg.HealthThresholdMS)
	prober := tunnel.NewHealthProber(pool, db, healthCfg)

	reconnectCfg := tunnel.DefaultReconnectConfig()
	reconnectMgr := tunnel.NewReconnectManager(pool, prober, reconnectCfg)

	caps := loadbalancer.CapacityConfig{
		MaxTotalPeers:      cfg.MaxTotalPeers,
		MaxPeersPerBackend: cfg.MaxPeersPerBackend,
	}

	lb, err := loadbalancer.NewLoadBalancer(cfg.Algorithm, cfg.Weights, caps)
	if err != nil {
		lb = loadbalancer.NewLeastConnectionsBalancer(caps)
	}

	stickyMgr := loadbalancer.NewStickySessionManager(db, lb, caps)

	accountant := forwarder.NewTrafficAccountant(db, 2*time.Second)
	fwd := forwarder.NewForwarder(accountant, 512)

	pub, priv, _ := tunnel.GenerateCurve25519KeyPair()

	svc := &Service{
		db:            db,
		cfg:           cfg,
		endpoint:      epListener,
		sessionMgr:    sessionMgr,
		ipam:          ipam,
		auth:          auth,
		pool:          pool,
		prober:        prober,
		reconnectMgr:  reconnectMgr,
		balancer:      lb,
		stickyMgr:     stickyMgr,
		forwarder:     fwd,
		accountant:    accountant,
		portalPubKey:  pub,
		portalPrivKey: priv,
	}

	return svc, nil
}

// NewService creates a new VPNService with a specific algorithm (backwards compatibility).
func NewService(algo models.LoadBalancingAlgorithm) *Service {
	cfg := &models.VPNConfig{
		Algorithm:          algo,
		ListenPort:         51820,
		SubnetCIDR:         "10.100.0.0/16",
		HealthThresholdMS:  500,
		MaxTotalPeers:      1000,
		MaxPeersPerBackend: 250,
		Weights:            make(map[int64]int),
	}
	svc, _ := NewVPNService(nil, cfg)
	return svc
}

// SetProbeFunc sets the health probe function for testing or customized reachability probing.
func (s *Service) SetProbeFunc(fn tunnel.ProbeFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prober != nil {
		s.prober.SetProbeFunc(fn)
	}
}

// SetHealthProber sets a custom health prober instance.
func (s *Service) SetHealthProber(prober *tunnel.HealthProber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prober = prober
}

// Start launches the complete VPN subsystem.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.mu.Unlock()

	// 1. Sync tunnels from DB
	if s.pool != nil {
		if err := s.pool.SyncFromDB(ctx); err != nil {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			return fmt.Errorf("failed to sync tunnels from DB: %w", err)
		}
	}

	// 2. Sync sessions from DB
	if s.sessionMgr != nil {
		_ = s.sessionMgr.SyncFromDB(ctx)
	}

	// 3. Start forwarder & accountant
	if s.forwarder != nil {
		s.forwarder.Start(ctx)
	}

	// 4. Start health prober & reconnect manager
	if s.prober != nil {
		s.prober.Start(ctx)
	}
	if s.reconnectMgr != nil {
		s.reconnectMgr.Start(ctx)
	}

	// 5. Start endpoint listener
	if s.endpoint != nil {
		if err := s.endpoint.Start(ctx); err != nil {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			return fmt.Errorf("failed to start endpoint listener: %w", err)
		}
	}

	return nil
}

// Stop gracefully shuts down the VPN subsystem.
func (s *Service) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	if s.endpoint != nil {
		_ = s.endpoint.Stop()
	}
	if s.prober != nil {
		s.prober.Stop()
	}
	if s.reconnectMgr != nil {
		s.reconnectMgr.Stop()
	}
	if s.forwarder != nil {
		_ = s.forwarder.Stop()
	}
	if s.pool != nil {
		_ = s.pool.Close()
	}

	return nil
}

// IsRunning returns true if the VPN service is active.
func (s *Service) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// GetStatus returns the operational status and telemetry of the VPN subsystem.
func (s *Service) GetStatus(ctx context.Context) (*Status, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := &Status{
		ListenerRunning: s.endpoint != nil && s.endpoint.IsRunning(),
	}

	if s.pool != nil {
		status.ActiveTunnels = len(s.pool.GetActiveTunnels())
	}
	if s.sessionMgr != nil {
		status.ConnectedSessions = s.sessionMgr.ActiveCount()
	}
	if s.forwarder != nil {
		rx, tx, _ := s.forwarder.GetStats()
		status.RxBytes = rx
		status.TxBytes = tx
	}

	return status, nil
}

// GetBackends returns all registered backend tunnels.
func (s *Service) GetBackends(ctx context.Context) ([]*models.BackendTunnel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.pool == nil {
		return nil, errors.New("tunnel pool not initialized")
	}
	return s.pool.ListTunnels(), nil
}

// EnableBackend enables a backend server for load balancing.
func (s *Service) EnableBackend(ctx context.Context, serverID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pool == nil {
		return errors.New("tunnel pool not initialized")
	}

	return s.pool.SetTunnelStatus(ctx, serverID, "active", 10)
}

// DisableBackend disables a backend server and initiates connection draining.
func (s *Service) DisableBackend(ctx context.Context, serverID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pool == nil {
		return errors.New("tunnel pool not initialized")
	}

	tunnel, err := s.pool.GetTunnel(serverID)
	if err != nil {
		return err
	}

	_ = s.pool.SetTunnelStatus(ctx, serverID, "disabled", 0)

	// Trigger failover for active sessions on this backend
	if s.stickyMgr != nil {
		activeTunnels := s.pool.GetActiveTunnels()
		_, _ = s.stickyMgr.HandleFailover(ctx, tunnel.ID, activeTunnels)
	}

	return nil
}

// GetTunnels is an alias for GetBackends.
func (s *Service) GetTunnels(ctx context.Context) ([]*models.BackendTunnel, error) {
	return s.GetBackends(ctx)
}

// GetConfig returns the active VPN configuration.
func (s *Service) GetConfig(ctx context.Context) (*models.VPNConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.cfg == nil {
		return nil, errors.New("vpn config is nil")
	}
	cfgCopy := *s.cfg
	return &cfgCopy, nil
}

// UpdateConfig updates the dynamic VPN configuration and reinitializes the load balancer.
func (s *Service) UpdateConfig(ctx context.Context, cfg *models.VPNConfig) error {
	if cfg == nil {
		return errors.New("vpn config cannot be nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.cfg = cfg
	if s.db != nil {
		if err := s.db.SaveVPNConfig(ctx, cfg); err != nil {
			return fmt.Errorf("failed to persist vpn config: %w", err)
		}
	}

	caps := loadbalancer.CapacityConfig{
		MaxTotalPeers:      cfg.MaxTotalPeers,
		MaxPeersPerBackend: cfg.MaxPeersPerBackend,
	}

	lb, err := loadbalancer.NewLoadBalancer(cfg.Algorithm, cfg.Weights, caps)
	if err == nil {
		s.balancer = lb
		if s.stickyMgr != nil {
			s.stickyMgr = loadbalancer.NewStickySessionManager(s.db, lb, caps)
		}
	}

	return nil
}

// GetUserConnectionState retrieves current VPN connectivity state for a user.
func (s *Service) GetUserConnectionState(ctx context.Context, userID string) (*UserVPNState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.sessionMgr == nil {
		return &UserVPNState{Connected: false}, nil
	}

	sessions := s.sessionMgr.GetSessionsByUserID(userID)
	if len(sessions) == 0 {
		return &UserVPNState{Connected: false}, nil
	}

	sess := sessions[0]
	state := &UserVPNState{
		Connected: true,
		Session:   sess,
	}

	if s.pool != nil {
		if t, err := s.pool.GetTunnelByID(sess.BackendTunnelID); err == nil && t != nil {
			state.BackendServerID = t.ServerID
			state.BackendEndpoint = t.Endpoint
			state.LatencyMS = t.LatencyMS
		}
	}

	return state, nil
}

// DisconnectUser disconnects all active VPN sessions for a user.
func (s *Service) DisconnectUser(ctx context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionMgr == nil {
		return nil
	}

	sessions := s.sessionMgr.GetSessionsByUserID(userID)
	for _, sess := range sessions {
		_ = s.sessionMgr.CloseSession(ctx, sess.ID, "disconnected")
		if s.forwarder != nil {
			s.forwarder.UnregisterSession(sess.PeerPublicKey)
		}
		if s.stickyMgr != nil {
			s.stickyMgr.ClearAffinity(userID)
			s.stickyMgr.ClearPeerAffinity(sess.PeerPublicKey)
		}
	}

	return nil
}

// DisconnectSession disconnects a specific VPN session by ID.
func (s *Service) DisconnectSession(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionMgr == nil {
		return nil
	}

	sess, ok := s.sessionMgr.GetSessionByID(sessionID)
	if !ok {
		return endpoint.ErrSessionNotFound
	}

	_ = s.sessionMgr.CloseSession(ctx, sessionID, "disconnected")
	if s.forwarder != nil {
		s.forwarder.UnregisterSession(sess.PeerPublicKey)
	}
	if s.stickyMgr != nil {
		s.stickyMgr.ClearAffinity(sess.UserID)
		s.stickyMgr.ClearPeerAffinity(sess.PeerPublicKey)
	}

	return nil
}

// HandleIncomingPeer authenticates a connecting peer, selects a backend tunnel, and registers forwarding routes.
func (s *Service) HandleIncomingPeer(ctx context.Context, peerPublicKey string) (*models.VPNSession, *models.BackendTunnel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.auth == nil || s.ipam == nil || s.sessionMgr == nil || s.pool == nil {
		return nil, nil, errors.New("subsystems not initialized")
	}

	user, conn, err := s.auth.AuthenticatePeer(ctx, peerPublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("peer authentication failed: %w", err)
	}

	activeTunnels := s.pool.GetActiveTunnels()
	if len(activeTunnels) == 0 {
		return nil, nil, loadbalancer.ErrNoActiveBackends
	}

	req := &loadbalancer.RoutingRequest{
		UserID:           user.ID,
		PeerPublicKey:    peerPublicKey,
		AvailableTunnels: activeTunnels,
	}

	var backend *models.BackendTunnel
	if s.stickyMgr != nil {
		b, _, err := s.stickyMgr.GetOrAssignBackend(ctx, req)
		if err != nil {
			return nil, nil, fmt.Errorf("backend selection failed: %w", err)
		}
		backend = b
	} else if s.balancer != nil {
		b, err := s.balancer.SelectBackend(ctx, req)
		if err != nil {
			return nil, nil, fmt.Errorf("backend selection failed: %w", err)
		}
		backend = b
	}

	assignedIP, err := s.ipam.Allocate(peerPublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("ip allocation failed: %w", err)
	}

	sess, err := s.sessionMgr.CreateSession(ctx, user.ID, peerPublicKey, assignedIP.String(), backend.ID)
	if err != nil {
		_ = s.ipam.Release(peerPublicKey)
		return nil, nil, fmt.Errorf("session creation failed: %w", err)
	}

	s.pool.IncrementConnections(backend.ID)

	if s.forwarder != nil {
		s.forwarder.RegisterSession(sess.ID, conn.ID, peerPublicKey, assignedIP.String(), backend.ID)
	}

	return sess, backend, nil
}

// SelectTunnel selects a backend tunnel using the configured load balancing algorithm.
func (s *Service) SelectTunnel(ctx context.Context, tunnels []*models.BackendTunnel) (*models.BackendTunnel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.balancer == nil {
		return nil, fmt.Errorf("load balancer not initialized")
	}

	req := &loadbalancer.RoutingRequest{
		AvailableTunnels: tunnels,
	}
	return s.balancer.SelectBackend(ctx, req)
}

// GenerateClientConfig builds an AWG client configuration for connecting to this portal VPN endpoint.
func (s *Service) GenerateClientConfig(ctx context.Context, userID string) (string, string, error) {
	s.mu.RLock()
	db := s.db
	cfg := s.cfg
	portalPub := s.portalPubKey
	s.mu.RUnlock()

	if db == nil {
		return "", "", errors.New("database not available")
	}

	user, err := db.GetUser(ctx, userID)
	if err != nil || user == nil {
		return "", "", fmt.Errorf("user not found: %w", err)
	}

	// Generate client keypair
	clientPub, clientPriv, err := tunnel.GenerateCurve25519KeyPair()
	if err != nil {
		return "", "", err
	}

	// Persist / update client connection in database so peer authentication succeeds
	conns, err := db.GetConnectionsByUserID(ctx, userID)
	var awgConn *models.UserConnection
	if err == nil {
		for i := range conns {
			if models.NormalizeProtocol(conns[i].Protocol) == "awg" {
				awgConn = &conns[i]
				break
			}
		}
	}

	if awgConn != nil {
		_, _ = db.UpdateConnection(ctx, awgConn.ID, map[string]any{
			"client_id": clientPub,
		})
	} else {
		var srvID int64
		if servers, err := db.GetAllServers(ctx); err == nil && len(servers) > 0 {
			srvID = servers[0].ID
		}
		newConn := &models.UserConnection{
			UserID:     user.ID,
			ServerID:   srvID,
			Protocol:   "awg",
			ClientID:   clientPub,
			Name:       fmt.Sprintf("%s-awg", user.Username),
			AWGMimicry: models.AWGMimicryAuto,
		}
		_, _ = db.CreateConnection(ctx, newConn)
	}

	assignedIP := "10.100.0.2"
	if s.ipam != nil {
		if ip, err := s.ipam.Allocate(clientPub); err == nil {
			assignedIP = ip.String()
		}
	}

	listenPort := 51820
	if cfg != nil && cfg.ListenPort > 0 {
		listenPort = cfg.ListenPort
	}

	endpointHost := "127.0.0.1"
	if servers, err := db.GetAllServers(ctx); err == nil && len(servers) > 0 {
		for _, srv := range servers {
			if srv.Host != "" {
				endpointHost = srv.Host
				break
			}
		}
	}

	endpointStr := fmt.Sprintf("%s:%d", endpointHost, listenPort)

	// Use real AWG obfuscation parameters with quadrant headers & CPS signatures
	awgParams, err := awg.GenerateAWGParams("standard")
	if err != nil {
		awgParams = awg.AWGParamsFromMap(nil)
	}

	configStr := awg.RenderClientConfig(
		clientPriv,
		assignedIP,
		portalPub,
		"", // psk
		endpointStr,
		"1.1.1.1",
		"1.0.0.1",
		"1420",
		awgParams,
	)

	filename := fmt.Sprintf("amnezia-portal-%s.conf", user.Username)
	return configStr, filename, nil
}
