package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager"
	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/health"
	"github.com/devops-igor/amnezia-web-ui-go/internal/service/remnawave"
	"github.com/devops-igor/amnezia-web-ui-go/internal/service/userops"
)

// ProtocolResolver resolves a ProtocolManager by protocol name.
type ProtocolResolver interface {
	Get(proto string) (manager.ProtocolManager, bool)
}

// RemnaWaveSyncer defines the interface for RemnaWave synchronization.
type RemnaWaveSyncer interface {
	Sync(ctx context.Context) (int, string, error)
}

// UserOpsService defines the interface for mass user operations.
type UserOpsService interface {
	PerformMassOperations(ctx context.Context, req userops.MassOperationRequest) error
}

type healthProbeKey struct {
	clientPriv string
	serverPub  string
	psk        string
}

// ProbeFunc defines the signature for Noise IK handshake UDP probes.
type ProbeFunc func(ctx context.Context, endpoint string, serverPubKey string, clientPrivKey string, psk string, h1, h2 uint32, s1, s2 int, timeout time.Duration) (time.Duration, error)

// Orchestrator coordinates scheduled background maintenance and telemetry tasks.
type Orchestrator struct {
	db              *database.DB
	registry        ProtocolResolver
	userOps         UserOpsService
	remnawaveSyncer RemnaWaveSyncer
	probeFn         ProbeFunc
	bootDelay       time.Duration
	interval        time.Duration
	maxConcurrency  int

	mu                sync.RWMutex
	reachabilityCache map[int64]map[string]any
	healthProbeKeys   map[int64]healthProbeKey

	running     bool
	cancel      context.CancelFunc
	stopCh      chan struct{}
	lastRun     *time.Time
	lastSuccess *time.Time
	lastError   error
}

// Option configures Orchestrator options.
type Option func(*Orchestrator)

// WithProbeFunc configures a custom handshake probe function for testing or simulation.
func WithProbeFunc(fn ProbeFunc) Option {
	return func(o *Orchestrator) {
		if fn != nil {
			o.probeFn = fn
		}
	}
}

// WithBootDelay configures initial delay before first background run.
func WithBootDelay(delay time.Duration) Option {
	return func(o *Orchestrator) {
		if delay >= 0 {
			o.bootDelay = delay
		}
	}
}

// WithInterval configures the recurring background ticker interval.
func WithInterval(interval time.Duration) Option {
	return func(o *Orchestrator) {
		if interval > 0 {
			o.interval = interval
		}
	}
}

// WithMaxConcurrency configures max parallel SSH workers across servers.
func WithMaxConcurrency(maxConcurrency int) Option {
	return func(o *Orchestrator) {
		if maxConcurrency > 0 {
			o.maxConcurrency = maxConcurrency
		}
	}
}

// WithRemnaWaveSyncer configures a custom RemnaWave syncer.
func WithRemnaWaveSyncer(syncer RemnaWaveSyncer) Option {
	return func(o *Orchestrator) {
		o.remnawaveSyncer = syncer
	}
}

// WithUserOps configures custom UserOpsService.
func WithUserOps(ops UserOpsService) Option {
	return func(o *Orchestrator) {
		o.userOps = ops
	}
}

// New creates a new BackgroundTaskOrchestrator.
func New(db *database.DB, registry ProtocolResolver, opts ...Option) *Orchestrator {
	var defaultUserOps UserOpsService
	if db != nil {
		defaultUserOps = userops.NewUserOpsService(db, registry)
	}

	var defaultSyncer RemnaWaveSyncer
	if db != nil {
		defaultSyncer = remnawave.NewSyncer(db, nil, defaultUserOps)
	}

	o := &Orchestrator{
		db:                db,
		registry:          registry,
		userOps:           defaultUserOps,
		remnawaveSyncer:   defaultSyncer,
		probeFn:           health.ProbeAWGEndpoint,
		bootDelay:         60 * time.Second,
		interval:          600 * time.Second,
		maxConcurrency:    10,
		reachabilityCache: make(map[int64]map[string]any),
		healthProbeKeys:   make(map[int64]healthProbeKey),
		stopCh:            make(chan struct{}),
	}

	for _, opt := range opts {
		opt(o)
	}

	return o
}

// Name returns the service identifier for supervisor registration.
func (o *Orchestrator) Name() string {
	return "background-orchestrator"
}

