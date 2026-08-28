# DEV_VERIFICATION_REVIEW: Phase 4E — VPN Endpoint & Load Balancing

**Date**: 2026-08-28
**Reviewer**: dev_bot (Lead Developer, independent verification)
**Artifacts reviewed**: `TASK.md`, `DEV_HANDOVER.md`, plan `docs/plans/2026-08-25-go-rewrite.md` (Phase 4E, lines 528–566), specs 01/03/04/06, and all 30 source/test files under `amnezia-web-ui-go/internal/vpn/` plus `internal/database/vpn.go` (5886 lines total incl. tests; handover claimed "5755 lines across 29 files" — 15 impl + 15 test files exist, actual count 30; minor bookkeeping discrepancy only).
**Scope**: READ-ONLY verification. All quality gates re-run independently. No code modified.

---

## 1. Executive Summary

**Verdict: APPROVED_WITH_FINDINGS**

The architectural skeleton of Phase 4E is real, well-structured, and passes every mechanical quality gate I ran myself (build, gofmt, vet, golangci-lint, gosec, focused `go test -race -cover`, full-repo `go test -race`, 1130 Python tests). The load-balancer algorithms, IPAM, session manager, health prober, reconnect backoff, sticky failover, and traffic accountant are genuinely implemented, correctly synchronized, and meaningfully tested — not line-ticking.

However, two spec items are satisfied **in structure but not in wire-level behavior**, and one API method (`GenerateClientConfig`) contains hardcoded placeholder values that make its output non-functional against the implemented authenticator. These are acceptable as Phase-4E-internal scaffolding only if the wiring (UDP read loop → Noise handshake → session registration → forwarder drain loops → backend tunnel sockets) is explicitly deferred to Phase 5/6. The plan's Phase 4E verification gate item "listener accepts connections from real AWG client" is **not demonstrable** with this code: the UDP read loop counts bytes and discards them.

---

## 2. Spec Compliance Checklist (plan lines 532–558)

