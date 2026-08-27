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

	sup.Register(svc1)
	sup.Register(svc2)

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
