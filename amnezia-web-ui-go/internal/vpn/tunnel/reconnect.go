package tunnel

import (
	"context"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// ReconnectConfig defines exponential backoff reconnection parameters.
type ReconnectConfig struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Multiplier     float64
	MaxRetries     int
	CheckInterval  time.Duration
}

// DefaultReconnectConfig returns default exponential backoff reconnection settings.
func DefaultReconnectConfig() ReconnectConfig {
	return ReconnectConfig{
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     60 * time.Second,
		Multiplier:     2.0,
		MaxRetries:     10,
		CheckInterval:  5 * time.Second,
	}
}

// ReconnectManager monitors degraded tunnels and executes backoff reconnection attempts.
type ReconnectManager struct {
	mu          sync.RWMutex
	pool        *Pool
	prober      *HealthProber
	cfg         ReconnectConfig
	backoffs    map[int64]time.Duration
	retries     map[int64]int
	nextAttempt map[int64]time.Time
	stopCh      chan struct{}
	wg          sync.WaitGroup
	running     bool
}

// NewReconnectManager initializes a new ReconnectManager.
func NewReconnectManager(pool *Pool, prober *HealthProber, cfg ReconnectConfig) *ReconnectManager {
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 1 * time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 60 * time.Second
	}
	if cfg.Multiplier <= 1.0 {
		cfg.Multiplier = 2.0
	}
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = 5 * time.Second
	}

	return &ReconnectManager{
		pool:        pool,
		prober:      prober,
		cfg:         cfg,
		backoffs:    make(map[int64]time.Duration),
		retries:     make(map[int64]int),
		nextAttempt: make(map[int64]time.Time),
		stopCh:      make(chan struct{}),
	}
}

// Start launches the background reconnection manager.
func (rm *ReconnectManager) Start(ctx context.Context) {
	rm.mu.Lock()
	if rm.running {
		rm.mu.Unlock()
		return
	}
	rm.running = true
	rm.stopCh = make(chan struct{})
	rm.mu.Unlock()

	rm.wg.Add(1)
	go rm.loop(ctx)
}

// Stop stops the reconnection loop.
func (rm *ReconnectManager) Stop() {
	rm.mu.Lock()
	if !rm.running {
		rm.mu.Unlock()
		return
	}
	rm.running = false
	close(rm.stopCh)
	rm.mu.Unlock()

	rm.wg.Wait()
}

// IsRunning returns true if the reconnection manager is active.
func (rm *ReconnectManager) IsRunning() bool {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return rm.running
}

// CheckAndReconnect performs an immediate pass over all degraded/disabled tunnels.
func (rm *ReconnectManager) CheckAndReconnect(ctx context.Context) int {
	if rm.pool == nil || rm.prober == nil {
		return 0
	}

	tunnels := rm.pool.ListTunnels()
	now := time.Now().UTC()
	reconnectedCount := 0

	for _, t := range tunnels {
		if t.Status == "active" {
			// Clear retry state if tunnel is active
			rm.mu.Lock()
			delete(rm.backoffs, t.ServerID)
			delete(rm.retries, t.ServerID)
			delete(rm.nextAttempt, t.ServerID)
			rm.mu.Unlock()
			continue
		}

		// Tunnel is degraded or disabled
		rm.mu.Lock()
		nextTime, exists := rm.nextAttempt[t.ServerID]
		retries := rm.retries[t.ServerID]

		if rm.cfg.MaxRetries > 0 && retries >= rm.cfg.MaxRetries {
			rm.mu.Unlock()
			continue
		}

		if exists && now.Before(nextTime) {
			rm.mu.Unlock()
			continue
		}

		// Ready to attempt reconnection probe
		backoff, hasBackoff := rm.backoffs[t.ServerID]
		if !hasBackoff {
			backoff = rm.cfg.InitialBackoff
		}
		rm.mu.Unlock()

		latency, err := rm.prober.ProbeTunnel(ctx, t)
		rm.mu.Lock()
		if err == nil && latency <= rm.prober.cfg.LatencyThresholdMS {
			// Successful reconnection!
			delete(rm.backoffs, t.ServerID)
			delete(rm.retries, t.ServerID)
			delete(rm.nextAttempt, t.ServerID)
			_ = rm.pool.SetTunnelStatus(ctx, t.ServerID, "active", latency)
			reconnectedCount++
		} else {
			// Failed, increase backoff
			nextBackoff := time.Duration(float64(backoff) * rm.cfg.Multiplier)
			if nextBackoff > rm.cfg.MaxBackoff {
				nextBackoff = rm.cfg.MaxBackoff
			}
			rm.backoffs[t.ServerID] = nextBackoff
			rm.retries[t.ServerID] = retries + 1
			rm.nextAttempt[t.ServerID] = now.Add(nextBackoff)
		}
		rm.mu.Unlock()
	}

	return reconnectedCount
}

// GetTunnelRetryState returns the current retry count and next attempt time for a tunnel.
func (rm *ReconnectManager) GetTunnelRetryState(serverID int64) (retries int, nextAttempt time.Time, backoff time.Duration) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	retries = rm.retries[serverID]
	nextAttempt = rm.nextAttempt[serverID]
	backoff = rm.backoffs[serverID]
	return
}

func (rm *ReconnectManager) loop(ctx context.Context) {
	defer rm.wg.Done()
	ticker := time.NewTicker(rm.cfg.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rm.stopCh:
			return
		case <-ticker.C:
			_ = rm.CheckAndReconnect(ctx)
		}
	}
}

// Helper model conversion if needed
var _ = models.BackendTunnel{}