| # | Deliverable | Verdict | Evidence |
|---|---|---|---|
| 4E.1 | AWG endpoint listener, UDP bind (default 51820) | **PASS** | `listener.go:201-226` real `net.ListenUDP` bind; `listen_port` configurable; test uses free port. |
| 4E.1 | TUN device creation via amneziawg-go | **PARTIAL** | `PacketDevice` interface (`listener.go:17-23`) with `ChannelPacketDevice` mock + `SetPacketDevice()` injection point — mockable design, which is acceptable. **But no real TUN factory exists anywhere** (no import of `amneziawg-go` device/tun packages in `internal/vpn`). The interface is ready for real TUN integration; the integration itself is absent. |
| 4E.1 | TUN read/write loop consuming decrypted packets | **FAIL** | `tunDev` field is stored but **never read or written** by any goroutine. `udpReadLoop` (`listener.go:368-401`) counts bytes into `rxBytes` and drops the packet. No AWG handshake parsing, no decrypt, no dispatch to `AuthenticateAndRegisterPeer`. Peer registration is only reachable via direct `Service.HandleIncomingPeer()` calls (used by tests and future API handlers). |
| 4E.1 | Peer auth vs `user_connections` (awg), enabled, expiry | **PASS** | `auth.go:38-84`: token lookup, protocol normalize check, `Enabled`, both `ExpiresAt`/`ExpirationDate`, and traffic-quota rejection. Covered by `auth_test.go` incl. over-limit case. |
| 4E.1 | IP allocation from subnet (default 10.100.0.0/16), collision-free | **PASS** | `ipam.go`: sequential cursor over `[net+2, bcast-1]`, gateway/network/broadcast reserved, forward+reverse maps under `sync.RWMutex`, exhaustion error, `Reserve`/`Release`/`ReleaseIP`/`IsAllocated` complete. Correct. |
| 4E.1 | Session lifecycle: create/update/timeout/drain in `vpn_sessions` | **PASS (with note)** | `session.go` covers create/get/activity/touch/close/timeout/drain/sync-from-DB. **Note**: `CloseSession`, `CheckTimeouts`, `Drain` all persist status via `db.CreateVPNSession`, which only works because that method is an UPSERT (`ON CONFLICT(peer_public_key) DO UPDATE`, `vpn.go:356-368`). Works, but the method name lies; a dedicated `UpdateVPNSessionStatus` would be cleaner. |
| 4E.2 | Tunnel pool keyed by server_id, `awg-be-<id>` naming | **PASS** | `pool.go:125` `fmt.Sprintf("awg-be-%d", serverID)`; three lookup indexes (serverID, ID, ifname); getters return defensive copies. |
| 4E.2 | Curve25519 keypair gen, private key encrypted in `backend_tunnels` | **PASS** | `pool.go:42-56` real X25519 from `golang.org/x/crypto/curve25519`, base64-encoded. Encryption at rest via `security.EncryptCredential` (Fernet) in `CreateBackendTunnel`/`UpdateBackendTunnel`, transparent decrypt on scan (`database/vpn.go:467-469`). Round-trip verified by pool tests with real DB. |
| 4E.2 | Health prober reusing `internal/manager/awg/health`, Noise IK probe + RTT | **PASS** | `health.go:10,83` imports and defaults to `health.ProbeAWGEndpoint` with `DefaultH1/H2/S1/S2`; injectable `ProbeFunc` for tests. Status transitions: failure → `degraded`, ≥FailureThreshold → `disabled`; success → `active` or `degraded` if RTT > threshold. Concurrent `ProbeAll` with mutex-protected result map. |
| 4E.2 | Exponential backoff reconnect | **PASS** | `reconnect.go`: InitialBackoff/Multiplier/MaxBackoff/MaxRetries, `nextAttempt` gating, state reset on recovery. Backoff math verified in test (50→100→200 capped). |
| 4E.3 | least-conn (default), weighted-RR, round-robin algorithms | **PASS** | All three implemented, factory default = least-conn (`balancer.go:60`). WRR is true Nginx smooth WRR (currentWeight += effective; pick max; winner -= totalWeight) — test asserts exact 50/25/25 and 80/20 distributions over 100 picks. |
| 4E.3 | Exclude `degraded`/`disabled` from routing | **PASS** | `FilterHealthy` (`balancer.go:40-55`) keeps only `active` (case-insensitive) under per-backend cap; used by all three balancers and sticky failover. Confirmed by tests (`vpn_test.go:41-48` rejects degraded+disabled-only sets). |
| 4E.3 | Sticky user→backend affinity, reassign on degradation | **PASS** | `sticky.go:33-85` peer-key affinity first, then userID; sticky hit re-validated against current health+capacity; miss/degraded path re-selects and re-records. |
| 4E.3 | Failover: migrate orphaned sessions to healthy backends | **PASS** | `HandleFailover` migrates in-memory user+peer affinities **and** live DB `vpn_sessions` rows (via upsert). `Service.DisableBackend` triggers it. Test asserts migration and DB re-pointing. |
| 4E.3 | Capacity limits: `max_peers_per_backend`, `max_total_peers` | **PASS** | Enforced in `FilterHealthy` (per-backend) and each balancer's `SelectBackend` (global total, `ErrCapacityExceeded`). Tested. |
| 4E.4 | Bidirectional relay, per-session goroutine pair | **PARTIAL** | Both directions route (`RouteClientToBackend` → per-backend queue; `RouteBackendToClient` → per-client queue by dest IP) with per-packet copies and backpressure (`ErrQueueFull`). **But no goroutines exist in the forwarder** — nothing drains `backendQueues` toward tunnel UDP sockets, nothing drains `clientQueue` toward the endpoint UDP conn. The relay is a synchronous channel router; the "goroutine pair" must live in Phase 5/6 wiring that doesn't exist yet. |
| 4E.4 | Traffic accounting: bytes/session → `vpn_sessions`, `user_connections` | **PASS** | `accounting.go`: per-session and per-connection `atomic.Int64` counters, lock-striped map creation, periodic `Flush` using `Swap(0)` deltas → `UpdateVPNSessionTraffic` + `UpdateConnectionTraffic` + `UpdateUserTraffic` (via connection→user lookup). Test verifies DB rows after flush. Clean shutdown does final flush. |
| 4E.4 | Rate limiting integration (tc / per-user speed limits) | **FAIL** | **Nothing implements or integrates rate limiting.** No reference to `tc`, `awg_tc`, or throttling anywhere under `internal/vpn/`. TASK.md §3 also demanded a "rate limit throttling" forwarder test — absent. |
| 4E.5 | `backend_tunnels`, `vpn_sessions` tables + CRUD | **PASS** | `schema.sql:114-143` with indexes and FK cascades; full CRUD incl. `GetBackendTunnelByServerID`, `GetVPNSessionByPeerKey/ID/UserID`, `GetActiveVPNSessions`, status/traffic updates, deletes. SQL-injection-safe column allowlist for dynamic updates, `#nosec G201` justified. |
| 4E.5 | `vpn_config` table + CRUD | **PARTIAL (spec-consistent)** | Not a table: stored as JSON in `settings` under key `vpn_config` (`database/vpn.go:293-328`, default seeded in `database.go:33`). Both spec `01-domain-model.md` (VPNConfig model) and `03-database.md` (no vpn_config DDL, only settings) agree with this design; the plan's wording "3 new tables" is inaccurate relative to the specs. Since specs are ground truth per TASK.md §1, this is acceptable — flagging the plan-vs-spec wording drift only. |
| 4E.5 | Additive migration, **schema version bump** | **PARTIAL** | Tables are additive via `CREATE TABLE IF NOT EXISTS` applied on every open (`database.go:150-153`), so existing DBs self-upgrade. But `schema_version` in `DefaultSettings` is still `"1"` — no bump occurred. Low-impact bookkeeping gap. |
| Gate | Listener accepts real AWG client handshake | **NOT DEMONSTRATED** | No wire-protocol path exists; only mock-level tests. Verification gate item from plan line 561 unmet. |
| API | `GetStatus, GetBackends, EnableBackend, DisableBackend, GetTunnels, GetConfig, UpdateConfig, GetUserConnectionState, DisconnectUser` | **PASS** | All present on `Service` with sensible implementations, plus `DisconnectSession`, `HandleIncomingPeer`, `SelectTunnel`, `GenerateClientConfig`. |