// Start launches the periodic orchestrator loop with boot delay.
func (o *Orchestrator) Start(ctx context.Context) error {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return errors.New("orchestrator is already running")
	}
	subCtx, cancel := context.WithCancel(ctx)
	o.cancel = cancel
	o.running = true
	o.stopCh = make(chan struct{})
	bootDelay := o.bootDelay
	interval := o.interval
	o.mu.Unlock()

	slog.Info("Background orchestrator started", "boot_delay", bootDelay, "interval", interval)

	// 1. Initial boot delay
	if bootDelay > 0 {
		select {
		case <-subCtx.Done():
			o.setStopped()
			return subCtx.Err()
		case <-o.stopCh:
			o.setStopped()
			return nil
		case <-time.After(bootDelay):
		}
	}

	// 2. Main periodic loop
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// First run after boot delay
	if err := o.RunAll(subCtx); err != nil && subCtx.Err() == nil {
		slog.Warn("Orchestrator initial RunAll encountered error", "err", err)
	}

	for {
		select {
		case <-subCtx.Done():
			o.setStopped()
			return subCtx.Err()
		case <-o.stopCh:
			o.setStopped()
			return nil
		case <-ticker.C:
			if err := o.RunAll(subCtx); err != nil && subCtx.Err() == nil {
				slog.Warn("Orchestrator periodic RunAll encountered error", "err", err)
			}
		}
	}
}

// Stop signals the orchestrator loop to terminate gracefully.
func (o *Orchestrator) Stop(ctx context.Context) error {
	o.mu.Lock()
	if !o.running {
		o.mu.Unlock()
		return nil
	}
	if o.cancel != nil {
		o.cancel()
	}
	select {
	case <-o.stopCh:
	default:
		close(o.stopCh)
	}
	o.running = false
	o.mu.Unlock()

	slog.Info("Background orchestrator stopped cleanly")
	return nil
}

func (o *Orchestrator) setStopped() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.running = false
}

// RunAll executes all periodic background operations with error isolation.
func (o *Orchestrator) RunAll(ctx context.Context) error {
	now := time.Now().UTC()
	o.mu.Lock()
	o.lastRun = &now
	o.mu.Unlock()

	operations := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"sync_traffic", o.SyncTraffic},
		{"check_server_reachability", func(c context.Context) error {
			_, err := o.CheckServerReachability(c)
			return err
		}},
		{"check_auto_trial_handshakes", o.CheckAutoTrialHandshakes},
		{"check_backend_tunnel_health", o.CheckBackendTunnelHealth},
		{"rebalance_vpn_sessions", o.RebalanceVPNSessions},
		{"sync_remnawave", o.SyncRemnaWave},
	}

	var combinedErrors []error

	for _, op := range operations {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := op.fn(ctx); err != nil {
			slog.Error("Background task operation failed", "operation", op.name, "err", err)
			combinedErrors = append(combinedErrors, fmt.Errorf("%s: %w", op.name, err))
		}
	}

	if len(combinedErrors) > 0 {
		joinErr := errors.Join(combinedErrors...)
		o.mu.Lock()
		o.lastError = joinErr
		o.mu.Unlock()
		return joinErr
	}

	o.mu.Lock()
	o.lastSuccess = &now
	o.lastError = nil
	o.mu.Unlock()
	return nil
}

// LastRun returns the timestamp of the last RunAll execution attempt.
func (o *Orchestrator) LastRun() *time.Time {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.lastRun
}

// LastSuccess returns the timestamp of the last successful RunAll execution.
func (o *Orchestrator) LastSuccess() *time.Time {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.lastSuccess
}

// LastError returns the error from the last RunAll execution, if any.
func (o *Orchestrator) LastError() error {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.lastError
}

// GetCachedServerReachability returns a snapshot of cached reachability test results.
func (o *Orchestrator) GetCachedServerReachability() map[int64]map[string]any {
	o.mu.RLock()
	defer o.mu.RUnlock()

	copyMap := make(map[int64]map[string]any, len(o.reachabilityCache))
	for k, v := range o.reachabilityCache {
		copyInner := make(map[string]any, len(v))
		for ik, iv := range v {
			copyInner[ik] = iv
		}
		copyMap[k] = copyInner
	}
	return copyMap
}
