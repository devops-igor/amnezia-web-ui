package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/internal/service/userops"
	"golang.org/x/sync/errgroup"
)

type connTrafficUpdate struct {
	connID  string
	rxDelta int64
	txDelta int64
	currRX  int64
	currTX  int64
}

// TelemtQuotaManager defines protocol managers capable of disabling over-quota proxy users.
type TelemtQuotaManager interface {
	DisableOverquotaUsers(ctx context.Context, server *models.Server) ([]string, error)
}

// SyncTraffic synchronizes bandwidth stats from all servers, handles 1st-of-month rollover,
// accumulates per-connection and user traffic, and enforces quota/expiry limits.
func (o *Orchestrator) SyncTraffic(ctx context.Context) error {
	if o.db == nil {
		return errors.New("database is not configured")
	}

	slog.Info("Starting background traffic sync...")

	servers, err := o.db.GetAllServers(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch servers: %w", err)
	}

	allConns, err := o.db.GetAllConnections(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch connections: %w", err)
	}

	connsByServer := make(map[int64][]models.UserConnection)
	for _, c := range allConns {
		connsByServer[c.ServerID] = append(connsByServer[c.ServerID], c)
	}

	var updatesMu sync.Mutex
	var updates []connTrafficUpdate

	// Query protocol managers concurrently across servers
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(o.maxConcurrency)

	for _, srv := range servers {
		server := srv
		serverConns, hasConns := connsByServer[server.ID]
		if !hasConns || len(server.Protocols) == 0 {
			continue
		}

		g.Go(func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic recovered in server traffic sync", "server_id", server.ID, "panic", r)
				}
			}()
			o.syncServerTraffic(gCtx, &server, serverConns, &updatesMu, &updates)
			return nil
		})
	}

	_ = g.Wait()

	now := time.Now().UTC()
	users, err := o.db.GetAllUsers(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch users: %w", err)
	}

	usersMap := make(map[string]*models.User, len(users))
	for i := range users {
		usersMap[users[i].ID] = &users[i]
	}

	toDisableUIDs := make(map[string]bool)

	// === 1. MONTHLY ROLLOVER: runs unconditionally every cycle ===
	o.handleMonthlyRollover(ctx, now, usersMap)

	// === 2. TRAFFIC DELTA PROCESSING ===
	if len(updates) > 0 {
		o.applyConnectionUpdates(ctx, now, updates, usersMap, toDisableUIDs)
	}

	// === 3. LIMIT & EXPIRATION CHECK: for all users in usersMap ===
	for _, user := range usersMap {
		if user.TrafficLimit > 0 && user.TrafficUsed >= user.TrafficLimit && user.Enabled {
			toDisableUIDs[user.ID] = true
		}
		if o.isUserExpired(user, now) && user.Enabled {
			toDisableUIDs[user.ID] = true
		}
	}

	// === 4. ENFORCE DISABLING OVER-LIMIT / EXPIRED USERS ===
	if len(toDisableUIDs) > 0 && o.userOps != nil {
		var toggleList []userops.UserToggle
		for uid := range toDisableUIDs {
			toggleList = append(toggleList, userops.UserToggle{UserID: uid, Enabled: false})
		}
		slog.Info("Disabling users reaching traffic limit or expiration", "count", len(toggleList))
		_ = o.userOps.PerformMassOperations(ctx, userops.MassOperationRequest{
			ToggleUIDs: toggleList,
		})
	}

	return nil
}

