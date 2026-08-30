package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

var handshakeUnitRegex = regexp.MustCompile(`(?i)(\d+)\s*([a-zA-Z]+)`)

// MimicryRotator defines protocol managers that support rotating AWG DPI mimicry signatures.
type MimicryRotator interface {
	RotateMimicry(ctx context.Context, server *models.Server, clientID string) (string, error)
}

// NextMimicryProfile calculates the next mimicry profile in rotation order.
func NextMimicryProfile(current models.AWGMimicryProfile) models.AWGMimicryProfile {
	switch current {
	case models.AWGMimicryAuto, "":
		return models.AWGMimicryTLS
	case models.AWGMimicryTLS:
		return models.AWGMimicryQUIC
	case models.AWGMimicryQUIC:
		return models.AWGMimicryDNS
	case models.AWGMimicryDNS:
		return models.AWGMimicrySIP
	case models.AWGMimicrySIP:
		return models.AWGMimicryTLS
	default:
		return models.AWGMimicryTLS
	}
}

// CheckAutoTrialHandshakes scans AWG connections with auto mimicry and rotates stalled clients.
func (o *Orchestrator) CheckAutoTrialHandshakes(ctx context.Context) error {
	if o.db == nil {
		return errors.New("database is not configured")
	}

	conns, err := o.db.GetAllConnections(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch connections for auto-trial check: %w", err)
	}

	connsByServer := filterAutoTrialConns(conns)
	if len(connsByServer) == 0 {
		return nil
	}

	for serverID, sConns := range connsByServer {
		o.processServerAutoTrial(ctx, serverID, sConns)
	}

	return nil
}

func filterAutoTrialConns(conns []models.UserConnection) map[int64][]models.UserConnection {
	connsByServer := make(map[int64][]models.UserConnection)
	for _, c := range conns {
		if models.NormalizeProtocol(c.Protocol) == "awg" {
			if c.AWGMimicry == models.AWGMimicryAuto || c.AWGMimicry == "" {
				connsByServer[c.ServerID] = append(connsByServer[c.ServerID], c)
			}
		}
	}
	return connsByServer
}

func (o *Orchestrator) processServerAutoTrial(ctx context.Context, serverID int64, sConns []models.UserConnection) {
	if o.registry == nil {
		return
	}

	mgr, ok := o.registry.Get("awg")
	if !ok {
		return
	}

	rotator, ok := mgr.(MimicryRotator)
	if !ok {
		return
	}

	server, err := o.db.GetServer(ctx, serverID)
	if err != nil || server == nil {
		return
	}

	var handshakeAges map[string]time.Duration
	var hasHandshake map[string]bool
	if remoteClients, err := mgr.GetClients(ctx, server); err == nil {
		handshakeAges, hasHandshake = extractHandshakes(remoteClients)
	}

	for _, c := range sConns {
		if hasHandshake != nil && hasHandshake[c.ClientID] {
			if handshakeAges[c.ClientID] <= 180*time.Second {
				// Fresh active handshake (<= 180s); no rotation needed
				continue
			}
		}

		nextProf, err := rotator.RotateMimicry(ctx, server, c.ClientID)
		if err != nil || nextProf == "" {
			slog.Warn("Failed to rotate mimicry on remote server, preserving database record",
				"server_id", serverID,
				"client_id", c.ClientID,
				"err", err,
			)
			continue
		}

		_, _ = o.db.UpdateConnection(ctx, c.ID, map[string]any{
			"awg_mimicry": nextProf,
		})

		slog.Info("Auto-trial rotated mimicry for client",
			"connection_id", c.ID,
			"client_id", c.ClientID,
			"new_profile", nextProf,
		)
	}
}

func extractHandshakes(clients []map[string]any) (map[string]time.Duration, map[string]bool) {
	handshakeAges := make(map[string]time.Duration, len(clients))
	hasHandshake := make(map[string]bool, len(clients))
	for _, cl := range clients {
		cID, _ := cl["clientId"].(string)
		if uData, ok := cl["userData"].(map[string]any); ok && uData != nil {
			if age, ok := parseHandshakeAge(uData["latestHandshake"]); ok {
				handshakeAges[cID] = age
				hasHandshake[cID] = true
			}
		}
	}
	return handshakeAges, hasHandshake
}

// parseHandshakeAge calculates the elapsed age of a handshake value.
// It supports WireGuard human-readable strings (e.g. "12 seconds ago", "1 minute, 32 seconds ago",
// "2 hours, 10 minutes ago", "3 days, 4 hours ago", "now", "never"), Go duration strings, and Unix timestamps.
func parseHandshakeAge(val any) (time.Duration, bool) {
	if val == nil {
		return 0, false
	}

	switch v := val.(type) {
	case int, int64, float64, json.Number:
		return parseHandshakeNumeric(v)
	case string:
		return parseHandshakeString(v)
	default:
		return 0, false
	}
}

func parseHandshakeNumeric(val any) (time.Duration, bool) {
	switch v := val.(type) {
	case int:
		return parseTimestampAge(int64(v))
	case int64:
		return parseTimestampAge(v)
	case float64:
		return parseTimestampAge(int64(v))
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return parseTimestampAge(n)
		}
		if f, err := v.Float64(); err == nil {
			return parseTimestampAge(int64(f))
		}
	}
	return 0, false
}

func parseHandshakeString(raw string) (time.Duration, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "never") || strings.EqualFold(s, "(none)") || strings.EqualFold(s, "none") || s == "0" {
		return 0, false
	}
	if strings.EqualFold(s, "now") {
		return 0, true
	}

	// Try numeric string timestamp (e.g. "1725000000")
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return parseTimestampAge(n)
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return parseTimestampAge(int64(f))
	}

	// Try standard Go duration (e.g. "1m30s", "45s", "2h10m")
	if d, err := time.ParseDuration(s); err == nil {
		if d < 0 {
			d = -d
		}
		return d, true
	}

	// Try WireGuard human-readable format (e.g. "1 minute, 32 seconds ago", "3 days, 4 hours ago")
	return parseHandshakeUnits(s)
}

func parseHandshakeUnits(s string) (time.Duration, bool) {
	matches := handshakeUnitRegex.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return 0, false
	}

	var total time.Duration
	var matchedAny bool
	for _, m := range matches {
		amount, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		unit := strings.ToLower(m[2])
		switch {
		case strings.HasPrefix(unit, "d"): // day, days
			total += time.Duration(amount) * 24 * time.Hour
			matchedAny = true
		case strings.HasPrefix(unit, "h"): // hour, hours, hr, hrs
			total += time.Duration(amount) * time.Hour
			matchedAny = true
		case strings.HasPrefix(unit, "m") && !strings.HasPrefix(unit, "ms"): // minute, minutes, min, mins
			total += time.Duration(amount) * time.Minute
			matchedAny = true
		case strings.HasPrefix(unit, "s"): // second, seconds, sec, secs
			total += time.Duration(amount) * time.Second
			matchedAny = true
		}
	}
	if matchedAny {
		return total, true
	}
	return 0, false
}

func parseTimestampAge(ts int64) (time.Duration, bool) {
	if ts <= 0 {
		return 0, false
	}
	age := time.Since(time.Unix(ts, 0))
	if age < 0 {
		age = 0
	}
	return age, true
}
