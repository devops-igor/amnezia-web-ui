package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// BackgroundService defines the contract for periodic or persistent background workers.
type BackgroundService interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Supervisor coordinates background service lifecycles and recovery.
type Supervisor struct {
	mu       sync.Mutex
	services []BackgroundService
	cancel   context.CancelFunc
	running  bool
}

// NewSupervisor creates a new supervisor.
func NewSupervisor() *Supervisor {
	return &Supervisor{
		services: make([]BackgroundService, 0),
	}
}

// Register registers a background service with the supervisor.
func (s *Supervisor) Register(svc BackgroundService) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services = append(s.services, svc)
}

// Start launches all registered background services within a managed errgroup context.
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("supervisor is already running")
	}
	subCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.running = true
	services := make([]BackgroundService, len(s.services))
	copy(services, s.services)
	s.mu.Unlock()

	g, gCtx := errgroup.WithContext(subCtx)

	for _, svc := range services {
		service := svc
		g.Go(func() error {
			slog.Info("Starting background service", "name", service.Name())
			if err := service.Start(gCtx); err != nil && gCtx.Err() == nil {
				slog.Error("Background service stopped with error", "name", service.Name(), "err", err)
				return err
			}
			slog.Info("Background service stopped cleanly", "name", service.Name())
			return nil
		})
	}

	return g.Wait()
}

// Stop signals all background services to shut down.
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
	services := make([]BackgroundService, len(s.services))
	copy(services, s.services)
	s.mu.Unlock()

	for _, svc := range services {
		if err := svc.Stop(ctx); err != nil {
			slog.Warn("Error stopping background service", "name", svc.Name(), "err", err)
		}
	}
	return nil
}

// MockBackgroundService provides a stub service for testing supervisor orchestration.
type MockBackgroundService struct {
	ServiceName string
	Interval    time.Duration
	stopCh      chan struct{}
}

// NewMockBackgroundService creates a test background service.
func NewMockBackgroundService(name string) *MockBackgroundService {
	return &MockBackgroundService{
		ServiceName: name,
		Interval:    10 * time.Millisecond,
		stopCh:      make(chan struct{}),
	}
}

func (m *MockBackgroundService) Name() string {
	return m.ServiceName
}

func (m *MockBackgroundService) Start(ctx context.Context) error {
	ticker := time.NewTicker(m.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.stopCh:
			return nil
		case <-ticker.C:
			// simulate periodic work
		}
	}
}

func (m *MockBackgroundService) Stop(ctx context.Context) error {
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
	return nil
}
