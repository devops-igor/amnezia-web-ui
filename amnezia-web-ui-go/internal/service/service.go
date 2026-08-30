package service

import (
	"context"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/service/orchestrator"
	"github.com/devops-igor/amnezia-web-ui-go/internal/service/reconciliation"
	"github.com/devops-igor/amnezia-web-ui-go/internal/service/remnawave"
	"github.com/devops-igor/amnezia-web-ui-go/internal/service/supervisor"
	"github.com/devops-igor/amnezia-web-ui-go/internal/service/userops"
)

// BackgroundService defines the contract for periodic or persistent background workers.
type BackgroundService = supervisor.BackgroundService

// Supervisor coordinates background service lifecycles and recovery.
type Supervisor = supervisor.Supervisor

// NewSupervisor creates a new supervisor with default configuration.
func NewSupervisor(opts ...supervisor.Option) *Supervisor {
	return supervisor.New(opts...)
}

// Orchestrator coordinates scheduled periodic tasks.
type Orchestrator = orchestrator.Orchestrator

// NewOrchestrator creates a new BackgroundTaskOrchestrator.
func NewOrchestrator(db *database.DB, registry orchestrator.ProtocolResolver, opts ...orchestrator.Option) *Orchestrator {
	return orchestrator.New(db, registry, opts...)
}

// Reconciler coordinates startup protocol reconciliation.
type Reconciler = reconciliation.Reconciler

// NewReconciler creates a new startup Reconciler.
func NewReconciler(db *database.DB, registry reconciliation.ProtocolResolver) *Reconciler {
	return reconciliation.New(db, registry)
}

// UserOpsService coordinates user mass operations.
type UserOpsService = userops.Service

// NewUserOpsService creates a new UserOpsService.
func NewUserOpsService(db *database.DB, registry userops.ProtocolResolver) *UserOpsService {
	return userops.NewUserOpsService(db, registry)
}

// RemnaWaveSyncer handles synchronization with RemnaWave.
type RemnaWaveSyncer = remnawave.Syncer

// NewRemnaWaveSyncer creates a new RemnaWave user syncer.
func NewRemnaWaveSyncer(db *database.DB, client remnawave.HTTPClient, ops remnawave.MassOperationExecutor) *RemnaWaveSyncer {
	return remnawave.NewSyncer(db, client, ops)
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
