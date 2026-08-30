package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/health"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"golang.org/x/sync/errgroup"
)

// AWGHealthProbeManager defines interface for AWG health probe client provisioning.
type AWGHealthProbeManager interface {
	GetServerPublicKey(ctx context.Context, server *models.Server) (string, error)
	GetServerPSK(ctx context.Context, server *models.Server) (string, error)
	GetClients(ctx context.Context, server *models.Server) ([]map[string]any, error)
	AddClient(ctx context.Context, server *models.Server, clientParams map[string]any) (map[string]any, error)
}

// CheckServerReachability probes all servers for network reachability via AWG Noise handshake or TCP.
func (o *Orchestrator) CheckServerReachability(ctx context.Context) (map[int64]map[string]any, error) {
	if o.db == nil {
		return nil, errors.New("database is not configured")
	}

	slog.Info("Starting background server reachability check...")

	servers, err := o.db.GetAllServers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch servers: %w", err)
	}

	results := make(map[int64]map[string]any)
	var mu sync.Mutex

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(o.maxConcurrency)

	for _, srv := range servers {
		server := srv
		g.Go(func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic recovered in reachability probe goroutine", "server_id", server.ID, "panic", r)
					res := map[string]any{
						"reachable":    false,
						"latency_ms":   0,
						"protocol":     "error",
						"last_checked": time.Now().UTC().Format(time.RFC3339),
						"error":        fmt.Sprintf("probe panicked: %v", r),
					}
					mu.Lock()
					results[server.ID] = res
					mu.Unlock()
				}
			}()
			res := o.probeSingleServer(gCtx, &server)
			mu.Lock()
			results[server.ID] = res
			mu.Unlock()
			return nil
		})
	}

	_ = g.Wait()

	o.mu.Lock()
	o.reachabilityCache = results
	o.mu.Unlock()

	return results, nil
}

func (o *Orchestrator) probeSingleServer(ctx context.Context, server *models.Server) map[string]any {
	host := server.Host
	if host == "" {
		return map[string]any{
			"reachable":    false,
			"latency_ms":   0,
			"protocol":     "tcp",
			"last_checked": time.Now().UTC().Format(time.RFC3339),
			"error":        "empty host address",
		}
	}

	nowStr := time.Now().UTC().Format(time.RFC3339)
	var preservedAutoTrials map[string]map[string]any

	// 1. If AWG is installed on server, attempt Noise IK handshake probe
	if awgInfo, ok := server.Protocols["awg"].(map[string]any); ok && awgInfo != nil {
		port := extractAWGPort(awgInfo)
		serverPub, clientPriv, psk := o.resolveAWGProbeKeys(ctx, server, awgInfo)
		awgParams, _ := awgInfo["awg_params"].(map[string]any)

		if serverPub != "" {
			reachRes, autoTrials, ok := o.probeAWGHealth(ctx, server, host, port, serverPub, clientPriv, psk, awgParams)
			if ok {
				return reachRes
			}
			preservedAutoTrials = autoTrials
		}
	}

	// 2. Fallback to TCP SSH port probe
	return o.probeTCPFallback(ctx, server, host, nowStr, preservedAutoTrials)
}

func extractAWGPort(awgInfo map[string]any) int {
	port := 55424
	if pVal, ok := awgInfo["port"]; ok && pVal != nil {
		if p, err := strconv.Atoi(fmt.Sprint(pVal)); err == nil && p > 0 {
			port = p
		}
	}
	return port
}

func (o *Orchestrator) resolveAWGProbeKeys(ctx context.Context, server *models.Server, awgInfo map[string]any) (string, string, string) {
	serverPub, _ := awgInfo["public_key"].(string)
	psk, _ := awgInfo["psk"].(string)

	o.mu.RLock()
	cached := o.healthProbeKeys[server.ID]
	o.mu.RUnlock()

	clientPriv := cached.clientPriv
	if serverPub == "" {
		serverPub = cached.serverPub
	}
	if psk == "" {
		psk = cached.psk
	}

	if (serverPub == "" || clientPriv == "") && o.registry != nil {
		if mgr, ok := o.registry.Get("awg"); ok {
			if hpm, isHealthProbeMgr := mgr.(AWGHealthProbeManager); isHealthProbeMgr {
				serverPub, clientPriv, psk = o.provisionMissingProbeKeys(ctx, server, hpm, serverPub, clientPriv, psk)
			}
		}
	}

	return serverPub, clientPriv, psk
}

