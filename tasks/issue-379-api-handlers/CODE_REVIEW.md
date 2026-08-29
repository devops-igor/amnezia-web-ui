# Senior Code Review — Issue #379 Phase 5: API Handlers & Business Logic

**Reviewer:** qa_bot (adversarial review)
**Date:** 2026-08-29
**Scope:** `amnezia-web-ui-go/internal/handlers/` (all 12 handler files, ~7k LOC), `internal/router/router.go`, `internal/middleware/` (session, CSRF, ratelimit), `internal/security/security.go`, `internal/database/` (schema, users, settings), and call chains into `internal/manager/awg/`.

---

## Summary

* Overall risk: `HIGH`
* Findings: `16`
* Critical: `0`
* High: `4`
* Medium: `7`
* Low: `5`

The middleware stack (HMAC-signed cookies, double-submit CSRF with constant-time compare, validated protocol→container-name mapping) is sound — the findings below are what got through it.

---

## Findings

### [HIGH] Sensitive secrets leaked in cleartext by GET /api/settings

**Location:** `internal/handlers/settings.go:16-45` (GetSettingsHandler), `internal/models/models.go:313-318`

**Problem:** The handler strips `sslCfg.KeyText`/`CertText` but returns `syncCfg` (containing `RemnawaveAPIKey`) and the raw `telegram` settings map (bot tokens) unmasked. The route is mounted under `RequireAdminOrSupport` — the **Support** role receives production RemnaWave API keys and Telegram bot tokens.

**Failure scenario:** A support-role account (intentionally limited, per the role split in `middleware/session.go:112`) fetches `/api/settings` and obtains the RemnaWave admin API key, gaining full ability to manage the upstream VPN panel.

**Impact:** Privilege escalation of the Support role to full RemnaWave control; leaked bot tokens allow impersonating the panel's Telegram bot.

**Recommended fix:** Mask `RemnawaveAPIKey` (e.g. return `"*******"` sentinel or omit when non-empty, mirroring the SSL key handling) and redact token fields in the telegram map. Follow the same mask-sentinel convention so `POST /settings/save` can distinguish "unchanged" from "cleared".

---

### [HIGH] Username uniqueness is check-then-act with no DB constraint — duplicate accounts possible

**Location:** `internal/handlers/users.go:143-147` (AddUserHandler), `internal/handlers/auth.go:227-254` (APISetupHandler), `internal/database/schema.sql:21-49`

**Problem:** `AddUserHandler` does `GetUserByUsername` then `CreateUser` with no lock and no `UNIQUE` constraint on `users.username` (schema has only `id TEXT PRIMARY KEY`). Two concurrent requests both pass the existence check and both insert. Same pattern in `APISetupHandler`: `CountUsers()` then `CreateUser` — two racing unauthenticated setup requests during first-run create **two admin accounts**.

**Failure scenario:** Attacker watching a fresh panel fires a burst of `POST /api/auth/setup` requests in parallel with the operator's legitimate setup. One of the attacker's requests lands in the `userCount == 0` window; the attacker's admin account persists silently alongside the real one.

**Impact:** Potential full panel takeover on first-run race; duplicate usernames make `GetUserByUsername` nondeterministic for all subsequent auth (SQLite returns whichever row matches first).

**Recommended fix:** Add `CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username);` (with migration for existing DBs) and treat a constraint violation in `CreateUser` as `user_exists` / `setup_already_done`. Keep the pre-check for friendly errors, but rely on the constraint as the source of truth. Regression test: parallel goroutines hitting setup with distinct payloads.

---

### [HIGH] Stateless sessions cannot be revoked — disabled or deleted users keep full access for up to 7 days

**Location:** `internal/middleware/session.go:26-40` (Session), `internal/handlers/users.go:390-429` (ToggleUserHandler/DeleteUserHandler)

**Problem:** Sessions are purely cookie-based HMAC-signed blobs (`DefaultSessionMaxAge = 7 days`). `ToggleUserHandler` sets `enabled=false` in the DB, and `DeleteUserHandler` removes the user — but neither touches an existing session cookie, and no middleware re-checks the DB (`RequireAuth`/`RequireAdminOrSupport` only trust the cookie). Only `UserAddConnectionHandler` re-checks `user.Enabled`.

**Failure scenario:** An admin account is compromised and then disabled by another operator. The attacker's cookie remains cryptographically valid; they continue using all `/api/servers/*`, `/api/users/*`, and `/api/settings/*` endpoints — including `POST /api/settings/backup/restore` — for up to 7 days or until panel restart (which doesn't even help, since there's no server-side state).

**Impact:** "Disable user" and "delete user" are not effective containment actions for the panel's own user base. This is the classic stateless-session revocation gap.

