package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/health"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

// CheckBackendTunnelHealth probes active backend AWG tunnels via pure-Go Noise IK handshakes.
func (o *Orchestrator) CheckBackendTunnelHealth(ctx context.Context) error {
	if o.db == nil {
		return errors.New("database is not configured")
	}

	tunnels, err := o.db.GetBackendTunnels(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch backend tunnels: %w", err)
	}

	if len(tunnels) == 0 {
		return nil
	}

	vpnCfg, _ := o.db.GetVPNConfig(ctx)
	latencyThreshold := int64(500)
	if vpnCfg != nil && vpnCfg.HealthThresholdMS > 0 {
		latencyThreshold = int64(vpnCfg.HealthThresholdMS)
	}

	probeFn := o.probeFn
	if probeFn == nil {
		probeFn = health.ProbeAWGEndpoint
	}

	var degradedTunnels []int64
	var healthyTunnels []*models.BackendTunnel

	for _, t := range tunnels {
		if strings.EqualFold(t.Status, "disabled") {
			continue
		}

		tCopy := t
		rtt, err := probeFn(
			ctx,
			t.Endpoint,
			t.PublicKey,
			t.PrivateKey,
			"",
			health.DefaultH1,
			health.DefaultH2,
			health.DefaultS1,
			health.DefaultS2,
			3*time.Second,
		)

		if err != nil {
			slog.Warn("Backend tunnel health probe failed", "tunnel_id", t.ID, "endpoint", t.Endpoint, "err", err)
			_ = o.db.UpdateBackendTunnelStatus(ctx, t.ID, "degraded", 0)
			degradedTunnels = append(degradedTunnels, t.ID)
			continue
		}

		latencyMS := int64(rtt.Milliseconds())
		if latencyMS <= 0 {
			latencyMS = 1
		}

		status := "active"
		if latencyMS > latencyThreshold {
			status = "degraded"
			degradedTunnels = append(degradedTunnels, t.ID)
		} else {
			healthyTunnels = append(healthyTunnels, &tCopy)
		}

		_ = o.db.UpdateBackendTunnelStatus(ctx, t.ID, status, latencyMS)
	}

	// Trigger failover / migration for sessions on degraded tunnels
	if len(degradedTunnels) > 0 && len(healthyTunnels) > 0 {
		sessions, err := o.db.GetActiveVPNSessions(ctx)
		if err == nil && len(sessions) > 0 {
			degradedMap := make(map[int64]bool)
			for _, tid := range degradedTunnels {
				degradedMap[tid] = true
			}

			migrated := 0
			hIdx := 0
			for _, s := range sessions {
				if degradedMap[s.BackendTunnelID] {
					target := healthyTunnels[hIdx%len(healthyTunnels)]
					hIdx++
					s.BackendTunnelID = target.ID
					s.Status = "connected"
					if err := o.db.CreateVPNSession(ctx, &s); err == nil {
						migrated++
					}
				}
			}
			if migrated > 0 {
				slog.Info("Migrated VPN sessions from degraded backend tunnels", "count", migrated)
			}
		}
	}

	return nil
}

// RebalanceVPNSessions detects load imbalance across backend tunnels and reassigns sessions to lighter backends.
func (o *Orchestrator) RebalanceVPNSessions(ctx context.Context) error {
	if o.db == nil {
		return errors.New("database is not configured")
	}

	tunnels, err := o.db.GetBackendTunnels(ctx)
	if err != nil {
		return fmt.Errorf("failed to query backend tunnels for rebalance: %w", err)
	}

	var activeTunnels []models.BackendTunnel
	for _, t := range tunnels {
		if strings.EqualFold(t.Status, "active") {
			activeTunnels = append(activeTunnels, t)
		}
	}

	if len(activeTunnels) < 2 {
		return nil
	}

	sessions, err := o.db.GetActiveVPNSessions(ctx)
	if err != nil {
		return fmt.Errorf("failed to query active sessions for rebalance: %w", err)
	}

	if len(sessions) == 0 {
		return nil
	}

	counts := make(map[int64]int)
	sessionsByTunnel := make(map[int64][]models.VPNSession)
	for _, s := range sessions {
		if s.BackendTunnelID > 0 {
			counts[s.BackendTunnelID]++
			sessionsByTunnel[s.BackendTunnelID] = append(sessionsByTunnel[s.BackendTunnelID], s)
		}
	}

	avg := float64(len(sessions)) / float64(len(activeTunnels))
	threshold := int(avg * 1.4) // >40% above average

	for _, t := range activeTunnels {
		count := counts[t.ID]
		if count > threshold && count > int(avg) {
			excess := count - int(avg)
			sessList := sessionsByTunnel[t.ID]
			drained := 0

			for i := 0; i < len(sessList) && drained < excess; i++ {
				// Find lighter active backend tunnel
				var targetTunnelID int64
				minCount := 999999
				for _, cand := range activeTunnels {
					if cand.ID != t.ID && counts[cand.ID] < minCount {
						minCount = counts[cand.ID]
						targetTunnelID = cand.ID
					}
				}

				s := sessList[i]
				s.Status = "draining"
				if targetTunnelID > 0 {
					s.BackendTunnelID = targetTunnelID
					counts[targetTunnelID]++
					counts[t.ID]--
				}
				if err := o.db.CreateVPNSession(ctx, &s); err == nil {
					drained++
				}
			}

			slog.Info("Rebalanced overloaded backend tunnel",
				"tunnel_id", t.ID,
				"session_count", count,
				"average", avg,
				"drained_sessions", drained,
			)
		}
	}

	return nil
}

// SyncRemnaWave delegates to the configured RemnaWave syncer.
func (o *Orchestrator) SyncRemnaWave(ctx context.Context) error {
	if o.remnawaveSyncer == nil {
		return nil
	}

	count, msg, err := o.remnawaveSyncer.Sync(ctx)
	if err != nil {
		slog.Warn("RemnaWave periodic sync encountered error", "err", err)
		return err
	}

	slog.Info("RemnaWave periodic sync completed", "synced_users", count, "msg", msg)
	return nil
}
