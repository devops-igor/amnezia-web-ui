# TASK-377: Phase 4E — VPN Endpoint & Load Balancing Subsystem

## Overview & Objective
Implement the complete in-process VPN endpoint and load balancing subsystem in `amnezia-web-ui-go/internal/vpn/`.

The portal acts as an AWG VPN endpoint where users connect directly to the portal over AWG (UDP port 51820), and traffic is dynamically load-balanced and forwarded across in-process AWG tunnels connected to healthy remote backend VPN servers (`awg-be-<server_id>`).

---

## 1. Specification References & Ground Truth
- [`docs/plans/2026-08-25-go-rewrite.md`](../../docs/plans/2026-08-25-go-rewrite.md) (Sections 2.8 and 3.4 / Phase 4E)
- [`docs/specs/01-domain-model.md`](../../docs/specs/01-domain-model.md)
- [`docs/specs/03-database.md`](../../docs/specs/03-database.md)
- [`docs/specs/04-external-services.md`](../../docs/specs/04-external-services.md)
- [`docs/specs/05-api-contract.md`](../../docs/specs/05-api-contract.md)
- [`docs/specs/06-background-jobs.md`](../../docs/specs/06-background-jobs.md)

---

## 2. Package Architecture & Sub-Task Requirements

```
amnezia-web-ui-go/internal/vpn/
├── vpn.go                         # Unified VPNService orchestrator & configuration loader
├── endpoint/
│   ├── listener.go                # In-process AWG endpoint listener, TUN management, UDP socket binding
│   ├── auth.go                    # Peer authentication against user_connections AWG registrations
│   ├── ipam.go                    # IP address allocation from subnet (e.g. 10.100.0.0/16)
│   └── session.go                 # Session manager (vpn_sessions creation, heartbeat, disconnect, drain)
├── tunnel/
│   ├── pool.go                    # Backend tunnel pool (awg-be-<server_id> interfaces, connect/teardown)
│   ├── health.go                  # Periodic Noise IK handshake health prober & latency tracker
│   └── reconnect.go               # Exponential backoff reconnection manager for degraded tunnels
├── loadbalancer/
│   ├── balancer.go                # LoadBalancer interface & router factory
│   ├── least_conn.go              # Least-Connections algorithm (default)
│   ├── weighted_rr.go             # Weighted Round-Robin algorithm
│   ├── round_robin.go             # Round-Robin algorithm
│   └── sticky.go                  # Sticky session affinity manager with automatic failover
└── forwarder/
    ├── forwarder.go               # Bidirectional packet relay between user session and backend tunnel
    └── accounting.go              # Real-time traffic accounting (Rx/Tx bytes sync to DB and user_connections)
```

---

### Detailed Deliverables

### A. AWG Endpoint Listener (`internal/vpn/endpoint/`)
1. **Listener Lifecycle (`listener.go`):**
   - Configurable listen port (default `51820`) and subnet (default `10.100.0.0/16`).
   - TUN device creation / abstract packet device interface for mock testing and Linux TUN interfaces.
   - Start / Stop / Graceful Drain lifecycle.
2. **Peer Authentication (`auth.go`):**
   - Validate connecting peer's public key against active `user_connections` with protocol `awg`.
   - Verify user account status (`enabled == true`, not expired).
3. **IP Address Management (`ipam.go`):**
   - Sequential and collision-free internal IP allocation from the configured subnet.
   - Release IP back to pool on session termination.
4. **Session Lifecycle (`session.go`):**
   - Create, update, and remove `vpn_sessions` records.
   - Handle connection timeouts, active disconnects, and draining states.

### B. Backend Tunnel Pool (`internal/vpn/tunnel/`)
1. **Tunnel Lifecycle & Pool (`pool.go`):**
   - Maintain active in-process AWG tunnels keyed by `server_id`.
   - Interface naming convention: `awg-be-<server_id>`.
   - Keypair management: generate Curve25519 keys, retrieve encrypted private keys from `backend_tunnels` table via `database.DB`.