**Recommended fix:** At minimum, `RequireAdminOrSupport` should periodically (e.g. cached 60s) re-load the user from DB and verify existence + `Enabled` + role. Better: embed an issue-time in the session and store a per-user `sessions_invalidated_before` timestamp checked in the middleware. Include a version/tick in `ToggleUserHandler`'s audit.

---

### [HIGH] SaveSettingsHandler silently wipes SSL certificate/key via mask round-trip

**Location:** `internal/handlers/settings.go:34-35, 56-63`

**Problem:** `GetSettingsHandler` deliberately returns empty `sslCfg.KeyText`/`CertText` (masking). `SaveSettingsHandler` then does `h.db.SetSetting(ctx, "ssl", req.SSL)` **unconditionally** — including the masked (empty) values. Unless the frontend resends the full certificate text on every settings save (not verifiable from this repo), any save of the appearance/captcha/sync form destroys the stored SSL key/cert.

**Failure scenario:** Admin opens `/settings`, toggles the CAPTCHA checkbox, saves. Next HTTPS termination (or the next backup) finds an empty `ssl.key_text` — the panel's TLS material is gone. The API returns `{"status":"ok"}` throughout because every `SetSetting` error is also discarded (`_ =`).

**Impact:** Silent credential destruction on a routine UI action; recovery requires re-uploading certs.

**Recommended fix:** Either (a) skip `SetSetting("ssl", ...)` when `req.SSL.KeyText == "" && req.SSL.CertText == ""`, or (b) use sentinel masking (`"__unchanged__"`) and skip unchanged sentinels per-field. Also propagate `SetSetting` errors instead of `_ =` — see next finding.

---

### [MEDIUM] Pervasive ignored errors turn remote failures into 200 OK

**Location:** `servers.go:188` (RebootServerHandler), `servers.go:227-235` (ClearServerHandler incl. `rm -rf /opt/amnezia` result ignored), `servers.go:690-694` (SetClientSpeedLimitHandler — `h.awgMgr.EditClient` error discarded), `servers.go:833-838` (SetAWGSpeedLimitConfigHandler — `UpdateServerProtocols` and `tc.SetGlobalLimit` errors discarded), `server_connections.go:402-403` (RemoveServerConnectionHandler), `settings.go:56-63` (SaveSettingsHandler), `users.go:382-383` (DeleteUserHandler)

**Problem:** Every mutating handler discards the error of the operation it claims to perform and responds `{"status":"ok"}`.

**Failure scenario:** `POST /api/servers/1/reboot` with a dead SSH path: `RunSudoCommand` error is swallowed, handler audits `server.reboot` as performed and returns ok. Operator sees "rebooted"; nothing happened. Worse for `ClearServerHandler`: `rm -rf /opt/amnezia` failing silently leaves the DB protocol state wiped (`UpdateServerProtocols` result also ignored on the success path it does run) while the server still runs containers — panel and reality diverge.

**Impact:** False-success responses defeat the operator's mental model and the audit log; partial-failure states are invisible.

**Recommended fix:** Return 5xx with `operation_failed` when the primary remote action errors. Where "best effort" is intentional (e.g. speed-limit apply after DB save), at least include a `"warnings"` array in the response and log at WARN.

---

### [MEDIUM] Connection limit and rate-limit checks are TOCTOU

**Location:** `internal/handlers/connections.go:155-158, 513-541` (checkConnectionLimits)

**Problem:** `UserAddConnectionHandler` reads `GetConnectionsByUserID`, checks against `effectiveMax`, then performs a slow SSH `AddClient` and `CreateConnection`. No lock or transaction spans the sequence.

**Failure scenario:** User at 9/10 connections fires 10 parallel `POST /api/connections/add`. All requests read `len(userConns) == 9` before any insert lands; all pass; user ends with 19 connections (and 9 SSH clients provisioned on the server that the limit was supposed to cap).

**Impact:** Limits enforceable only under serial traffic; SSH-side client sprawl is the expensive part and is unbounded under concurrency.

**Recommended fix:** Wrap check+create in a per-user mutex (in-process is sufficient for this single-node SQLite panel), or use `INSERT ... WHERE (SELECT COUNT(*) ...) < limit` in a transaction. Test with parallel goroutines asserting the cap is never exceeded.

---

### [MEDIUM] Backup restore is non-transactional and blindly overwrites settings

**Location:** `internal/handlers/settings.go:242-417` (RestoreBackupHandler / restoreBackupData / restoreBackupSettings)

**Problem:** (1) Restore iterates entity-by-entity, each with independently ignored errors — a mid-way failure (e.g. constraint, malformed record) leaves servers restored but connections/settings not, reported as `{"status":"ok"}` with partial counts. (2) `restoreBackupSettings` writes **every** key from the uploaded file into the settings store with no allowlist — a crafted backup can replace `sync` (pointing RemnaWave sync at an attacker URL with attacker API key), `ssl`, `captcha` (disable CAPTCHA), and `limits` wholesale.