---

## 3. Architecture Audit

**Interfaces & DI — good.** `Authenticator`, `PacketDevice`, `ProbeFunc`, `LoadBalancer` are all interfaces/func types with production defaults and test injectors. `NewLoadBalancer` factory covers the enum incl. empty→default. `Service` composes without leaking internals; aliasing (`BackendTunnel = models.BackendTunnel`) avoids type duplication.

**Concurrency — mostly solid.**
- Every shared map is behind an explicit `sync.RWMutex`; counters that are hot (`rxBytes`, `activeCount`, session traffic) use `atomic.Int64`.
- `Pool` getters return copies — prevents callers racing with `SetTunnelStatus` mutations.
- `TrafficAccountant.Flush` takes write lock only to `Swap(0)` the counters, then releases before DB I/O — correct pattern.
- Double-checked counter creation in `getOrCreateCounter` is correct.

**Issues found:**
1. **(Minor) `ReconnectManager` reads `rm.prober.cfg.LatencyThresholdMS` directly** (`reconnect.go:152`) instead of via `prober.Config()`. Prober cfg is immutable post-construction in practice, so no live race, but it breaks the prober's own encapsulation — and the race detector can flag it if `SetProbeFunc`-style setters ever extend to cfg.
2. **(Test-only data race) `reconnect_test.go:106` writes `reconnectMgr.cfg` without the mutex** while no background loop is running — currently benign but is exactly the pattern `-race` exists to catch if `Start()` is ever called earlier in the test.
3. **(Dead code) `var _ = models.BackendTunnel{}`** at `reconnect.go:201` — leftover import anchor; harmless but should be removed with the unused import.
4. **(Lifecycle)** `Listener.Stop` closes `tunDev` (the channel mock) but nothing ever re-opens it on restart; `Start` recreates `stopCh` but not the device. Restartability is untested. Similarly `Pool.Close()` sets `closed=true` but no method checks it — a pool flagged closed still accepts `AddTunnel`.
5. **(Error swallowing)** A recurring pattern: `_ = p.db.UpdateBackendTunnel(...)`, `_ = s.pool.SyncFromDB(ctx)` (vpn.go:228), `_ = s.endpoint.Start(ctx)` (vpn.go:251). `Service.Start` returns success even if the endpoint fails to bind the UDP port — the service reports running with a dead listener until someone calls `GetStatus`. Should at least log; ideally propagate the bind error.

---

## 4. Load Balancer Algorithm Audit

