# Phase 5 API Handlers — Independent Code Review Report

**Reviewer:** dev_bot (independent read-only review)
**Date:** 2026-08-29 02:20 MSK
**Target Packages:** `amnezia-web-ui-go/internal/handlers/`, `amnezia-web-ui-go/internal/router/`
**Reference Spec:** `docs/specs/05-api-contract.md`
**Python Reference:** `app/routers/*.py`

---

## Verdict

**CONFIRMED_COMPLETE** — with reservations. All 10 REVIEW_TASK.md remediation items are genuinely fixed, all compilation gates pass, and all 75 spec catalog endpoints (the "64 API endpoints" + 11 HTML/redirect page routes) are mounted. However, the review surfaced **2 HIGH** findings (one a real command-injection-prone validation gap, one dev-handover mischaracterization of the govulncheck results), **4 MEDIUM** behavioral-parity gaps where the Go port returns stubbed/incomplete data versus the Python reference, and several LOW items. None of these are new regressions introduced by the rework, but `ToggleContainerHandler`'s missing protocol validation should be fixed before merge.

---

## Summary

The Phase 5 rework is real and verifiable. I independently ran every compilation gate: `go fmt` (clean, zero diff), `go vet` (clean), `go build` (clean), `go test -race -cover` (handlers **85.1%**, router **91.4%**, 0 data races — exactly matching the handover's claims), `golangci-lint` (0 issues), `gosec` (0 issues). All 10 remediation items check out in the actual code: no `coverage_*_test.go` files exist, no `amnezia-xray`/`xray` references remain in handlers or manager/dns, audit logging covers all 37 genuinely state-changing handlers, `RestoreBackupHandler` now restores all four entity classes (verified live via the audit-log emission `servers=1 users=1 conns=1 settings=1`), and `SyncNowHandler` is honestly documented as a `TODO(issue-380)` stub.

The Python reference test suite confirms no regressions: **1130 passed, 0 failed** for all non-e2e tests (`--ignore=tests/e2e`). The 82 failures + 28 errors in the full run are exclusively Playwright e2e tests failing with `net::ERR_CONNECTION_REFUSED` at `http://localhost:8000` — an environmental precondition (no live server / browser in this read-only review), not a code problem.

Where the review diverges from the handover's self-assessment: (1) the govulncheck results are **not** "stdlib-only with zero third-party module findings" as claimed — 6 of the "called" findings live in the required third-party module `golang.org/x/crypto@v0.26.0` (SSH), fixable to `v0.52.0`; and (2) a behavioral-parity audit against the Python reference found the Go port stubbing or skipping several behaviors the Python app implements (real reachability latency, per-user connection-limit overrides, "protocol not installed" guard, and an actual bulk speed-limit application).

---

## REVIEW_TASK.md Remediation Verification

| # | Issue | Status | Evidence |
|---|-------|--------|----------|
| 1 | MISSING DEV_HANDOVER.md | **PASS** | File exists at `tasks/issue-379-api-handlers/DEV_HANDOVER.md` (111 lines), well-formed, with gate output attached. |
| 2 | Coverage >= 85% | **PASS** | Independently ran: `go test -race -cover ./internal/handlers/... ./internal/router/...` → `internal/handlers coverage: 85.1% of statements`, `internal/router coverage: 91.4% of statements`, 0 races. Meets the >=85% gate. |
| 3 | Audit logging on state-changing handlers | **PASS** | 37 `h.audit(...)` calls found across auth/settings/servers/server_connections/connections/users/vpn. Enumerated all 37 genuinely state-changing handlers and confirmed 1:1 coverage. `audit()` wraps `middleware.LogAuditEvent` (handlers.go:243). Non-audited handlers are read-only POST probes (AddServer/ServerStats/ServerCheck/AutoTrial/GetServerConfig) — justified. |
| 4 | go fmt failures | **PASS** | `go fmt ./...` → zero output, zero diff, exit 0. |
| 5 | "amnezia-xray" leftover | **PASS** | `grep -r amnezia-xray` in entire repo → 0 matches. ClearServerHandler container list (servers.go:218-224) contains only `amnezia-awg`, `amnezia-awg2`, `amnezia-awg-legacy`, `telemt`, `amnezia-dns`. `internal/manager/dns` clean too. |
| 6 | RestoreBackupHandler incomplete | **PASS** | `settings.go:restoreBackupData` (254-360) restores settings + servers + users + connections with old→new server-ID mapping keyed on host+name (288-309). Live-verified with `go test -run TestSettingsHandlers/RestoreBackupHandler_Full_JSON -v` → audit log emitted `servers=1 users=1 conns=1 settings=1`. Matches the handover claim. |
| 7 | Error messages sanitized | **PASS** | All infrastructure-error paths return fixed generic strings ("SSH connection failed", "Failed to save server", "Failed to get config"). The surviving `err.Error()` calls in JSON responses are confined to `validation_failed`/`invalid_protocol` codes fed by request-struct `Validate()` methods (safe user-facing strings like "username must be 3-255 characters") and `GetProtocolManager` ("unsupported protocol: %s" — reflects the caller's own protocol input, no internal state; JSON-encoded so no XSS). No SSH dial, DB, or stack detail leaks. |
| 8 | SyncNowHandler placeholder | **PASS** | `settings.go:69-91` has explicit `TODO(issue-380)` referencing Phase 6 RemnaWave integration, returns honest `synced_users: 0` + message, reports pending RemnaWave-linked user count via audit. |
| 9 | Consolidate coverage test files | **PASS** | Filesystem search for `coverage_*_test.go` → 0 files. Test logic consolidated into `handlers_test.go`, `settings_test.go`, `servers_test.go`, etc. with shared mock infra (`setupTestHandlers`, `mockProtocolManager`, `testMockSSHClient/Pool`). |
| 10 | "xray" mock in tests | **PASS** | `handlers_test.go:57-59` registers only `awg`, `telemt`, `dns` `mockProtocolManager`s. `grep -ri xray` across all handler sources/tests → 0 matches (the sole intentional xray reference is the negative-validation test in `internal/models/models_test.go` asserting `IsValidProtocol("xray")==false`, outside handlers — correct per Issue #373). |

---

## Compilation Gate Results

All commands run independently from `/home/igor/Amnezia-Web-Panel/amnezia-web-ui-go` with `go version go1.26.2 linux/amd64`.

```
$ go fmt ./...
(zero output — clean, zero diff)                                EXIT=0

$ go vet ./...
(clean)                                                         EXIT=0

$ go build ./...
(clean)                                                         EXIT=0

$ go test -race -cover ./internal/handlers/... ./internal/router/...
ok  github.com/devops-igor/amnezia-web-ui-go/internal/handlers  coverage: 85.1% of statements
ok  github.com/devops-igor/amnezia-web-ui-go/internal/router    coverage: 91.4% of statements
(0 data races)                                                  EXIT=0

$ golangci-lint run ./internal/handlers/... ./internal/router/...
(no issues)                                                     EXIT=0

$ gosec -quiet ./internal/handlers/... ./internal/router/...
(0 issues)                                                      EXIT=0

$ govulncheck ./internal/handlers/... ./internal/router/...
Your code is affected by 16 vulnerabilities from 1 module and the Go standard library.
(7 stdlib: html/template, crypto/tls x2, net/http, encoding/asn1, net/textproto,
 crypto/x509, net — all "Fixed in go1.26.3-1.26.6", i.e. toolchain bump;
 9 in Module golang.org/x/crypto@v0.26.0 — fixed in v0.52.0/v0.35.0 — of which
 ~6 are actually CALLED via the SSH manager: GO-2026-5020/5019/5018/5017/5013+)
                                                                EXIT=3
```

**Python reference regression check** (from `/home/igor/Amnezia-Web-Panel`):
```
$ python -m pytest --tb=no -q --ignore=tests/e2e
1130 passed, 1 warning in 107.50s                            EXIT=0

(Full run including e2e: 82 failed + 28 errors — ALL Playwright e2e failing with
 net::ERR_CONNECTION_REFUSED at http://localhost:8000; environmental: no live
 server/browser in this read-only review. Not a code regression.)
```

---

## Code Review Findings

### CRITICAL
None.

### HIGH

1. **`ToggleContainerHandler` builds a Docker command from unvalidated user input (command-injection-prone).**
   `internal/handlers/servers.go:462-501`. The handler decodes an **anonymous inline struct** (`{Protocol, Action}`) — which has **no `Validate()` method** — then only `NormalizeProtocol`s the protocol (alias mapping only, NOT validation; `NormalizeProtocol` returns unknown protocols verbatim, models.go:28-33). The protocol is interpolated into `containerName := fmt.Sprintf("amnezia-%s", req.Protocol)` and then into `RunSudoCommand(ctx, fmt.Sprintf("docker start %s", containerName))`. A `support`-role caller (allowed by `RequireAdminOrSupport`) could pass a protocol like `awg; malicious-cmd` and get shell execution. The Python reference avoids this by whitelisting: `container = CONTAINER_NAMES.get(req.protocol)` returning 400 "Unknown protocol" (servers.py:519-521). **Fix:** validate with `IsValidProtocol(req.Protocol)` and 400-reject before use. Same class of issue at **`GetServerConfigHandler`** (servers.go:539-548): unvalidated `req.Protocol` interpolated into `cat /opt/amnezia/%s/config.json` — path-injection-prone; Python whitelists via `CONTAINER_NAMES`/config-path maps. (Auth-gated so bounded, but parity + defense-in-depth gap.)

2. **DEV_HANDOVER.md / WORKLOG.md mischaracterize the govulncheck results.**
   Both documents state the 16 govulncheck findings are "ALL stdlib-only... Zero findings in project or third-party module code" / "16 stdlib-only findings (zero project/module code findings)". This is **inaccurate**: 9 of the 16 are in **`golang.org/x/crypto@v0.26.0`**, a required third-party module listed in `go.mod` (the SSH library), fixed in `v0.52.0`. The example traces show real call paths from `internal/handlers/server_connections.go` and `internal/manager/ssh/client.go` into `ssh.channel.Read`/`mux.SendRequest`. Unlike the stdlib findings (which need a toolchain bump), these are remediable in-repo via `go get golang.org/x/crypto@v0.52.0`. The handover's claim understates the exposure and misattributes the fix.

### MEDIUM

3. **`ApplyDefaultSpeedLimitsHandler` is a stub that misreports success.**
   `internal/handlers/servers.go:743-768`. It only counts AWG clients (`count = len(clients)`) and returns `{"status": "ok", "updated": count}` — it **never applies any speed limit** (no per-client `SetClientLimit`/`tc` calls). The Python reference calls `manager.bulk_apply_default_speed_limits(...)` over SSH and returns the manager's actual result (servers.py:1363-1374). The Go endpoint returns a truthful-looking `updated` count for work it did not do.

4. **`GetServerReachabilityHandler` returns hardcoded `latency_ms: 25`.** `internal/handlers/servers.go:632-637`. `reachable` is derived from the DB-cached `GetServerStatus` (treating `unknown` as reachable — questionable), and latency is a constant. Python computes real latency via `compute_simplified_server_health` + the BackgroundTaskOrchestrator cache (servers.py:768-794). Response shape matches the contract, but the data is fabricated.

5. **`UserGetMyConnectionsHandler` hardcodes `server_reachable: true, server_status: "online"` and ignores per-user limits.**
   `internal/handlers/connections.go:56-57,61-65`. Python's `/api/my/connections` enriches each connection with the real cached reachability (`server_reachable`, `server_status`, else `unknown`/`false`) and reports **per-user** effective limits `user_limits.get("max_connections_per_user", global...)` (connections.py:34-65). Go reports the global limit only and fabricates online status for every connection — users with custom per-user limits see the wrong max, and offline servers are shown as online.

6. **`UserAddConnectionHandler` skips the "protocol not installed on this server" guard.**
   `internal/handlers/connections.go:146-165`. Python verifies `server["protocols"][proto].get("installed")` and 400s with "Protocol X is not installed on this server" before calling `add_client` (connections.py:154-161). Go goes straight to `protoMgr.AddClient`, relying on the remote call to fail generically. Also uses global rate limits only, not the per-user override Python applies (connections.py:99-108), and its sliding-window `retry_after` just returns the whole window rather than Python's oldest-entry computation (lower precision, acceptable).

### LOW

7. **Go backup omits `known_hosts` and `leaderboard_snapshots`.** Python's `load_data()` exports 7 keys (`servers`, `users`, `user_connections`, `connection_creation_log`, `known_hosts`, `leaderboard_snapshots`, `settings`); Go's `DownloadBackupHandler` exports only 5 (no SSH host-key fingerprints, no leaderboard snapshots). `known_hosts` is security-relevant (TOFU fingerprints). Go also omits the `credentials_excluded: true` marker Python sets. JSON field names otherwise match (`servers`/`users`/`user_connections`), so Go can consume a Python backup's connection/server/user data.

8. **Double `WriteHeader` on the 428 max-connections path.** `internal/handlers/connections.go:123-125` calls `w.WriteHeader(http.StatusPreconditionRequired)` and then `h.JSON(...)` which calls `WriteHeader` again (superfluous-call warning, second is a no-op). The rate-limit 428 path (line 138) correctly delegates to `h.JSON` alone. Harmless but sloppy.

9. **Backup round-trip loses server SSH credentials and user password hashes — by design (matches Python), but worth documenting.** `DownloadBackupHandler` never writes `ssh_pass`/`ssh_key`/`password_hash` into the export, yet `restoreBackupData` reads them (they'll be empty). This matches Python's intentional credential-exclusion posture (settings.py:82-89) — restored servers need SSH credentials re-entered and this is not separately documented in the Go response.

10. **`GetProtocolManager` reflects the caller-supplied protocol string in its error** (`unsupported protocol: %s`, handlers.go:239) which then appears verbatim in `invalid_protocol` JSON responses. Not a leak (it's the caller's own input, JSON-encoded) and Python echoes similarly, but echoing raw user input into responses is a minor reflection smell.

### Spec documentation discrepancies (NOT implementation bugs)
- **§1.1 says admin endpoints require `role == "admin"`, but both the spec intent and Python `require_admin` actually allow `"support"` too** (dependencies.py:42). Go's `RequireAdminOrSupport` correctly matches the Python behavior; the spec text is the thing that's wrong.
- The prompt's Python filenames (`api_servers.py`, `api_connections.py`, `api_users.py`, `api_settings.py`) don't exist — actual files are `app/routers/servers.py`, `connections.py`, `users.py`, `settings.py`.

---

## Endpoint Coverage Audit

The "64-endpoint API catalog" = 75 spec table rows − 11 HTML/redirect page routes. I enumerated every spec row and verified it is mounted in `internal/router/router.go`:

| Module (spec §) | Spec rows | Mounted | Match? |
|-----------------|-----------|---------|--------|
| 2.1 Auth        | 7  | 7 (login/logout/set_lang/captcha/login/setup/change-password) | ✓ |
| 2.2 Servers     | 28 | 28, mounted under BOTH `/api/servers/...` AND legacy root aliases (`/add`, `/{server_id}/...`) | ✓ |
| 2.3 Connections | 5  | 5 under `/api/connections` | ✓ |
| 2.4 Users       | 6  | 6 under `/api/users` | ✓ |
| 2.5 Settings    | 7  | 7 (`/api/settings` resolves via chi `r.Route("/api/settings")` + `r.Get("/")`) | ✓ |
| 2.6 Share       | 5  | 5 | ✓ |
| 2.7 Pages+Leaderboard | 7 | 7 | ✓ |
| 2.8 VPN         | 10 | 10 (8 admin under `/api/vpn`, my-connection/my-config under session group) | ✓ |
| **TOTAL**       | **75** | **75/75 mounted** | **✓** |

**Extra (beyond spec, additive not divergent):** `GET /api/health`, `GET /api/version`, `GET /api/my/connections`, `GET /api/connections/` (legacy alias of my-connections), `GET /api/users/` (ListUsersHandler). Note the Go router does **not** expose a JSON server-list `GET /api/servers/` (Python has `api_list_servers`, servers.py:49); the spec intentionally omits it — frontend gets servers via the index HTML render. LOW/informational.

**CSRF exemptions** (spec §1.2): verified `middleware/csrf.go:IsCSRFExempt` exempts exactly `POST /api/auth/login`, `POST /api/auth/setup`, `POST /api/share/{token}/auth` — matches spec, and confirmed by `csrf_test.go`.
**Middleware ordering** (router.go:85-160): RequestID → RealIP → Logger → Recoverer → Timeout → Session → SetupRedirect → PasswordChangeRequired → **CSRF(100) → RequireAuth/RequireAdminOrSupport(per-group) → RateLimit(per-route)**. CSRF-before-auth and auth-before-rate-limit are both correct.
**Standard error shape** (§1.3): all errors go through `middleware.WriteJSONError(err, detail)` — verified consistent `{"error": <code>, "detail": <msg>}` across handlers.

---

## Python Parity Check

Compared `app/routers/{servers,connections,users,settings}.py` against the Go counterparts:

| Area | Parity | Notes |
|------|--------|-------|
| AddServer two-phase fingerprint flow | ✓ Exact | `status:"pending_fingerprint_confirmation"` + fingerprint + server_info, no persistence until confirm — identical to Python servers.py:90-139. |
| Container toggle | ✗ **HIGH #1** | Python whitelists container names; Go interpolates unvalidated protocol into `docker` cmd. |
| Apply default speed limits | ✗ **MEDIUM #3** | Python applies via `bulk_apply_default_speed_limits`; Go only counts. |
| Server reachability | ✗ **MEDIUM #4** | Python computes real latency from cached health; Go hardcodes 25ms + `unknown`→reachable. |
| My-connections enrichment | ✗ **MEDIUM #5** | Python: real reachability + per-user limits; Go: fabricated online + global limits only. |
| Add-connection guards | ✗ **MEDIUM #6** | Python validates protocol-installed + per-user limits; Go skips both. |
| Settings get SSL masking | ✓ Exact | Both strip `key_text`/`cert_text`. |
| Backup credential exclusion | ✓ (by design) | Both exclude ssh_pass/ssh_key/password_hash. Go omits known_hosts + leaderboard_snapshots (LOW #7). |
| User add role validation | ✓ Exact | Both validate role ∈ {admin,support,user} (Go models.go:524, Python users.py:108). Support-role caller can create admins in BOTH — parity-matched design, not a Go bug. |
| Duplicate connection name | ~ | Go case-insensitive (`EqualFold`), Python case-sensitive — Go stricter, acceptable. |

---

## Test Quality Assessment

**Strong overall.** 85.1% handler / 91.4% router coverage is genuine behavioral coverage, not line-hitting:
- **Isolation:** each test opens a fresh temp-dir SQLite DB via `setupTestHandlers` + `t.TempDir()`/`t.Cleanup`; no shared state. Router tests seed a real admin user for the setup-redirect middleware. Race detector passes across the whole suite (0 races) — meaningfully exercised through concurrent handler/SSH-pool paths.
- **Mock infra:** clean and reusable — injectable `mockProtocolManager` (per-method funcs), `testMockSSHClient`/`testMockSSHPool` (injectable cmd/upload/download funcs). Only valid protocols registered (awg/telemt/dns).
- **Both paths covered:** success + validation failures + ownership/permission boundaries + not-found + bad-JSON + edge branches. Settings tests verify actual persistence (`telegram` bot_token read back) and secret masking, not just 200s.
- **Table-driven where appropriate** (leaderboard periods; CSRF exemption list).
- **Restore backup:** `RestoreBackupHandler Full JSON` only asserts HTTP 200 — it does NOT programmatically assert the `restored{servers,users,conns,settings}` counts (the counts ARE correct — I verified via the live audit-log emission — but the test itself wouldn't catch a regression in the counter logic). Recommend asserting the response body's `restored` map. (LOW)
- **No flaky patterns:** no wall-clock sleeps, no ordering dependencies, no network access (SSH fully mocked).

---

## Recommendations (ordered)

1. **[HIGH] Validate protocol input in `ToggleContainerHandler`** (servers.go:462-501): add `if !models.IsValidProtocol(req.Protocol) { 400 }` before Sprintf — or better, whitelist container names via a map like Python's `CONTAINER_NAMES`. Apply the same to `GetServerConfigHandler`/`SaveServerConfigHandler` config-path selection.
2. **[HIGH] Correct the govulncheck narrative** in DEV_HANDOVER.md and WORKLOG.md: 9 of 16 findings are in third-party `golang.org/x/crypto@v0.26.0`, not stdlib-only. Remediate with `go get golang.org/x/crypto@v0.52.0 && go mod tidy`, then re-run govulncheck (still bump the Go toolchain to 1.26.6+ for the 7 genuine stdlib findings).
3. **[MEDIUM] Implement `ApplyDefaultSpeedLimitsHandler`** for real (per-client default tc limits) or return `not implemented` until then; don't report fake `updated` counts.
4. **[MEDIUM] Wire `GetServerReachabilityHandler` and `UserGetMyConnectionsHandler` to the real reachability cache** (or an equivalent `BackgroundTaskOrchestrator` analog) instead of hardcoded `25ms`/`online`/`true`, and consume per-user limit overrides in both my-connections and add-connection.
5. **[MEDIUM] Add the "protocol not installed" guard** to `UserAddConnectionHandler` (and check it in server_connections add for parity).
6. **[LOW] Include `known_hosts` (and `leaderboard_snapshots`) in `DownloadBackupHandler`** and restore; add the `credentials_excluded` marker for cross-compat with Python backups.
7. **[LOW] Assert the `restored` counts in the RestoreBackup test**; remove the duplicate `WriteHeader` in connections.go:123.
8. **[Spec] Fix §1.1 to say `role in (admin, support)`** and note the actual Python router filenames in the review/task templates.

---

*Review performed read-only. No source files modified. No git commit/push performed.*
