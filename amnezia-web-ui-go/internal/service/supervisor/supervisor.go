package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// BackgroundService defines the interface for a managed background worker.
type BackgroundService interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// WorkerStatus represents the health and execution metadata for a supervised task.
type WorkerStatus struct {
	Name         string     `json:"name"`
	Running      bool       `json:"running"`
	CrashCount   int        `json:"crash_count"`
	RestartCount int        `json:"restart_count"`
	LastSuccess  *time.Time `json:"last_success,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	Tripped      bool       `json:"tripped"`
}

// Option configures the Supervisor instance.
type Option func(*Supervisor)

// WithMaxRestarts configures the maximum restarts allowed within the sliding window.
func WithMaxRestarts(maxRestarts int) Option {
	return func(s *Supervisor) {
		if maxRestarts >= 0 {
			s.maxRestarts = maxRestarts
		}
	}
}

// WithRestartWindow configures the sliding window duration.
func WithRestartWindow(window time.Duration) Option {
	return func(s *Supervisor) {
		if window > 0 {
			s.restartWindow = window
		}
	}
}

// WithRestartDelay configures the delay before restarting a crashed worker.
func WithRestartDelay(delay time.Duration) Option {
	return func(s *Supervisor) {
		if delay >= 0 {
			s.restartDelay = delay
		}
	}
}

type workerEntry struct {
	name        string
	fn          func(ctx context.Context) error
	service     BackgroundService
	crashCount  int
	restarts    []time.Time
	lastSuccess *time.Time
	lastError   error
	running     bool
	tripped     bool
	stopFunc    func(ctx context.Context) error
}

// Supervisor coordinates background service lifecycles with panic recovery and sliding window restarts.
type Supervisor struct {
	mu            sync.RWMutex
	maxRestarts   int
	restartWindow time.Duration
	restartDelay  time.Duration
	workers       map[string]*workerEntry
	cancel        context.CancelFunc
	running       bool
	totalCrashes  int
	lastError     error
	lastSuccess   *time.Time
}

// New creates a new Supervisor instance with default or configured options.
func New(opts ...Option) *Supervisor {
	s := &Supervisor{
		maxRestarts:   3,
		restartWindow: 300 * time.Second,
		restartDelay:  5 * time.Second,
		workers:       make(map[string]*workerEntry),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Register registers a named worker function to be supervised.
func (s *Supervisor) Register(name string, fn func(ctx context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[name] = &workerEntry{
		name: name,
		fn:   fn,
	}
}

// RegisterService registers a BackgroundService to be supervised.
func (s *Supervisor) RegisterService(svc BackgroundService) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workers[svc.Name()] = &workerEntry{
		name:     svc.Name(),
		service:  svc,
		fn:       svc.Start,
		stopFunc: svc.Stop,
	}
}

// Start runs all registered workers under supervision in isolated goroutines.
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("supervisor is already running")
	}
	subCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.running = true

	entries := make([]*workerEntry, 0, len(s.workers))
	for _, w := range s.workers {
		entries = append(entries, w)
	}
	s.mu.Unlock()

	g, gCtx := errgroup.WithContext(subCtx)

	for _, entry := range entries {
		e := entry
		g.Go(func() error {
			s.runWorkerLoop(gCtx, e)
			return nil
		})
	}

	err := g.Wait()
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	return err
}

// Stop stops the supervisor and signals all workers to terminate gracefully.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.running = false
	entries := make([]*workerEntry, 0, len(s.workers))
	for _, w := range s.workers {
		entries = append(entries, w)
	}
	s.mu.Unlock()

	for _, e := range entries {
		if e.stopFunc != nil {
			if err := e.stopFunc(ctx); err != nil {
				slog.Warn("Error stopping supervised worker", "worker", e.name, "err", err)
			}
		}
	}
	return nil
}

// RunWithRecovery runs a single worker function with panic recovery and sliding window restarts.
func (s *Supervisor) RunWithRecovery(ctx context.Context, name string, taskFn func(ctx context.Context) error) {
	s.mu.Lock()
	entry, exists := s.workers[name]
	if !exists {
		entry = &workerEntry{name: name, fn: taskFn}
		s.workers[name] = entry
	}
	s.mu.Unlock()

	s.runWorkerLoop(ctx, entry)
}

func (s *Supervisor) runWorkerLoop(ctx context.Context, e *workerEntry) {
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			e.running = false
			s.mu.Unlock()
			slog.Info("Supervisor received shutdown signal for worker", "worker", e.name)
			return
		default:
		}

		s.mu.Lock()
		e.running = true
		s.mu.Unlock()

		err := s.executeSafely(ctx, e)

		if err == nil || errors.Is(err, context.Canceled) {
			s.mu.Lock()
			e.running = false
			now := time.Now().UTC()
			e.lastSuccess = &now
			s.lastSuccess = &now
			s.mu.Unlock()
			slog.Info("Worker exited cleanly", "worker", e.name)
			return
		}

		// An error or panic occurred
		s.mu.Lock()
		e.crashCount++
		s.totalCrashes++
		e.lastError = err
		s.lastError = err

		now := time.Now().UTC()
		cutoff := now.Add(-s.restartWindow)

		// Prune sliding window for worker
		var recent []time.Time
		for _, t := range e.restarts {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		e.restarts = recent

		// Check if allowed to restart
		if len(e.restarts) >= s.maxRestarts {
			e.tripped = true
			e.running = false
			s.mu.Unlock()
			slog.Error("Worker exceeded maximum restart limit; tripping circuit breaker",
				"worker", e.name,
				"crashes", e.crashCount,
				"max_restarts", s.maxRestarts,
				"window_sec", s.restartWindow.Seconds(),
			)
			return
		}

		e.restarts = append(e.restarts, now)
		restartNum := len(e.restarts)
		delay := s.restartDelay
		s.mu.Unlock()

		slog.Warn("Restarting crashed worker after delay",
			"worker", e.name,
			"restart_num", restartNum,
			"max_restarts", s.maxRestarts,
			"delay", delay,
			"err", err,
		)

		select {
		case <-ctx.Done():
			s.mu.Lock()
			e.running = false
			s.mu.Unlock()
			return
		case <-time.After(delay):
		}
	}
}

func (s *Supervisor) executeSafely(ctx context.Context, e *workerEntry) (taskErr error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Worker panicked", "worker", e.name, "panic", r)
			taskErr = fmt.Errorf("panic: %v", r)
		}
	}()
	return e.fn(ctx)
}

// IsHealthy returns true if no supervised workers have tripped their circuit breaker or exhausted restarts.
func (s *Supervisor) IsHealthy() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()
	cutoff := now.Add(-s.restartWindow)

	for _, w := range s.workers {
		if w.tripped {
			return false
		}
		var recent int
		for _, t := range w.restarts {
			if t.After(cutoff) {
				recent++
			}
		}
		if recent > s.maxRestarts {
			return false
		}
	}
	return true
}

// CrashCount returns total crash count across all workers.
func (s *Supervisor) CrashCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalCrashes
}

// RestartCount returns total restart attempts recorded across all workers.
func (s *Supervisor) RestartCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, w := range s.workers {
		total += len(w.restarts)
	}
	return total
}

// LastSuccessTime returns the timestamp of the last cleanly completed worker.
func (s *Supervisor) LastSuccessTime() *time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastSuccess
}

// LastError returns the most recent error/panic recorded.
func (s *Supervisor) LastError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastError
}

// WorkerStatuses returns a snapshot map of all worker statuses.
func (s *Supervisor) WorkerStatuses() map[string]WorkerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	statuses := make(map[string]WorkerStatus, len(s.workers))
	for name, w := range s.workers {
		var lastErrStr string
		if w.lastError != nil {
			lastErrStr = w.lastError.Error()
		}
		statuses[name] = WorkerStatus{
			Name:         name,
			Running:      w.running,
			CrashCount:   w.crashCount,
			RestartCount: len(w.restarts),
			LastSuccess:  w.lastSuccess,
			LastError:    lastErrStr,
			Tripped:      w.tripped,
		}
	}
	return statuses
}