- **Least-conn**: correct min-scan with two deterministic tie-breakers (latency, then lowest ID). Verified by test expecting ID 4 of a mixed set (5/2/8 connections).
- **Weighted RR**: textbook Nginx smooth WRR. One subtlety: `currentWeights` is keyed by **tunnel ID** while `weights` is keyed by **server ID**, and entries for unhealthy servers keep accumulating in `currentWeights` between selections — wait, no: accumulation happens inside the loop over `healthy` only, and only healthy entries are incremented per selection. A previously-degraded tunnel rejoining starts from its stale `currentWeight` (never decremented while absent). **Impact: momentary unfairness on rejoin (the returning server can be under-served for up to ~1 selection cycle), self-correcting within one round.** Not a correctness bug; worth a comment.
- **Round-robin**: `atomic.Uint64` counter + modulo over the *currently healthy* slice — list membership changes between calls change indexing (inherent to RR-over-filtered-sets, acceptable).
- **Sticky failover**: re-validates sticky health before honoring it, migrates both affinities and DB sessions, selects replacements through the base balancer (so capacity and health filters apply). Correct. Test asserts `isNew` semantics and DB re-pointing.
- Capacity enforcement happens **at selection time only** from tunnel copies; `ActiveConnections` is incremented via `pool.IncrementConnections` after selection — a small TOCTOU window where concurrent `HandleIncomingPeer` calls could over-admit beyond `max_peers_per_backend`. `HandleIncomingPeer` holds `s.mu` for its full duration, however, so calls are serialized through the Service — mitigated at the service layer, though the balancer itself isn't atomic with the increment.

---

## 5. Forwarder & Accounting Audit

- Bidirectional routing exists as API, with proper packet copies before enqueue (no aliasing of caller buffers) and non-blocking queue sends with `ErrQueueFull` backpressure. Tests verify both directions, queue-full behavior on both paths, backend re-pointing via `UpdateSessionBackend`, and unregistration.
- **No drain goroutines, no socket integration**: `GetBackendPacketChannel`/`GetClientPacketChannel` are exposed for an external pump that does not exist in this phase. Net effect: the "Traffic Forwarder" can be driven correctly but currently isn't driven by anything outside tests. Combined with the silent `udpReadLoop`, no byte ever enters or leaves the forwarder in production today. **This must be wired in Phase 5/6 and should be tracked as a hard dependency**, ideally with a task note in TASK.md of the consuming phase.
- **Rx/Tx semantics are swapped relative to convention**: `RouteClientToBackend` counts as **Rx** (`totalRxBytes`, `RecordRx`) and `RouteBackendToClient` as **Tx**. If "Rx = received from client" that's fine; the DB columns and `UpdateVPNSessionTraffic(rx, tx)` inherit the same convention, so it's at least internally consistent. Flagging for the API layer to keep the convention straight.
- `UnregisterSession` deletes the route map entries but **never closes/drains `clientQueue`** — packets enqueued between last route lookup and unregistration are silently orphaned; a concurrent `RouteBackendToClient` that already fetched `route` under RLock will still enqueue. Bounded leak (≤ queue depth), benign, but a `close(clientQueue)` + drain would be cleaner.
- Accounting flush cadence (2s) and stop-flush are correct; per-connection → per-user rollup re-reads the connection row each flush (N+1 queries, tolerable at this scale).

---

## 6. Database Layer Audit

- **Tables**: `backend_tunnels` (11 cols, UNIQUE interface_name, FKs with CASCADE) and `vpn_sessions` (UUID PK, UNIQUE peer_public_key and assigned_ip, FKs) present in embedded `schema.sql`, matching spec `03-database.md` §9/§10, including the three required indexes.
- **CRUD completeness**: full for both tables (list/get/create/update/status-update/delete + by-serverID/by-peerKey/by-user lookups). `vpn_config` is settings-backed (see §2 finding) with full get/save + typed defaults. Spec-required method set from `03-database.md` §5 table (lines 314–322) is fully implemented with the exact signatures listed.
- **Encryption at rest**: tunnel private keys Fernet-encrypted on write, decrypted on read, with `LooksLikeFernetToken` idempotency guard — good; verified present.
- **Upsert semantics**: `CreateVPNSession` is INSERT ... ON CONFLICT(peer_public_key) DO UPDATE — this is what makes `CloseSession`'s status persistence work. It also means `vpn_sessions.id` (the UUID PK) **changes identity** on upsert-by-peer — wait, no: ON CONFLICT updates all listed columns excluding the PK only if the conflict target is `peer_public_key`; the `id` column IS being updated to `excluded.id` implicitly? No — `id` is in the INSERT column list but not in the DO UPDATE SET list, so on conflict the original row's UUID is preserved while all other fields update. Correct behavior for `CloseSession`/`CheckTimeouts` re-persisting the same session object. However in `SessionManager.CreateSession` peer-replacement, the old session row gets overwritten in place by the new UUID's INSERT conflicting on peer key — the DB row keeps the OLD UUID while the new in-memory session has a NEW UUID → **in-memory `sessionID` ≠ DB row `id` after a peer reconnect**. `UpdateVPNSessionTraffic(sessionID, ...)` then hits no row. Functional impact: minor (traffic for replaced sessions silently lost until next full create). Worth a follow-up task.
- **Schema version**: not bumped (still "1"); additive IF-NOT-EXISTS makes this harmless on boot but violates the plan's letter.

