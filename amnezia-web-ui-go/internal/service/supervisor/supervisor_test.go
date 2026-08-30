package supervisor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type mockService struct {
	name      string
	startFunc func(ctx context.Context) error
	stopFunc  func(ctx context.Context) error
	stopped   int32
}

func (m *mockService) Name() string {
	return m.name
}

func (m *mockService) Start(ctx context.Context) error {
	if m.startFunc != nil {
		return m.startFunc(ctx)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (m *mockService) Stop(ctx context.Context) error {
	atomic.AddInt32(&m.stopped, 1)
	if m.stopFunc != nil {
		return m.stopFunc(ctx)
	}
	return nil
}

func TestSupervisor_LifecycleAndOptions(t *testing.T) {
	s := New(
		WithMaxRestarts(5),
		WithRestartWindow(10*time.Second),
		WithRestartDelay(10*time.Millisecond),
	)

	if s.maxRestarts != 5 {
		t.Errorf("expected maxRestarts 5, got %d", s.maxRestarts)
	}
	if s.restartWindow != 10*time.Second {
		t.Errorf("expected restartWindow 10s, got %v", s.restartWindow)
	}
	if s.restartDelay != 10*time.Millisecond {
		t.Errorf("expected restartDelay 10ms, got %v", s.restartDelay)
	}

	if !s.IsHealthy() {
		t.Error("new supervisor should be healthy")
	}
	if s.CrashCount() != 0 {
		t.Errorf("expected 0 crashes, got %d", s.CrashCount())
	}
	if s.RestartCount() != 0 {
		t.Errorf("expected 0 restarts, got %d", s.RestartCount())
	}
	if s.LastError() != nil {
		t.Errorf("expected nil last error, got %v", s.LastError())
	}
	if s.LastSuccessTime() != nil {
		t.Errorf("expected nil last success time, got %v", s.LastSuccessTime())
	}
}

func TestSupervisor_CleanExecution(t *testing.T) {
	s := New(WithRestartDelay(5 * time.Millisecond))

	var executed int32
	s.Register("clean-worker", func(ctx context.Context) error {
		atomic.StoreInt32(&executed, 1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("unexpected Start error: %v", err)
	}

	if atomic.LoadInt32(&executed) != 1 {
		t.Error("worker was not executed")
	}
	if !s.IsHealthy() {
		t.Error("supervisor should be healthy after clean run")
	}
	if s.LastSuccessTime() == nil {
		t.Error("last success time should not be nil")
	}
}

func TestSupervisor_DoubleStartError(t *testing.T) {
	s := New(WithRestartDelay(5 * time.Millisecond))
	s.Register("long-worker", func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = s.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	err := s.Start(ctx)
	if err == nil {
		t.Fatal("expected error on double start, got nil")
	}

	_ = s.Stop(context.Background())
}

func TestSupervisor_PanicRecoveryAndRestart(t *testing.T) {
	s := New(
		WithMaxRestarts(2),
		WithRestartWindow(500*time.Millisecond),
		WithRestartDelay(10*time.Millisecond),
	)

	var runCount int32
	s.Register("panic-worker", func(ctx context.Context) error {
		count := atomic.AddInt32(&runCount, 1)
		if count == 1 {
			panic("simulated fatal panic")
		}
		if count == 2 {
			return errors.New("simulated error")
		}
		// 3rd run: succeeds
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("supervisor Start failed: %v", err)
	}

	if atomic.LoadInt32(&runCount) != 3 {
		t.Fatalf("expected worker to run 3 times, ran %d times", runCount)
	}

	if s.CrashCount() != 2 {
		t.Errorf("expected 2 crashes, got %d", s.CrashCount())
	}
	if s.RestartCount() != 2 {
		t.Errorf("expected 2 restarts, got %d", s.RestartCount())
	}
	if !s.IsHealthy() {
		t.Error("supervisor should be healthy after successful third run")
	}
}

func TestSupervisor_CircuitBreakerTrips(t *testing.T) {
	s := New(
		WithMaxRestarts(3),
		WithRestartWindow(10*time.Second),
		WithRestartDelay(5*time.Millisecond),
	)

	var runCount int32
	s.Register("failing-worker", func(ctx context.Context) error {
		atomic.AddInt32(&runCount, 1)
		panic("always panics")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("supervisor Start returned error: %v", err)
	}

	// 1 initial run + 3 restarts = 4 runs total
	if atomic.LoadInt32(&runCount) != 4 {
		t.Fatalf("expected worker to run 4 times before tripping, ran %d times", runCount)
	}

	if s.IsHealthy() {
		t.Error("supervisor should report unhealthy when circuit breaker is tripped")
	}
	if s.CrashCount() != 4 {
		t.Errorf("expected 4 crashes, got %d", s.CrashCount())
	}
	if s.RestartCount() != 3 {
		t.Errorf("expected 3 restarts, got %d", s.RestartCount())
	}

	statuses := s.WorkerStatuses()
	wStatus, ok := statuses["failing-worker"]
	if !ok {
		t.Fatal("worker status not found")
	}
	if !wStatus.Tripped {
		t.Error("worker status should have Tripped=true")
	}
	if wStatus.Running {
		t.Error("tripped worker should have Running=false")
	}
}

func TestSupervisor_SlidingWindowPruning(t *testing.T) {
	s := New(
		WithMaxRestarts(2),
		WithRestartWindow(50*time.Millisecond),
		WithRestartDelay(10*time.Millisecond),
	)

	var runs int32
	s.Register("flaky-worker", func(ctx context.Context) error {
		curr := atomic.AddInt32(&runs, 1)
		if curr == 1 {
			return errors.New("error 1")
		}
		if curr == 2 {
			// Sleep past window
			time.Sleep(80 * time.Millisecond)
			return errors.New("error 2")
		}
		if curr == 3 {
			// Sleep past window again
			time.Sleep(80 * time.Millisecond)
			return errors.New("error 3")
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("supervisor Start failed: %v", err)
	}

	if atomic.LoadInt32(&runs) != 4 {
		t.Fatalf("expected 4 runs due to sliding window pruning, got %d", runs)
	}
	if !s.IsHealthy() {
		t.Error("supervisor should remain healthy since crashes were spaced apart")
	}
}

func TestSupervisor_RegisterServiceAndStop(t *testing.T) {
	s := New(WithRestartDelay(5 * time.Millisecond))
	mock := &mockService{
		name: "mock-bg-service",
		startFunc: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	s.RegisterService(mock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start(ctx)
	}()

	time.Sleep(20 * time.Millisecond)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer stopCancel()

	if err := s.Stop(stopCtx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if atomic.LoadInt32(&mock.stopped) != 1 {
		t.Error("mock service Stop was not called")
	}

	// Calling Stop again should be a safe no-op
	if err := s.Stop(stopCtx); err != nil {
		t.Errorf("second Stop call failed: %v", err)
	}
}

func TestSupervisor_RunWithRecoveryDirect(t *testing.T) {
	s := New(
		WithMaxRestarts(1),
		WithRestartWindow(100*time.Millisecond),
		WithRestartDelay(5*time.Millisecond),
	)

	var executed int32
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	s.RunWithRecovery(ctx, "direct-task", func(c context.Context) error {
		if atomic.AddInt32(&executed, 1) == 1 {
			panic("direct panic")
		}
		return nil
	})

	if atomic.LoadInt32(&executed) != 2 {
		t.Errorf("expected 2 executions, got %d", executed)
	}
	if s.CrashCount() != 1 {
		t.Errorf("expected 1 crash, got %d", s.CrashCount())
	}
}