**Failure scenario:** A backup file circulated to an operator ("restore this, it's from our old panel") rewrites the sync API key/URL; when Phase 6 lands, the panel will push user data to the attacker's endpoint.

**Impact:** Restore is a settings-injection primitive; partial-failure inconsistency corrupts panel state.

**Recommended fix:** Restore settings and entities inside a DB transaction with rollback on any error; allowlist restorable settings keys; validate server/user records before any write; return per-section errors.

---

### [MEDIUM] Empty SecretKey breaks CAPTCHA and login entirely, with 200 responses

**Location:** `internal/handlers/auth.go:75-104` (CaptchaHandler), `auth.go:186-191` (APILoginHandler)

**Problem:** When `cfg.SecretKey == ""`, `CaptchaHandler` generates a challenge whose answer is **never persisted** (the `if` guarding `SetSessionCookie` skips), yet still returns the image; with captcha enabled, every login fails `invalid_captcha` forever. Similarly `APILoginHandler` validates credentials and returns `200 {"status":"ok"}` but sets no session cookie — the client appears logged in per the response, then gets 401 on the next request.

**Failure scenario:** Misconfigured deployment (missing secret key): login page spins — "ok" from login, "unauthorized" from everything else — with no error anywhere.

**Impact:** Complete auth outage with misleading success responses; hard to diagnose.

**Recommended fix:** Fail fast: 500 `internal_error` with a clear detail ("session signing key not configured") from both handlers when `SecretKey == ""`. Better: reject startup when SecretKey is empty.

---

### [MEDIUM] ListUsersHandler materializes the entire user and connection tables per request

**Location:** `internal/handlers/users.go:20-127`

**Problem:** Every call to `GET /api/users` runs `GetAllUsers` **and** `GetAllConnections`, builds a count map, filters in memory, then paginates the slice. With 10k users × 10 connections, each request allocates and scans ~110k records.

**Failure scenario:** Support staff with the users page open on auto-refresh (plus search-as-you-type) under moderate panel growth → CPU and GC pressure on the single SQLite node; latency grows linearly with the user base.

**Impact:** Scales poorly; will be the first thing to fall over under load.

**Recommended fix:** Push search + pagination + per-user connection counts into SQL (`LIKE` filter, `LIMIT/OFFSET`, `COUNT(*) ... GROUP BY user_id`).

---

### [LOW] GET /logout is CSRF-able and logs out cross-site

**Location:** `internal/handlers/auth.go:39-43`, `router.go:124`

**Problem:** Logout is a GET with no CSRF check (GET is "safe" so the CSRF middleware lets it through), so `<img src="https://panel/logout">` on any page logs the victim out. Denial of service only, but it is a spec'd mutating action reachable via a safe method.

**Recommended fix:** Accept this (many panels do) or move logout to POST with CSRF validation and keep a GET redirect for legacy links.

---

### [LOW] SetLangHandler writes an unvalidated cookie value of unbounded size

**Location:** `internal/handlers/auth.go:46-72`

**Problem:** `lang := chi.URLParam(r, "lang")` is never checked against a language allowlist or length bound; the raw value goes into two persistent (1-year) cookies. `http.SetCookie` prevents header injection but not bloat/odd values.

**Failure scenario:** `/set_lang/` + 3000-char string → 6KB of cookies attached to every subsequent request; also poisons `Translate` lookups.

**Recommended fix:** Validate against the supported-language set (defaulting to `en`), reject otherwise.

---

### [LOW] ToggleContainerHandler reports fabricated state and corrupts the audit record

**Location:** `internal/handlers/servers.go:505-519`

**Problem:** For `action == "restart"` the handler overwrites `req.Action = "running"` and returns `"state": "running"` without ever checking that docker succeeded (error ignored). The audit entry then records `action: running` — the requested action is lost from the audit trail.

**Failure scenario:** Restart fails (container missing, SSH dropped); API says `running`; audit says the operator "ran" something they didn't.

**Recommended fix:** Report the requested action, not an invented post-state; verify with `docker inspect -f '{{.State.Status}}'` after the command, or at least drop the fabricated "running".

---

### [LOW] DeleteServerHandler deletes children before parent, leaving orphaned state on failure

**Location:** `internal/handlers/servers.go:152-161`

**Problem:** Connections and known-hosts are deleted, then `DeleteServer` errors → user connections are gone while the server remains, and the API returns 500 with the half-deleted state. No transaction.

**Recommended fix:** Order deletes parent-first-check → single transaction, or compensate on failure.

---

