package forwarder

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
)

// TrafficAccountant aggregates in-memory Rx/Tx byte counts and batches periodic DB updates.
type TrafficAccountant struct {
	mu            sync.RWMutex
	db            *database.DB
	sessionRx     map[string]*atomic.Int64
	sessionTx     map[string]*atomic.Int64
	connRx        map[string]*atomic.Int64
	connTx        map[string]*atomic.Int64
	flushInterval time.Duration
	stopCh        chan struct{}
	wg            sync.WaitGroup
	running       bool
}

// NewTrafficAccountant creates a new TrafficAccountant.
func NewTrafficAccountant(db *database.DB, flushInterval time.Duration) *TrafficAccountant {
	if flushInterval <= 0 {
		flushInterval = 2 * time.Second
	}
	return &TrafficAccountant{
		db:            db,
		sessionRx:     make(map[string]*atomic.Int64),
		sessionTx:     make(map[string]*atomic.Int64),
		connRx:        make(map[string]*atomic.Int64),
		connTx:        make(map[string]*atomic.Int64),
		flushInterval: flushInterval,
		stopCh:        make(chan struct{}),
	}
}

// RecordRx accumulates incoming byte count for a session and its associated connection.
func (ta *TrafficAccountant) RecordRx(sessionID, connectionID string, bytes int64) {
	if bytes <= 0 {
		return
	}
	if sessionID != "" {
		ta.getOrCreateCounter(ta.sessionRx, sessionID).Add(bytes)
	}
	if connectionID != "" {
		ta.getOrCreateCounter(ta.connRx, connectionID).Add(bytes)
	}
}

// RecordTx accumulates outgoing byte count for a session and its associated connection.
func (ta *TrafficAccountant) RecordTx(sessionID, connectionID string, bytes int64) {
	if bytes <= 0 {
		return
	}
	if sessionID != "" {
		ta.getOrCreateCounter(ta.sessionTx, sessionID).Add(bytes)
	}
	if connectionID != "" {
		ta.getOrCreateCounter(ta.connTx, connectionID).Add(bytes)
	}
}

// GetSessionTraffic returns the currently buffered in-memory Rx and Tx bytes for a session.
func (ta *TrafficAccountant) GetSessionTraffic(sessionID string) (rx int64, tx int64) {
	ta.mu.RLock()
	defer ta.mu.RUnlock()
	if c, ok := ta.sessionRx[sessionID]; ok {
		rx = c.Load()
	}
	if c, ok := ta.sessionTx[sessionID]; ok {
		tx = c.Load()
	}
	return
}

// GetConnectionTraffic returns the currently buffered in-memory Rx and Tx bytes for a connection.
func (ta *TrafficAccountant) GetConnectionTraffic(connectionID string) (rx int64, tx int64) {
	ta.mu.RLock()
	defer ta.mu.RUnlock()
	if c, ok := ta.connRx[connectionID]; ok {
		rx = c.Load()
	}
	if c, ok := ta.connTx[connectionID]; ok {
		tx = c.Load()
	}
	return
}

// Flush persists all accumulated delta byte counters to the database.
func (ta *TrafficAccountant) Flush(ctx context.Context) error {
	if ta.db == nil {
		return nil
	}

	ta.mu.Lock()
	// Drain session counters
	sessionDeltas := make(map[string][2]int64)
	for sID, rxCounter := range ta.sessionRx {
		val := rxCounter.Swap(0)
		if val > 0 {
			deltas := sessionDeltas[sID]
			deltas[0] = val
			sessionDeltas[sID] = deltas
		}
	}
	for sID, txCounter := range ta.sessionTx {
		val := txCounter.Swap(0)
		if val > 0 {
			deltas := sessionDeltas[sID]
			deltas[1] = val
			sessionDeltas[sID] = deltas
		}
	}

	// Drain connection counters
	connDeltas := make(map[string][2]int64)
	for cID, rxCounter := range ta.connRx {
		val := rxCounter.Swap(0)
		if val > 0 {
			deltas := connDeltas[cID]
			deltas[0] = val
			connDeltas[cID] = deltas
		}
	}
	for cID, txCounter := range ta.connTx {
		val := txCounter.Swap(0)
		if val > 0 {
			deltas := connDeltas[cID]
			deltas[1] = val
			connDeltas[cID] = deltas
		}
	}
	ta.mu.Unlock()

	// Update DB sessions
	for sID, deltas := range sessionDeltas {
		_ = ta.db.UpdateVPNSessionTraffic(ctx, sID, deltas[0], deltas[1])
	}

	// Update DB connections and user traffic
	for cID, deltas := range connDeltas {
		rx := deltas[0]
		tx := deltas[1]
		_ = ta.db.UpdateConnectionTraffic(ctx, cID, rx, tx)

		conn, err := ta.db.GetConnection(ctx, cID)
		if err == nil && conn != nil {
			_ = ta.db.UpdateUserTraffic(ctx, conn.UserID, rx, tx)
		}
	}

	return nil
}

// Start launches the periodic background database synchronization loop.
func (ta *TrafficAccountant) Start(ctx context.Context) {
	ta.mu.Lock()
	if ta.running {
		ta.mu.Unlock()
		return
	}
	ta.running = true
	ta.stopCh = make(chan struct{})
	ta.mu.Unlock()

	ta.wg.Add(1)
	go ta.loop(ctx)
}

// Stop stops the background synchronization loop and executes a final flush.
func (ta *TrafficAccountant) Stop() error {
	ta.mu.Lock()
	if !ta.running {
		ta.mu.Unlock()
		return nil
	}
	ta.running = false
	close(ta.stopCh)
	ta.mu.Unlock()

	ta.wg.Wait()
	return ta.Flush(context.Background())
}

// IsRunning returns true if the accountant loop is active.
func (ta *TrafficAccountant) IsRunning() bool {
	ta.mu.RLock()
	defer ta.mu.RUnlock()
	return ta.running
}

func (ta *TrafficAccountant) getOrCreateCounter(m map[string]*atomic.Int64, key string) *atomic.Int64 {
	ta.mu.RLock()
	c, ok := m[key]
	ta.mu.RUnlock()
	if ok {
		return c
	}

	ta.mu.Lock()
	defer ta.mu.Unlock()
	if c, ok := m[key]; ok {
		return c
	}
	newC := &atomic.Int64{}
	m[key] = newC
	return newC
}

func (ta *TrafficAccountant) loop(ctx context.Context) {
	defer ta.wg.Done()
	ticker := time.NewTicker(ta.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ta.stopCh:
			return
		case <-ticker.C:
			_ = ta.Flush(ctx)
		}
	}
}