2. **Health Monitoring (`health.go`):**
   - Execute periodic Noise IK handshake probes (reusing `internal/manager/awg/health`) to backend endpoints.
   - Measure RTT latency (ms) and update `backend_tunnels` status (`active`, `degraded`, `disabled`).
3. **Auto-Reconnect (`reconnect.go`):**
   - Exponential backoff retry loop for disconnected / degraded backend tunnels.

### C. Load Balancer (`internal/vpn/loadbalancer/`)
1. **Selection Algorithms:**
   - **Least-Connections (`least_conn.go`):** Select active backend tunnel with fewest active connections.
   - **Weighted Round-Robin (`weighted_rr.go`):** Distribute traffic proportionally based on `vpn_config.weights` (1–100).
   - **Round-Robin (`round_robin.go`):** Uniform sequential distribution.
2. **Health-Aware Routing & Sticky Sessions (`sticky.go`):**
   - Exclude `degraded` and `disabled` tunnels.
   - Maintain user-to-backend affinity unless assigned backend becomes degraded.
   - Seamless failover: reassign orphaned sessions to healthy backends.
   - Enforce capacity limits (`max_peers_per_backend`, `max_total_peers`).

### D. Traffic Forwarder & Accounting (`internal/vpn/forwarder/`)
1. **Packet Forwarding Engine (`forwarder.go`):**
   - Bidirectional packet forwarding between user sessions and assigned backend tunnels.
   - Context cancellation and clean worker shutdown.
2. **Traffic Accounting (`accounting.go`):**
   - Real-time aggregation of Rx/Tx bytes per session.
   - Periodic batch synchronization to SQLite `vpn_sessions` and `user_connections` traffic counters.

### E. Unified VPN Service (`internal/vpn/vpn.go`)
- Orchestrate `endpoint`, `tunnel`, `loadbalancer`, and `forwarder`.
- Expose methods for API handlers and background tasks:
  - `GetStatus(ctx) (*VPNStatus, error)`
  - `GetBackends(ctx) ([]*BackendTunnel, error)`
  - `EnableBackend(ctx, serverID int64) error`
  - `DisableBackend(ctx, serverID int64) error`
  - `GetTunnels(ctx) ([]*BackendTunnel, error)`
  - `GetConfig(ctx) (*models.VPNConfig, error)`
  - `UpdateConfig(ctx, cfg *models.VPNConfig) error`
  - `GetUserConnectionState(ctx, userID string) (*UserVPNState, error)`
  - `DisconnectUser(ctx, userID string) error`

---

## 3. Testing & Verification Requirements

1. **Unit & Table-Driven Tests:**
   - `endpoint/*_test.go`: IPAM allocation/exhaustion, peer authentication checks, session lifecycle.
   - `tunnel/*_test.go`: Tunnel pool concurrency, mock health check status transitions, reconnect backoff.
   - `loadbalancer/*_test.go`: Algorithmic correctness tests (Least-Conn, Weighted-RR distribution ratio, Round-Robin), sticky session persistence, failover rebalancing under simulated backend failure, capacity cap enforcement.
   - `forwarder/*_test.go`: Bidirectional packet loopback test, byte accounting precision, rate limit throttling.
   - `vpn_test.go`: End-to-end integration test with mock client, mock backend, and SQLite DB.
2. **Compilation & Quality Gates:**
   - `go fmt ./... && go vet ./... && go build ./...`
   - `go test -count=1 -race -cover ./internal/vpn/...` (Coverage $\ge 85.0\%$ across all `internal/vpn` packages)
   - `golangci-lint run ./...` (0 issues)
   - `gosec ./...` (0 issues)
   - `pytest tests/test_*.py -q` (all 1130 pass)

---

## 4. Handover Output
- Output your report to `tasks/issue-377-vpn-loadbalancer/DEV_HANDOVER.md`.
- Append status to `WORKLOG.md`.
