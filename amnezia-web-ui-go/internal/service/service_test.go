package service

import (
	"context"
	"testing"
	"time"
)

func TestSupervisorLifecycle(t *testing.T) {
	sup := NewSupervisor()

	svc1 := NewMockBackgroundService("svc1")
	svc2 := NewMockBackgroundService("svc2")

	sup.RegisterService(svc1)
	sup.RegisterService(svc2)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- sup.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Stop supervisor
	cancel()

	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Errorf("unexpected error from supervisor: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for supervisor to stop")
	}

	// Double stop should be a no-op
	if err := sup.Stop(context.Background()); err != nil {
		t.Errorf("Stop failed: %v", err)
	}
}

func TestServicePackageDelegates(t *testing.T) {
	orch := NewOrchestrator(nil, nil)
	if orch == nil {
		t.Fatal("expected orchestrator instance")
	}

	reconciler := NewReconciler(nil, nil)
	if reconciler == nil {
		t.Fatal("expected reconciler instance")
	}

	userOps := NewUserOpsService(nil, nil)
	if userOps == nil {
		t.Fatal("expected userops instance")
	}

	syncer := NewRemnaWaveSyncer(nil, nil, nil)
	if syncer == nil {
		t.Fatal("expected syncer instance")
	}

	mock := NewMockBackgroundService("test-svc")
	if mock.Name() != "test-svc" {
		t.Errorf("expected test-svc, got %s", mock.Name())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_ = mock.Start(ctx)
	_ = mock.Stop(context.Background())
}