---

## 7. Security Review

- **Peer auth**: protocol assertion (`NormalizeProtocol(conn.Protocol) != "awg"` → reject) prevents cross-protocol token confusion; enabled/expiry/quota checks all fail-closed; empty key rejected. Good.
- **SQL injection**: dynamic-update column allowlist with explicit error; all other queries parameterized. `#nosec G201` justification is valid.
- **Key material**: Curve25519 private keys generated from `crypto/rand`, encrypted at rest; portal keypair generated per service instance (`vpn.go:162`) — ephemeral per restart. Since no real handshake consumes it yet, impact is nil today; when the wire path lands, the portal key must become persistent (from settings/vpn_config) or every restart invalidates all client configs.
- **`GenerateClientConfig` (main security-adjacent finding)**: generates a fresh client keypair per call, **never persists the public key to `user_connections`**, hardcodes `Endpoint = 127.0.0.1`, hardcodes obfuscation params (Jc=4/Jmin=50/Jmax=1000/S1=15/S2=18/H1-H4 static), and leases an IP for a key that will never authenticate (`DBAuthenticator` looks up by `client_id` token; the new pubkey isn't there). **The generated config is connectable against nothing.** Either register the pubkey as a connection row, or document the method as a Phase-5 template stub. Currently it's a footgun for whoever wires the 5.8 VPN router.
- **Race conditions**: full-repo `-race` is clean on a quiet system; listener/session/pool/forwarder maps and counters are all synchronized. IPAM double-map invariant is maintained under a single write lock. The only practical flake is the reconnect test's dependence on wall-clock sleeps (see §8).
- **DoS posture**: IPAM exhaustion is handled (error, not panic); forwarder queues bounded; UDP read loop bounded to 2KB datagrams with 500ms deadlines. No peer-rate limiting on auth attempts — acceptable at this layer.

---

## 8. Test Quality Review

Coverage claims re-verified by me this session: `vpn 93.0%, endpoint 88.9%, forwarder 98.0%, loadbalancer 97.9%, tunnel 92.2%` — all ≥ 85% gate. Tests are **meaningful**:

- Exact WRR distribution ratios (50/25/25, 80/20 over 100 picks) — real statistical assertion, not smoke.
- Sticky test asserts `isNew` flag transitions, sticky-hit-after-load-rebalance, failover re-pointing, and DB session migration with a real SQLite DB.
- Forwarder tests assert byte-exact delivery both directions, queue-full semantics, stats counters.
- Accounting test creates real server/user/connection/session rows, flushes, and reads back DB values.
- VPN service test wires a real DB + real listener (free UDP port) + mock probe end-to-end through `HandleIncomingPeer`.
- IPAM tests: collision-freedom, exhaustion, reserve/release, gateway reservation.
- Auth tests: happy path + disabled, expired (both date fields), wrong protocol, over-quota, unknown peer.

**Gaps vs TASK.md §3**: no rate-limit throttling test (feature absent); `vpn_test.go` integration uses mock client+backend at the **API level**, not a real AWG handshake at the socket level (feature absent — see §2).

**Flakiness**: in one of my two full-repo `go test -race ./...` runs (under concurrent pytest load), `TestReconnectManager` failed at step 5: expected retries=2/backoff=200ms, observed retries=1/backoff=100ms — consistent with a timing race between the test's `time.Sleep(150ms)` and the manager's `nextAttempt` cutoff under CPU starvation, plus a goroutine-safety smell where the test mutates `reconnectMgr.cfg` lock-free. The standalone and re-run full suites passed. **It is a real flaky test with a real failure signature in CI history (this session) and must be fixed** (e.g., drive time via injected clock or widen sleeps ×5 under `-race`), especially since CI runs the full suite exactly this way.

Quality gates I ran independently (all pass): `go build ./internal/vpn/...`, `gofmt -l` (0 diffs), `go vet ./...` (0), `golangci-lint run ./...` (0 issues), `gosec ./internal/vpn/...` (0 issues, 15 files/3361 lines), `go test -count=1 -race -cover ./internal/vpn/...` (pass, coverages as above), `go test -count=1 -race ./...` (all 22 packages pass on re-run), `pytest tests/test_*.py -q` (**1130 passed**, 0 failed, 114s).

---

## 9. Findings

**F1 (Major, deferred-wiring)**: Endpoint listener does not implement the AWG wire protocol — the UDP read loop counts and discards packets; the TUN device is never serviced. Peer onboarding exists only as the callable `HandleIncomingPeer`. Plan gate "accepts connections from real AWG client" is not met. **Action**: Phase 5/6 must own amneziawg-go conn/device integration; add a tracking line to that phase's TASK.md.

**F2 (Major, deferred-wiring)**: Forwarder lacks the per-session goroutine pair and any pump between endpoint UDP/TUN and backend tunnel sockets. Routing primitives are correct but idle in production paths today. Same action as F1 — co-design the pumps with F1.

**F3 (Moderate)**: `Service.GenerateClientConfig` produces non-functional configs (ephemeral unpersisted client key, `127.0.0.1` endpoint, hardcoded obfuscation params). Either persist the client pubkey into `user_connections` and use the configured listen host, or mark the method `TODO(phase-5.8)` and stop returning success-looking output.

**F4 (Moderate)**: Rate-limit integration (4E.4 bullet 3; TASK.md test bullet "rate limit throttling") is entirely absent. No tc hook, no token bucket, nothing. Needs to land with the F1/F2 wiring or be explicitly descoped by pm_bot.

**F5 (Moderate)**: `TestReconnectManager` is timing-flaky under full-suite load (observed one failure this session with a coherent failure signature). Fix with clock injection or relaxed timing before CI noise trains people to ignore red runs.

**F6 (Minor)**: `vpn_sessions` peer-reconnect path leaves DB row id ≠ in-memory session UUID (upsert on peer_public_key preserves old PK), silently breaking `UpdateVPNSessionTraffic` for replaced sessions.

**F7 (Minor)**: `Pool.Close()` sets `closed` but nothing enforces it; `Service.Start` swallows `endpoint.Start` errors (dead listener reported as running); `Listener` restart after `Stop` reuses a closed channel device.

**F8 (Minor)**: `reconnect.go:201` dead anchor `var _ = models.BackendTunnel{}`; direct access to `prober.cfg` instead of `prober.Config()`.

**F9 (Bookkeeping)**: `schema_version` not bumped; plan's "3 new tables" vs specs' 2 tables + settings (specs are ground truth — plan wording should be corrected); DEV_HANDOVER's "5755 lines / 29 files" vs actual 5886 / 30.

**Verified-correct highlights** (so they're on the record): smooth WRR math; FilterHealthy exclusion semantics; sticky failover incl. DB migration; Fernet at-rest encryption of tunnel keys; IPAM collision/exhaustion handling and RWMutex discipline; atomic accounting with swap-drain flush and final flush on stop; `database.DB` write-mutex serialization with WAL/busy-timeout pragmas; defensive-copy getters throughout the pool.

---

## 10. Final Verdict

**APPROVED_WITH_FINDINGS.**

The code that exists is genuinely good: correct algorithms, disciplined concurrency, real persistent state, real tests, and every mechanical quality gate I ran independently passed (including all 1130 Python regressions). Nothing is fabricated. But Phase 4E as merged is a **substrate, not a service**: no packet ever flows through it, because the two ends of the pipe (AWG wire protocol on the UDP socket; drain pumps into/out of the forwarder) and the rate limiter are not in this codebase. qa_bot's APPROVED verdict stands for code quality, and my findings do not contradict it — they extend it with the behavioral gaps that only a line-by-line read (not gate re-runs) surfaces.

**Required before Phase 4E can be called functionally complete** (recommend filing as explicit acceptance criteria in the consuming phase's TASK.md, not reopening #377):
1. amneziawg-go conn/TUN integration for the listener (F1) + forwarder pumps (F2) — one task.
2. Fix `GenerateClientConfig` to persist the client pubkey and use real endpoint/params (F3).
3. Rate limiting integration or explicit descope decision (F4).
4. Deflake `TestReconnectManager` (F5).

Recommendations F6–F9 are follow-up polish; file as low-priority issues.