func (o *Orchestrator) applyConnectionUpdates(ctx context.Context, now time.Time, updates []connTrafficUpdate, usersMap map[string]*models.User, toDisableUIDs map[string]bool) {
	for _, u := range updates {
		uc, err := o.db.GetConnection(ctx, u.connID)
		if err != nil || uc == nil {
			continue
		}

		newConnTotalRx := uc.TrafficTotalRx + u.rxDelta
		newConnTotalTx := uc.TrafficTotalTx + u.txDelta
		newConnTotal := uc.TrafficTotal + u.rxDelta + u.txDelta

		_, _ = o.db.UpdateConnection(ctx, u.connID, map[string]any{
			"last_rx":          u.currRX,
			"last_tx":          u.currTX,
			"traffic_delta_rx": u.rxDelta,
			"traffic_delta_tx": u.txDelta,
			"traffic_total_rx": newConnTotalRx,
			"traffic_total_tx": newConnTotalTx,
			"traffic_total":    newConnTotal,
		})

		user, exists := usersMap[uc.UserID]
		if !exists || user == nil {
			continue
		}

		// Check user resettable strategy
		if o.isTrafficResetNeeded(user, now) {
			nowStr := now.Format(time.RFC3339)
			_, _ = o.db.UpdateUser(ctx, user.ID, map[string]any{
				"traffic_used":  0,
				"last_reset_at": nowStr,
			})
			user.TrafficUsed = 0
			user.LastResetAt = &nowStr
		}

		delta := u.rxDelta + u.txDelta
		newUsed := user.TrafficUsed + delta
		newTotal := user.TrafficTotal + delta
		newTotalRx := user.TrafficTotalRx + u.rxDelta
		newTotalTx := user.TrafficTotalTx + u.txDelta
		newMonthlyRx := user.MonthlyRx + u.rxDelta
		newMonthlyTx := user.MonthlyTx + u.txDelta

		_, _ = o.db.UpdateUser(ctx, user.ID, map[string]any{
			"traffic_used":     newUsed,
			"traffic_total":    newTotal,
			"traffic_total_rx": newTotalRx,
			"traffic_total_tx": newTotalTx,
			"monthly_rx":       newMonthlyRx,
			"monthly_tx":       newMonthlyTx,
		})

		user.TrafficUsed = newUsed
		user.TrafficTotal = newTotal
		user.TrafficTotalRx = newTotalRx
		user.TrafficTotalTx = newTotalTx
		user.MonthlyRx = newMonthlyRx
		user.MonthlyTx = newMonthlyTx

		// Check traffic quota limit
		if user.TrafficLimit > 0 && user.TrafficUsed >= user.TrafficLimit && user.Enabled {
			toDisableUIDs[user.ID] = true
		}
	}
}

func (o *Orchestrator) syncServerTraffic(ctx context.Context, server *models.Server, conns []models.UserConnection, mu *sync.Mutex, updates *[]connTrafficUpdate) {
	if o.registry == nil {
		return
	}

	for protoKey := range server.Protocols {
		proto := models.NormalizeProtocol(protoKey)
		mgr, ok := o.registry.Get(proto)
		if !ok {
			continue
		}

		clients, err := mgr.GetClients(ctx, server)
		if err != nil {
			slog.Warn("Failed to query clients from protocol manager", "server_id", server.ID, "protocol", proto, "err", err)
			continue
		}

		type clientBytes struct {
			rx int64
			tx int64
		}
		clientMap := make(map[string]clientBytes)

		for _, c := range clients {
			cid := fmt.Sprint(c["clientId"])
			if cid == "" || cid == "<nil>" {
				if idVal, ok := c["client_id"]; ok {
					cid = fmt.Sprint(idVal)
				}
			}
			if cid == "" || cid == "<nil>" {
				continue
			}

			var rx, tx int64
			if uData, ok := c["userData"].(map[string]any); ok {
				rx = extractBytes(uData["dataReceivedBytes"])
				tx = extractBytes(uData["dataSentBytes"])
			} else {
				rx = extractBytes(c["dataReceivedBytes"])
				tx = extractBytes(c["dataSentBytes"])
			}

			clientMap[cid] = clientBytes{rx: rx, tx: tx}
		}

		for _, uc := range conns {
			if models.NormalizeProtocol(uc.Protocol) != proto {
				continue
			}

			cb, exists := clientMap[uc.ClientID]
			if !exists {
				continue
			}

			currRX := cb.rx
			currTX := cb.tx

			lastRX := uc.LastRx
			lastTX := uc.LastTx

			rxDelta := computeDelta(currRX, lastRX)
			txDelta := computeDelta(currTX, lastTX)

			mu.Lock()
			*updates = append(*updates, connTrafficUpdate{
				connID:  uc.ID,
				rxDelta: rxDelta,
				txDelta: txDelta,
				currRX:  currRX,
				currTX:  currTX,
			})
			mu.Unlock()
		}

		// Check TeleMT quota manager over-quota disable
		if proto == "telemt" {
			if tqm, ok := mgr.(TelemtQuotaManager); ok {
				_, _ = tqm.DisableOverquotaUsers(ctx, server)
			}
		}
	}
}

func computeDelta(current, last int64) int64 {
	if current < last {
		// Remote container reset / host rebooted
		return current
	}
	return current - last
}

func parseTolerantTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("empty timestamp string")
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse timestamp: %q", s)
}

