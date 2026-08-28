package tunnel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestReconnectManager(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	pool := NewPool(db)

	s1ID, _ := db.CreateServer(ctx, &models.Server{Name: "Host 1", Host: "1.1.1.1"})
	_, _ = pool.AddTunnel(ctx, s1ID, "1.1.1.1:51820", "pub1")

	var mockLatency time.Duration = 0
	var mockErr error = errors.New("timeout")

	mockProbe := func(ctx context.Context, endpoint string, serverPubKey string, clientPrivKey string, psk string, h1, h2 uint32, s1, s2 int, timeout time.Duration) (time.Duration, error) {
		return mockLatency, mockErr
	}

	hCfg := HealthConfig{
		Timeout:            500 * time.Millisecond,
		LatencyThresholdMS: 200,
	}
	prober := NewHealthProber(pool, db, hCfg, mockProbe)

	rCfg := ReconnectConfig{
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
		Multiplier:     2.0,
		MaxRetries:     3,
		CheckInterval:  20 * time.Millisecond,
	}

	reconnectMgr := NewReconnectManager(pool, prober, rCfg)

	simTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reconnectMgr.SetNowFunc(func() time.Time { return simTime })

	// 1. Initial state: tunnel is active, so CheckAndReconnect does nothing
	reconnected := reconnectMgr.CheckAndReconnect(ctx)
	if reconnected != 0 {
		t.Errorf("expected 0 reconnected for active tunnel, got %d", reconnected)
	}

	// 2. Mark tunnel as degraded
	_ = pool.SetTunnelStatus(ctx, s1ID, "degraded", 0)

	// 3. First reconnect attempt (fails)
	reconnected = reconnectMgr.CheckAndReconnect(ctx)
	if reconnected != 0 {
		t.Errorf("expected 0 reconnected on failed probe, got %d", reconnected)
	}
	retries, nextAttempt, backoff := reconnectMgr.GetTunnelRetryState(s1ID)
	if retries != 1 || backoff != 100*time.Millisecond {
		t.Errorf("retry state mismatch: retries=%d, backoff=%v", retries, backoff)
	}
	if nextAttempt.IsZero() {
		t.Errorf("expected non-zero nextAttempt")
	}

	// 4. Immediate second pass before backoff elapsed -> should skip
	simTime = simTime.Add(40 * time.Millisecond) // only 40ms passed, need 100ms
	reconnected = reconnectMgr.CheckAndReconnect(ctx)
	if reconnected != 0 {
		t.Errorf("expected 0 reconnected when skipping, got %d", reconnected)
	}
	retries, _, _ = reconnectMgr.GetTunnelRetryState(s1ID)
	if retries != 1 {
		t.Errorf("expected retries to remain 1 before backoff expires, got %d", retries)
	}

	// 5. Advance time past backoff (100ms), second attempt fails
	simTime = simTime.Add(70 * time.Millisecond) // total 110ms > 100ms
	reconnectMgr.CheckAndReconnect(ctx)
	retries, _, backoff = reconnectMgr.GetTunnelRetryState(s1ID)
	if retries != 2 || backoff != 200*time.Millisecond { // 100 * 2.0 = 200 (capped at MaxBackoff 200ms)
		t.Errorf("retry state mismatch after 2nd attempt: retries=%d, backoff=%v", retries, backoff)
	}

	// 6. Third attempt fails -> reaches MaxRetries (3)
	simTime = simTime.Add(210 * time.Millisecond) // total elapsed > nextAttempt
	reconnectMgr.CheckAndReconnect(ctx)
	retries, _, _ = reconnectMgr.GetTunnelRetryState(s1ID)
	if retries != 3 {
		t.Errorf("expected retries 3, got %d", retries)
	}

	// 7. MaxRetries reached -> subsequent pass skips even after time advances
	simTime = simTime.Add(500 * time.Millisecond)
	reconnectMgr.CheckAndReconnect(ctx)
	retries, _, _ = reconnectMgr.GetTunnelRetryState(s1ID)
	if retries != 3 {
		t.Errorf("expected retries to remain 3 after MaxRetries cap, got %d", retries)
	}

	// 8. Reconnect with success
	mockErr = nil
	mockLatency = 20 * time.Millisecond
	// Reset retries by allowing unlimited retries safely
	rCfgUnlimited := rCfg
	rCfgUnlimited.MaxRetries = 0
	reconnectMgr.SetConfig(rCfgUnlimited)
	simTime = simTime.Add(300 * time.Millisecond)

	reconnected = reconnectMgr.CheckAndReconnect(ctx)
	if reconnected != 1 {
		t.Errorf("expected 1 reconnected tunnel, got %d", reconnected)
	}

	t1Status, _ := pool.GetTunnel(s1ID)
	if t1Status.Status != "active" {
		t.Errorf("expected tunnel status active after reconnect, got %s", t1Status.Status)
	}
	retries, _, _ = reconnectMgr.GetTunnelRetryState(s1ID)
	if retries != 0 {
		t.Errorf("expected retries reset to 0, got %d", retries)
	}

	// 9. Lifecycle Start and Stop
	reconnectMgr.SetNowFunc(nil) // restore real clock
	reconnectMgr.Start(ctx)
	if !reconnectMgr.IsRunning() {
		t.Errorf("expected running")
	}
	// Double start noop
	reconnectMgr.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	reconnectMgr.Stop()
	if reconnectMgr.IsRunning() {
		t.Errorf("expected not running after Stop")
	}
	// Double stop noop
	reconnectMgr.Stop()

	// Default config
	defCfg := DefaultReconnectConfig()
	if defCfg.InitialBackoff != 1*time.Second || defCfg.Multiplier != 2.0 {
		t.Errorf("DefaultReconnectConfig mismatch")
	}
	nilMgr := NewReconnectManager(nil, nil, defCfg)
	if nilMgr.CheckAndReconnect(ctx) != 0 {
		t.Errorf("expected 0 for nil pool reconnect")
	}
}
