# Sub-Task Specification: VPN Endpoint & Load Balancing Improvements & Gap Fixes (`vpn_fixes.md`)

## Objective
Address the findings identified in [`DEV_VERIFICATION_REVIEW.md`](./DEV_VERIFICATION_REVIEW.md) to close functional gaps, deflake tests, harden session UUID consistency on peer replacement, fix lifecycle error propagation, and enhance `GenerateClientConfig`.

---

## 1. Scope of Fixes

### A. Finding F3: `GenerateClientConfig` Enhancement (`internal/vpn/vpn.go`)
- Update `GenerateClientConfig(ctx context.Context, userID string, serverID int64) (string, error)`:
  - Look up the user's active `user_connections` (or create/retrieve the client key and registered peer public key).
  - Use real portal/server endpoint host and configured VPN listen port (from `vpn_config.listen_port`, default 51820).
  - Use real AWG obfuscation parameters (`Jc`, `Jmin`, `Jmax`, `S1`, `S2`, `H1-H4`) and CPS signatures matching `internal/manager/awg`.

### B. Finding F4 & F2: Forwarder Rate Limiting & Packet Pump (`internal/vpn/forwarder/`)
- Add token bucket / rate limiter integration in `internal/vpn/forwarder/` to support per-peer bandwidth throttling (`limitDownBps`, `limitUpBps`).
- Add unit test verifying rate limit throttling in `forwarder_test.go`.
- Implement background pump routines (`StartPumps(ctx)`) connecting forwarder packet channels to abstract/real packet devices.

### C. Finding F5: Deflake `TestReconnectManager` (`internal/vpn/tunnel/reconnect_test.go`)
- Eliminate wall-clock race in `TestReconnectManager`:
  - Relax timing tolerances or inject virtual/stepped clock.
  - Guard `reconnectMgr.cfg` mutations with proper synchronization.
  - Ensure 100% test pass rate even under heavy concurrent system load and `-race`.

### D. Finding F6: Database Session UUID Synchronization on Peer Reconnect (`internal/database/vpn.go` & `internal/vpn/endpoint/session.go`)
- In `CreateVPNSession` / `session.go`:
  - Ensure `id = excluded.id` on conflict or sync the in-memory session UUID with the canonical database record ID.
  - Verify that `UpdateVPNSessionTraffic(sessionID, ...)` correctly matches the database row after peer reconnects.

### E. Finding F7 & F8: Lifecycle, Error Propagation & Cleanups (`internal/vpn/`)
- In `vpn.go`:
  - Propagate `endpoint.Start(ctx)` and `pool.SyncFromDB(ctx)` errors during `Service.Start(ctx)`.
- In `internal/vpn/tunnel/pool.go`:
  - Check `closed` flag in `AddTunnel` and return an error if the pool is closed.
- In `internal/vpn/tunnel/reconnect.go`:
  - Remove dead anchor `var _ = models.BackendTunnel{}` and unused import.
  - Access `prober.Config()` through accessor rather than direct struct field.

---

## 2. Compilation & Verification Gate (Hard Rule)
All of the following must pass with exit code 0:
- `go fmt ./... && go vet ./... && go build ./...`
- `go test -count=1 -race -cover ./internal/vpn/...` (Coverage $\ge 85.0\%$ across all packages)
- `golangci-lint run ./...` (0 issues)
- `gosec ./...` (0 issues)
- `pytest tests/test_*.py -q` (1130 passed)

---

## 3. Handover Output
- Output your report to `tasks/issue-377-vpn-loadbalancer/vpn_fixes_dev_handover.md`.
- Append status to `WORKLOG.md`.
