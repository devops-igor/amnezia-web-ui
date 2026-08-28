package tunnel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestHealthProber(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	pool := NewPool(db)

	s1ID, _ := db.CreateServer(ctx, &models.Server{Name: "Host 1", Host: "1.1.1.1"})
	t1, err := pool.AddTunnel(ctx, s1ID, "1.1.1.1:51820", "pub1")
	if err != nil {
		t.Fatalf("AddTunnel failed: %v", err)
	}

	// Mock probe function
	var mockLatency time.Duration = 25 * time.Millisecond
	var mockErr error = nil

	mockProbe := func(ctx context.Context, endpoint string, serverPubKey string, clientPrivKey string, psk string, h1, h2 uint32, s1, s2 int, timeout time.Duration) (time.Duration, error) {
		return mockLatency, mockErr
	}

	cfg := HealthConfig{
		Interval:           50 * time.Millisecond,
		Timeout:            1 * time.Second,
		LatencyThresholdMS: 200,
		FailureThreshold:   2,
	}

	prober := NewHealthProber(pool, db, cfg, mockProbe)

	// 1. Successful probe with normal latency
	lat, err := prober.ProbeTunnel(ctx, t1)
	if err != nil || lat != 25 {
		t.Fatalf("ProbeTunnel success mismatch: lat=%d, err=%v", lat, err)
	}
	t1Status, _ := pool.GetTunnel(s1ID)
	if t1Status.Status != "active" || t1Status.LatencyMS != 25 {
		t.Errorf("status mismatch: status=%s, lat=%d", t1Status.Status, t1Status.LatencyMS)
	}

	// 2. High latency probe (> 200ms threshold)
	mockLatency = 350 * time.Millisecond
	lat, err = prober.ProbeTunnel(ctx, t1)
	if err != nil || lat != 350 {
		t.Fatalf("ProbeTunnel high latency failed: lat=%d, err=%v", lat, err)
	}
	t1Status, _ = pool.GetTunnel(s1ID)
	if t1Status.Status != "degraded" || t1Status.LatencyMS != 350 {
		t.Errorf("expected degraded status on high latency, got %s", t1Status.Status)
	}

	// 3. Failing probe 1 (below threshold -> degraded)
	mockErr = errors.New("connection timeout")
	_, err = prober.ProbeTunnel(ctx, t1)
	if err == nil {
		t.Errorf("expected error from failing probe")
	}
	t1Status, _ = pool.GetTunnel(s1ID)
	if t1Status.Status != "degraded" {
		t.Errorf("expected degraded on first failure, got %s", t1Status.Status)
	}

	// 4. Failing probe 2 (reaching failure threshold 2 -> disabled)
	_, err = prober.ProbeTunnel(ctx, t1)
	if err == nil {
		t.Errorf("expected error from failing probe")
	}
	t1Status, _ = pool.GetTunnel(s1ID)
	if t1Status.Status != "disabled" {
		t.Errorf("expected disabled on second failure, got %s", t1Status.Status)
	}

	// 5. Recovery probe (success restores active)
	mockErr = nil
	mockLatency = 15 * time.Millisecond
	lat, err = prober.ProbeTunnel(ctx, t1)
	if err != nil || lat != 15 {
		t.Fatalf("ProbeTunnel recovery failed: lat=%d, err=%v", lat, err)
	}
	t1Status, _ = pool.GetTunnel(s1ID)
	if t1Status.Status != "active" || t1Status.LatencyMS != 15 {
		t.Errorf("expected active after recovery, got %s", t1Status.Status)
	}

	// 6. Nil tunnel
	if _, err := prober.ProbeTunnel(ctx, nil); err == nil {
		t.Errorf("expected error for nil tunnel")
	}

	// 7. ProbeAll
	s2ID, _ := db.CreateServer(ctx, &models.Server{Name: "Host 2", Host: "2.2.2.2"})
	_, _ = pool.AddTunnel(ctx, s2ID, "2.2.2.2:51820", "pub2")

	allResults := prober.ProbeAll(ctx)
	if len(allResults) != 2 {
		t.Errorf("expected 2 probe results from ProbeAll, got %d", len(allResults))
	}

	// 8. Background loop lifecycle
	prober.Start(ctx)
	if !prober.IsRunning() {
		t.Errorf("expected prober to be running")
	}
	// Double start noop
	prober.Start(ctx)

	time.Sleep(120 * time.Millisecond)

	prober.Stop()
	if prober.IsRunning() {
		t.Errorf("expected prober to not be running after Stop")
	}
	// Double stop noop
	prober.Stop()

	// Default config values
	defCfg := DefaultHealthConfig()
	defProber := NewHealthProber(nil, nil, defCfg)
	if defProber.cfg.Interval != 10*time.Second {
		t.Errorf("DefaultHealthConfig mismatch")
	}
	if defProber.ProbeAll(ctx) != nil {
		t.Errorf("expected nil ProbeAll for nil pool")
	}
}