func (o *Orchestrator) provisionMissingProbeKeys(ctx context.Context, server *models.Server, hpm AWGHealthProbeManager, serverPub, clientPriv, psk string) (string, string, string) {
	if serverPub == "" {
		if pub, err := hpm.GetServerPublicKey(ctx, server); err == nil && pub != "" {
			serverPub = pub
		}
	}
	if psk == "" {
		if p, err := hpm.GetServerPSK(ctx, server); err == nil && p != "" {
			psk = p
		}
	}
	if clientPriv == "" {
		clientPriv = o.resolveOrCreateHealthProbeClient(ctx, server, hpm)
	}

	if serverPub != "" || clientPriv != "" || psk != "" {
		o.mu.Lock()
		o.healthProbeKeys[server.ID] = healthProbeKey{
			clientPriv: clientPriv,
			serverPub:  serverPub,
			psk:        psk,
		}
		o.mu.Unlock()
	}

	return serverPub, clientPriv, psk
}

func (o *Orchestrator) resolveOrCreateHealthProbeClient(ctx context.Context, server *models.Server, hpm AWGHealthProbeManager) string {
	if clients, err := hpm.GetClients(ctx, server); err == nil {
		for _, cl := range clients {
			if uData, ok := cl["userData"].(map[string]any); ok && uData != nil {
				cName, _ := uData["clientName"].(string)
				if strings.EqualFold(strings.TrimSpace(cName), "health probe") {
					if priv, _ := uData["clientPrivateKey"].(string); priv != "" {
						return priv
					}
				}
			}
		}
	}

	slog.Info("Health Probe client not found on server; provisioning dedicated peer...", "server_id", server.ID)
	added, err := hpm.AddClient(ctx, server, map[string]any{
		"name":       "Health Probe",
		"clientName": "Health Probe",
	})
	if err == nil && added != nil {
		if confStr, ok := added["config"].(string); ok {
			if priv := extractPrivateKeyFromConfig(confStr); priv != "" {
				return priv
			}
		}
		if uData, ok := added["userData"].(map[string]any); ok && uData != nil {
			if priv, ok := uData["clientPrivateKey"].(string); ok {
				return priv
			}
		}
	}

	return ""
}

func extractPrivateKeyFromConfig(confStr string) string {
	for _, line := range strings.Split(confStr, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "PrivateKey") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func (o *Orchestrator) probeAWGHealth(ctx context.Context, server *models.Server, host string, port int, serverPub, clientPriv, psk string, awgParams map[string]any) (map[string]any, map[string]map[string]any, bool) {
	reachRes, err := health.PerformAWGHandshake(ctx, host, port, serverPub, clientPriv, psk, awgParams, "tls", 3*time.Second)
	if err != nil {
		return nil, nil, false
	}

	autoTrials, _ := health.RunAutoTrialProfiles(ctx, host, port, serverPub, clientPriv, psk, awgParams, 2*time.Second)
	reachRes["auto_trials"] = autoTrials

	if isReachable, _ := reachRes["reachable"].(bool); isReachable {
		_ = o.db.UpdateServerReachability(ctx, server.ID, models.ReachabilityOnline)
		return reachRes, autoTrials, true
	}
	return reachRes, autoTrials, false
}

func (o *Orchestrator) probeTCPFallback(ctx context.Context, server *models.Server, host string, nowStr string, preservedAutoTrials map[string]map[string]any) map[string]any {
	sshPort := server.SSHPort
	if sshPort <= 0 {
		sshPort = 22
	}

	addr := net.JoinHostPort(host, strconv.Itoa(sshPort))
	t0 := time.Now()

	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		res := map[string]any{
			"reachable":    false,
			"latency_ms":   0,
			"protocol":     "tcp",
			"last_checked": nowStr,
			"error":        err.Error(),
		}
		if preservedAutoTrials != nil {
			res["auto_trials"] = preservedAutoTrials
		}
		_ = o.db.UpdateServerReachability(ctx, server.ID, models.ReachabilityOffline)
		return res
	}
	_ = conn.Close()

	latency := time.Since(t0).Milliseconds()
	if latency <= 0 {
		latency = 1
	}

	res := map[string]any{
		"reachable":    true,
		"latency_ms":   latency,
		"protocol":     "tcp",
		"last_checked": nowStr,
	}
	if preservedAutoTrials != nil {
		res["auto_trials"] = preservedAutoTrials
	}

	_ = o.db.UpdateServerReachability(ctx, server.ID, models.ReachabilityOnline)
	return res
}
