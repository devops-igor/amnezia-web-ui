package tunnel

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/health"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// ProbeFunc is a function type for executing Noise IK handshake probes to a UDP endpoint.
type ProbeFunc func(ctx context.Context, endpoint string, serverPubKey string, clientPrivKey string, psk string, h1, h2 uint32, s1, s2 int, timeout time.Duration) (time.Duration, error)

// HealthConfig defines tuning parameters for the backend health prober.
type HealthConfig struct {
	Interval           time.Duration
	Timeout            time.Duration
	LatencyThresholdMS int64
	FailureThreshold   int
	H1                 uint32
	H2                 uint32
	S1                 int
	S2                 int
}

// DefaultHealthConfig returns standard default prober settings.
func DefaultHealthConfig() HealthConfig {
	return HealthConfig{
		Interval:           10 * time.Second,
		Timeout:            3 * time.Second,
		LatencyThresholdMS: 500,
		FailureThreshold:   3,
		H1:                 health.DefaultH1,
		H2:                 health.DefaultH2,
		S1:                 health.DefaultS1,
		S2:                 health.DefaultS2,
	}
}

// HealthProber periodically performs Noise IK handshake probes against backend tunnels.
type HealthProber struct {
	mu         sync.RWMutex
	pool       *Pool
	db         *database.DB
	cfg        HealthConfig
	probeFn    ProbeFunc
	failCounts map[int64]int
	stopCh     chan struct{}
	wg         sync.WaitGroup
	running    bool
}

// NewHealthProber initializes a new HealthProber instance.
func NewHealthProber(pool *Pool, db *database.DB, cfg HealthConfig, probeFn ...ProbeFunc) *HealthProber {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.LatencyThresholdMS <= 0 {
		cfg.LatencyThresholdMS = 500
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.H1 == 0 {
		cfg.H1 = health.DefaultH1
	}
	if cfg.H2 == 0 {
		cfg.H2 = health.DefaultH2
	}
	if cfg.S1 < 0 {
		cfg.S1 = health.DefaultS1
	}
	if cfg.S2 < 0 {
		cfg.S2 = health.DefaultS2
	}

	pFn := health.ProbeAWGEndpoint
	if len(probeFn) > 0 && probeFn[0] != nil {
		pFn = probeFn[0]
	}

	return &HealthProber{
		pool:       pool,
		db:         db,
		cfg:        cfg,
		probeFn:    pFn,
		failCounts: make(map[int64]int),
		stopCh:     make(chan struct{}),
	}
}

// SetProbeFunc updates the probe function used by the health prober.
func (hp *HealthProber) SetProbeFunc(fn ProbeFunc) {
	hp.mu.Lock()
	defer hp.mu.Unlock()
	if fn != nil {
		hp.probeFn = fn
	}
}

// Config returns a copy of the prober configuration.
func (hp *HealthProber) Config() HealthConfig {
	hp.mu.RLock()
	defer hp.mu.RUnlock()
	return hp.cfg
}

// ProbeTunnel executes a single Noise IK handshake probe against a backend tunnel and returns measured RTT.
func (hp *HealthProber) ProbeTunnel(ctx context.Context, tunnel *models.BackendTunnel) (int64, error) {
	if tunnel == nil {
		return 0, errors.New("tunnel is nil")
	}

	rtt, err := hp.probeFn(
		ctx,
		tunnel.Endpoint,
		tunnel.PublicKey,
		tunnel.PrivateKey,
		"",
		hp.cfg.H1,
		hp.cfg.H2,
		hp.cfg.S1,
		hp.cfg.S2,
		hp.cfg.Timeout,
	)

	latencyMS := int64(rtt.Milliseconds())
	if latencyMS <= 0 && err == nil {
		latencyMS = 1
	}

	hp.mu.Lock()
	defer hp.mu.Unlock()

	if err != nil {
		hp.failCounts[tunnel.ServerID]++
		failures := hp.failCounts[tunnel.ServerID]

		status := "degraded"
		if failures >= hp.cfg.FailureThreshold {
			status = "disabled"
		}

		if hp.pool != nil {
			_ = hp.pool.SetTunnelStatus(ctx, tunnel.ServerID, status, 0)
		}
		return 0, err
	}

	// Probe succeeded
	hp.failCounts[tunnel.ServerID] = 0
	status := "active"
	if latencyMS > hp.cfg.LatencyThresholdMS {
		status = "degraded"
	}

	if hp.pool != nil {
		_ = hp.pool.SetTunnelStatus(ctx, tunnel.ServerID, status, latencyMS)
	}

	return latencyMS, nil
}

// ProbeAll probes all backend tunnels concurrently and updates their statuses.
func (hp *HealthProber) ProbeAll(ctx context.Context) map[int64]error {
	if hp.pool == nil {
		return nil
	}

	tunnels := hp.pool.ListTunnels()
	results := make(map[int64]error)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, t := range tunnels {
		tunnel := t
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := hp.ProbeTunnel(ctx, tunnel)
			mu.Lock()
			results[tunnel.ServerID] = err
			mu.Unlock()
		}()
	}

	wg.Wait()
	return results
}

// Start launches the periodic background probing loop.
func (hp *HealthProber) Start(ctx context.Context) {
	hp.mu.Lock()
	if hp.running {
		hp.mu.Unlock()
		return
	}
	hp.running = true
	hp.stopCh = make(chan struct{})
	hp.mu.Unlock()

	hp.wg.Add(1)
	go hp.probingLoop(ctx)
}

// Stop terminates the background prober.
func (hp *HealthProber) Stop() {
	hp.mu.Lock()
	if !hp.running {
		hp.mu.Unlock()
		return
	}
	hp.running = false
	close(hp.stopCh)
	hp.mu.Unlock()

	hp.wg.Wait()
}

// IsRunning returns true if the prober is running.
func (hp *HealthProber) IsRunning() bool {
	hp.mu.RLock()
	defer hp.mu.RUnlock()
	return hp.running
}

func (hp *HealthProber) probingLoop(ctx context.Context) {
	defer hp.wg.Done()
	ticker := time.NewTicker(hp.cfg.Interval)
	defer ticker.Stop()

	// Initial probe sweep
	_ = hp.ProbeAll(ctx)

	for {
		select {
		case <-hp.stopCh:
			return
		case <-ticker.C:
			_ = hp.ProbeAll(ctx)
		}
	}
}
