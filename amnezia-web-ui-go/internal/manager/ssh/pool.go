package ssh

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

var (
	// ErrPoolClosed is returned when operations are attempted on a closed pool.
	ErrPoolClosed = errors.New("ssh client pool is closed")
)

// PoolConfig configures the SSHClientPool lifecycle.
type PoolConfig struct {
	IdleTimeout     time.Duration // Duration after which idle connections are closed (default 5m)
	KeepAlivePeriod time.Duration // Interval to send keepalive pings (default 30s)
	SweepInterval   time.Duration // Interval to sweep expired connections (default 1m)
}

type pooledEntry struct {
	client     *Client
	key        string
	serverID   *int64
	cfg        Config
	lastActive time.Time
}

// SSHClientPool manages a thread-safe pool of reusable SSH connections with keepalive and idle eviction.
//
//nolint:revive
type SSHClientPool struct {
	mu     sync.RWMutex
	conns  map[string]*pooledEntry
	cfg    PoolConfig
	store  HostKeyStore
	stopCh chan struct{}
	wg     sync.WaitGroup
	closed bool
}

// NewSSHClientPool creates a new SSHClientPool and starts background maintainer routines.
func NewSSHClientPool(cfg PoolConfig, store HostKeyStore) *SSHClientPool {
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}
	if cfg.KeepAlivePeriod <= 0 {
		cfg.KeepAlivePeriod = 30 * time.Second
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = 1 * time.Minute
	}

	p := &SSHClientPool{
		conns:  make(map[string]*pooledEntry),
		cfg:    cfg,
		store:  store,
		stopCh: make(chan struct{}),
	}

	p.wg.Add(2)
	go p.keepAliveLoop()
	go p.sweepLoop()

	return p
}

// PoolKey generates a deterministic lookup key for a server connection.
func PoolKey(host string, port int, user string) string {
	if port <= 0 {
		port = 22
	}
	if user == "" {
		user = "root"
	}
	return fmt.Sprintf("%s:%d:%s", host, port, user)
}

// ServerPoolKey generates a lookup key from a models.Server instance.
func ServerPoolKey(server *models.Server) string {
	if server == nil {
		return ""
	}
	return fmt.Sprintf("server:%d:%s:%d:%s", server.ID, server.Host, server.SSHPort, server.SSHUser)
}

// Get retrieves or creates an active SSHClient for a models.Server.
func (p *SSHClientPool) Get(ctx context.Context, server *models.Server) (SSHClient, error) {
	if server == nil {
		return nil, errors.New("server cannot be nil")
	}

	cfg := Config{
		Host:            server.Host,
		Port:            server.SSHPort,
		User:            server.SSHUser,
		Password:        server.SSHPass,
		PrivateKey:      server.SSHKey,
		ServerID:        &server.ID,
		Store:           p.store,
		Timeout:         15 * time.Second,
		KeepAlivePeriod: p.cfg.KeepAlivePeriod,
	}

	key := ServerPoolKey(server)
	return p.getOrCreate(ctx, key, cfg)
}

// GetWithConfig retrieves or creates an active SSHClient with custom Config parameters.
func (p *SSHClientPool) GetWithConfig(ctx context.Context, cfg Config) (SSHClient, error) {
	if cfg.Store == nil && p.store != nil {
		cfg.Store = p.store
	}
	var key string
	if cfg.ServerID != nil {
		key = fmt.Sprintf("server:%d:%s:%d:%s", *cfg.ServerID, cfg.Host, cfg.Port, cfg.User)
	} else {
		key = PoolKey(cfg.Host, cfg.Port, cfg.User)
	}

	return p.getOrCreate(ctx, key, cfg)
}

func (p *SSHClientPool) getOrCreate(ctx context.Context, key string, cfg Config) (SSHClient, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}

	entry, exists := p.conns[key]
	if exists && entry.client != nil {
		if entry.client.IsAlive() {
			entry.lastActive = time.Now()
			client := entry.client
			p.mu.Unlock()
			return client, nil
		}
		// Connection dead -> evict
		_ = entry.client.Close()
		delete(p.conns, key)
	}
	p.mu.Unlock()

	// Dial outside mutex lock
	client, err := Dial(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pool failed to dial %s: %w", key, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		_ = client.Close()
		return nil, ErrPoolClosed
	}

	// Check if another goroutine connected while we were dialing
	if existing, ok := p.conns[key]; ok && existing.client != nil && existing.client.IsAlive() {
		_ = client.Close() // Discard our redundant new client
		existing.lastActive = time.Now()
		return existing.client, nil
	}

	p.conns[key] = &pooledEntry{
		client:     client,
		key:        key,
		serverID:   cfg.ServerID,
		cfg:        cfg,
		lastActive: time.Now(),
	}

	return client, nil
}

// Remove closes and evicts a specific connection by server ID.
func (p *SSHClientPool) Remove(serverID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for k, entry := range p.conns {
		if entry.serverID != nil && *entry.serverID == serverID {
			if entry.client != nil {
				_ = entry.client.Close()
			}
			delete(p.conns, k)
		}
	}
}

// RemoveByKey closes and evicts a connection by its pool key.
func (p *SSHClientPool) RemoveByKey(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if entry, ok := p.conns[key]; ok {
		if entry.client != nil {
			_ = entry.client.Close()
		}
		delete(p.conns, key)
	}
}

// Len returns the number of active pooled connections.
func (p *SSHClientPool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.conns)
}

// Close closes all pooled connections and shuts down background workers.
func (p *SSHClientPool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	close(p.stopCh)

	var errs []error
	for k, entry := range p.conns {
		if entry.client != nil {
			if err := entry.client.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		delete(p.conns, k)
	}
	p.mu.Unlock()

	p.wg.Wait()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (p *SSHClientPool) keepAliveLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.cfg.KeepAlivePeriod)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.pingAll()
		}
	}
}

func (p *SSHClientPool) pingAll() {
	p.mu.RLock()
	entries := make([]*pooledEntry, 0, len(p.conns))
	for _, entry := range p.conns {
		entries = append(entries, entry)
	}
	p.mu.RUnlock()

	var deadKeys []string
	for _, entry := range entries {
		if entry.client != nil && !entry.client.IsAlive() {
			deadKeys = append(deadKeys, entry.key)
		}
	}

	if len(deadKeys) > 0 {
		p.mu.Lock()
		for _, key := range deadKeys {
			if entry, ok := p.conns[key]; ok {
				if entry.client != nil {
					_ = entry.client.Close()
				}
				delete(p.conns, key)
			}
		}
		p.mu.Unlock()
	}
}

func (p *SSHClientPool) sweepLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.cfg.SweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.sweepIdle()
		}
	}
}

func (p *SSHClientPool) sweepIdle() {
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()

	for key, entry := range p.conns {
		if now.Sub(entry.lastActive) > p.cfg.IdleTimeout {
			if entry.client != nil {
				_ = entry.client.Close()
			}
			delete(p.conns, key)
		}
	}
}