func (o *Orchestrator) handleMonthlyRollover(ctx context.Context, now time.Time, usersMap map[string]*models.User) {
	// 1. Take previous month leaderboard snapshot if month changed
	for _, u := range usersMap {
		if u.MonthlyResetAt != nil && *u.MonthlyResetAt != "" {
			if lastReset, err := parseTolerantTime(*u.MonthlyResetAt); err == nil {
				if now.Month() != lastReset.Month() || now.Year() != lastReset.Year() {
					snapshotYear := lastReset.Year()
					snapshotMonth := int(lastReset.Month())
					savedCount, err := o.db.SaveLeaderboardSnapshot(ctx, snapshotYear, snapshotMonth)
					if err == nil && savedCount > 0 {
						slog.Info("Saved leaderboard snapshot before monthly rollover",
							"year", snapshotYear,
							"month", snapshotMonth,
							"count", savedCount,
						)
					}
					break
				}
			} else {
				slog.Warn("Failed to parse monthly_reset_at timestamp during snapshot check", "raw", *u.MonthlyResetAt, "user_id", u.ID, "err", err)
			}
		}
	}

	// 2. Reset monthly counters for each user, and reset traffic_used for monthly strategy users
	nowStr := now.Format(time.RFC3339)
	for _, u := range usersMap {
		shouldReset := false
		if u.MonthlyResetAt == nil || *u.MonthlyResetAt == "" {
			shouldReset = true
		} else if lastReset, err := parseTolerantTime(*u.MonthlyResetAt); err == nil {
			if now.Month() != lastReset.Month() || now.Year() != lastReset.Year() {
				shouldReset = true
			}
		} else {
			slog.Warn("Failed to parse monthly_reset_at timestamp during user reset check", "raw", *u.MonthlyResetAt, "user_id", u.ID, "err", err)
		}

		if shouldReset {
			fields := map[string]any{
				"monthly_rx":       0,
				"monthly_tx":       0,
				"monthly_reset_at": nowStr,
			}
			if u.TrafficResetStrategy == models.ResetStrategyMonthly {
				fields["traffic_used"] = 0
				fields["last_reset_at"] = nowStr
				u.TrafficUsed = 0
				u.LastResetAt = &nowStr
			}
			_, _ = o.db.UpdateUser(ctx, u.ID, fields)
			u.MonthlyRx = 0
			u.MonthlyTx = 0
			u.MonthlyResetAt = &nowStr
		}
	}
}

func (o *Orchestrator) isTrafficResetNeeded(u *models.User, now time.Time) bool {
	strategy := u.TrafficResetStrategy
	if strategy == models.ResetStrategyNever || strategy == "" {
		return false
	}
	if u.LastResetAt == nil || *u.LastResetAt == "" {
		return false
	}

	last, err := parseTolerantTime(*u.LastResetAt)
	if err != nil {
		slog.Warn("Failed to parse last_reset_at timestamp", "raw", *u.LastResetAt, "user_id", u.ID, "err", err)
		return false
	}

	switch strategy {
	case models.ResetStrategyDaily:
		return now.Year() != last.Year() || now.YearDay() != last.YearDay()
	case "weekly":
		y1, w1 := now.ISOWeek()
		y2, w2 := last.ISOWeek()
		return y1 != y2 || w1 != w2
	case models.ResetStrategyMonthly:
		return now.Year() != last.Year() || now.Month() != last.Month()
	default:
		return false
	}
}

func (o *Orchestrator) isUserExpired(u *models.User, now time.Time) bool {
	if u.ExpiresAt != nil {
		return now.After(*u.ExpiresAt)
	}
	if u.ExpirationDate != nil {
		return now.After(*u.ExpirationDate)
	}
	return false
}

// CheckExpiry checks all active users in DB and disables any expired accounts.
func (o *Orchestrator) CheckExpiry(ctx context.Context) error {
	if o.db == nil {
		return errors.New("database is not configured")
	}

	users, err := o.db.GetAllUsers(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch users for expiry check: %w", err)
	}

	now := time.Now().UTC()
	var toDisable []userops.UserToggle

	for _, u := range users {
		if u.Enabled && o.isUserExpired(&u, now) {
			toDisable = append(toDisable, userops.UserToggle{UserID: u.ID, Enabled: false})
		}
	}

	if len(toDisable) > 0 && o.userOps != nil {
		slog.Info("Disabling expired users", "count", len(toDisable))
		_ = o.userOps.PerformMassOperations(ctx, userops.MassOperationRequest{
			ToggleUIDs: toDisable,
		})
	}

	return nil
}

func extractBytes(val any) int64 {
	if val == nil {
		return 0
	}
	switch v := val.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case string:
		if num, err := strconv.ParseInt(v, 10, 64); err == nil {
			return num
		}
	}
	return 0
}