### [LOW] AutoTrialHandler returns hardcoded fake probe results

**Location:** `internal/handlers/server_connections.go:256-279`

**Problem:** The endpoint returns fabricated `reachable` / fixed latencies (22/28/19/35 ms) for quic/tls/dns/sip regardless of any real probing. This isn't marked as a stub in the response.

**Failure scenario:** Operator relies on auto-trial output to pick a mimicry profile under DPI blocking; the "results" are constants.

**Impact:** Misleading API consumer; decision support that never measures.

**Recommended fix:** Either implement the probe or return `{"status": "not_implemented"}` with an honest error code — do not return plausible-looking fake data.

---

### [INFO] Backup round-trip drops user credentials by construction

**Location:** `settings.go:139-163` (DownloadBackupHandler omits `password_hash`), `settings.go:420-438` (restore reads `password_hash` from the map)

Restoring a panel's own backup recreates all users with empty password hashes — nobody can log in (empty bcrypt hash never matches). If the Python reference behaves the same way this is parity, but the restore path *reads* `password_hash`, suggesting intent to support it. Worth confirming against the Python implementation (see Questions).

### [INFO] CAPTCHA image is trivially machine-readable

**Location:** `auth.go:350-389`

The generated image is a fixed-grid dot pattern with deterministic per-glyph strokes; any OCR (or even template matching, since rendering is deterministic given the digit) defeats it. For a panel exposed to the internet with login rate limiting of 5/min/IP, the CAPTCHA adds near-zero protection. Consider a real library or dropping the visual challenge in favor of the rate limiter.

### [INFO] Router: `NewRouter` vs `NewRouterWithOptions` create two SSH pools / VPN services when both used

**Location:** `router.go:36-68` — `NewRouter` constructs managers and then delegates to `NewRouterWithOptions`, which constructs *another* `Handlers` if `opts.Handlers == nil`. In `NewRouter` the handler is passed so it's fine, but the nil-branch silently produces a handler with no SSH pool / managers — any admin server endpoint then returns `connection_failed` rather than failing loudly. Minor foot-gun for embedders.

---

## Tests

The suite (5,315 lines across handler tests) covers happy paths well, but the ignored-error pattern (`_ = h.db...`) means **failure paths are largely untestable and untested**: a test cannot distinguish "delete succeeded" from "delete failed silently" because both produce `{"status":"ok"}`. Specific gaps:

1. **No concurrency tests** — the setup race, duplicate-username race, and connection-limit TOCTOU all have zero regression coverage. All three are testable with goroutines + a real temp SQLite DB.
2. **No masking round-trip test** — GET settings → POST settings does not assert SSL key survives; this would have caught finding #4 immediately.
3. **No revocation test** — disable user then call an admin endpoint with the old cookie; currently passes, which is the bug.
4. **`RestoreBackupHandler` tests** exercise well-formed backups only; no malformed/partial-failure/evil-settings-key tests.
5. `ToggleUserHandler` test does not verify remote `awgMgr.ToggleClient` invocation counts (errors there are ignored, so a stub that always fails would pass the test).

Proposed regression tests:
- `TestSetupRace_TwoAdmins` — parallel setup POSTs, assert exactly one admin row
- `TestSettingsSave_PreservesSSLCert`
- `TestDisabledUser_SessionRevoked`
- `TestConnectionLimit_ConcurrentAdds` asserting `len(conns) <= limit`

---

## Questions / Uncertainties

1. **SSL round-trip**: does the frontend re-send full certificate text on every `/api/settings/save`? If yes, finding #4 is mitigated in practice (the backend hazard remains). Could not be determined from the Go repo.
2. **Python reference behavior** for backup credential exclusion and `sync_delete` — is `password_hash`-less restore intended parity or an oversight?
3. **SecretKey** — is there startup validation guaranteeing a non-empty key in production? If yes, the empty-key finding is dev-only.
4. Whether a session revocation mechanism is planned in a later phase; the stateless-cookie design makes finding #3 an architectural decision, not an accident.

---

## Final Assessment

**REQUEST CHANGES**

The routing, auth middleware, CSRF, and input validation layers are solid, and the handler coverage breadth is genuinely good — but this change set has four blocking defects: secret leakage to the Support role, the absence of any username uniqueness constraint combined with check-then-act creation (a first-run takeover race), no session revocation making user disable/delete ineffective containment, and the settings-save SSL wipe. All four have concrete failure scenarios and none are theoretical style nits. Additionally, the systemic ignored-error pattern means the API's `{"status":"ok"}` cannot be trusted for remote operations, which will generate real operational incidents. Fix the HIGH findings and add the four proposed regression tests before this reaches QA handover; the MEDIUM items (transactions for restore/delete, TOCTOU limits, pagination push-down) should be tracked but need not block.
