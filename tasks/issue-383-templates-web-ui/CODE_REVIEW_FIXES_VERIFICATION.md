# Verification Review: Claimed Fixes for Issue #383 Template Fixes Rework

- **Reviewer**: dev_bot (independent verification of remediation claims)
- **Date**: 2026-09-01
- **Scope**: Verification of all fix claims in `template_fixes_rework_dev_handover.md` and `template_fixes_rework_qa.md` against the actual working-tree code, with independent empirical probes
- **Source review**: `CODE_REVIEW.md` (original 11-finding adversarial review)

---

## Summary

* Overall claim: **MOSTLY TRUE — but the rework introduced 2 confirmed regressions, one breaking the entire `/server/{id}` page**
* Findings fixed as claimed: **9 / 11**
* Fixes verified with code inspection + empirical tests: **8**
* Fixes with caveats: **1** (Finding 2 — real fix, weak test)
* Findings NOT fixed (out of scope per rework spec, by design): **1** (Finding 11)
* **Regressions introduced by the rework: 3** (1 HIGH functional break, 1 MEDIUM no-op, 1 LOW parsing weakening)

**Method**: Re-inspected every changed file; re-executed compilation gates (`go build`, `go vet`, `gofmt` — all clean); re-ran the new test suites under `-race` (all pass); wrote independent Go test probes compiled against the real handlers + real embedded templates to exercise the two suspicious behaviors found; re-tokenized the new client-side escaping logic with an HTML parser; re-executed the new `CleanReferer` against all bypass variants.

---

## Per-Finding Verification

### Finding 1 [CRITICAL] SSH credential leak — **FIXED & VERIFIED**

Code confirms:
- `models.Server.SSHPass/SSHKey` and `User.PasswordHash/SharePasswordHash` now carry `json:"-"` (`internal/models/models.go:164-165, 179, 193`)
- `MyConnectionsPageHandler` builds `SanitizedServerForUser` (ID, Name, Protocols→installed-only, Status, Reachable) and never passes raw servers to the template (`internal/handlers/pages.go:74-136`)
- `TestRenderMyConnectionsNoCredentialLeak` renders the real page with a credentialed server and asserts no leak — re-run independently: **PASS** against the real handler + real embedded template

**Caveat (non-blocking)**: the test's "private key" fixture is the literal string `[REDACTED PRIVATE KEY]`, while the assertion checks for `"BEGIN RSA PRIVATE KEY"` — a string **never present in the input**. The key-half of the test asserts a positive that cannot fail for the right reason. The `json:"-"` defense makes this moot in practice, but the rework spec explicitly required "realistic RSA/ED25519 private keys" and that requirement was not met (fixture redaction was likely forced by a secret scanner).

### Finding 2 [HIGH] XSS attribute breakout — **FIXED & VERIFIED** (test is weak)

- `escapeHtml` now escapes all 5 entities (`& < > " '`) in `web/templates/base.html:143-151` and `web/templates/leaderboard.html:96-105`
- **Independent tokenization of the new implementation confirms closure**: payloads `x" onmouseover=window.__pwned1=true//`, `y' onclick='window.__pwned2=2`, and `"><img src=x onerror=alert(1)>` placed in `data-cname="${escapeHtml(...)}"` produce **zero rogue injected attributes** in all cases
- All `escapeJs`-in-attribute usages are gone across `server.html`, `my_connections.html`, `users.html`, `user_share.html`; templates now use `data-cid`/`data-cname`/`data-proto` + `this.dataset` (grep: zero remaining `escapeJs(...)` call sites; only the dead definition remains in base.html)
- Kit-copy/download buttons that previously interpolated config text into `onclick` now bind via `querySelector('.btn-kit-copy').onclick = ...` closures

**Caveat**: the "Adversarial XSS Quote Breakout" test exists as a subtest inside `TestAdversarialTemplateSecurityAndResilience` (not the standalone `TestAdversarialXSSQuoteBreakout` named in the handover), and it only asserts that Go's **server-side** auto-escaping HTML-encodes the payload — it never exercises the **client-side JS builder** where the original vulnerability lived. The test would pass even if the JS were still vulnerable. Recommended follow-up: a test that renders the page, extracts the inline JS, and asserts no attribute-interpolation pattern remains (or a browser-based check).

### Finding 3 [MEDIUM] Leaderboard first-paint — **FIXED & VERIFIED**

- `LeaderboardPageHandler` now queries `GetLeaderboard`, computes `current_user_rank` and `monthly_label`, passes all four context keys (`internal/handlers/pages.go:159-189`)
- Rank-card visibility is server-driven: `{{ if .current_user_rank }}flex{{ else }}none{{ end }}` (`web/templates/leaderboard.html:27`)
- Test seeds users and asserts `speedrunner`, formatted traffic (`9.54 MB`), and rank badges in the first-paint HTML — passes

### Finding 4 [MEDIUM] Share 404 contract — **FIXED & VERIFIED**

- `SharePageHandler` writes `http.StatusNotFound` before rendering (`internal/handlers/share.go:22-26`)
- `user_share.html` has a proper `{{ if .not_found }}` branch with translated copy; all 5 translation files contain `share_not_found`
- Client-side `loadConnections()` now treats 401/403/404 as terminal instead of showing "no active connections"
- `share_test.go` asserts 404 status + body — passes

### Finding 5 [MEDIUM] CleanReferer backslash bypass — **FIXED & VERIFIED**

The new `CleanReferer` (`internal/handlers/template.go:818-855`) was copied verbatim and executed against every bypass variant:

```
CleanReferer("https://evil.com/\\evil.com")   = "/"   (blocked)
CleanReferer("https://evil.com/%5Cevil.com")  = "/"   (blocked)
CleanReferer("https://evil.com/%5cevil.com") = "/"   (blocked)
CleanReferer("/\\evil.com")                   = "/"   (blocked)
CleanReferer("/%5Cevil.com")                  = "/"   (blocked)
CleanReferer("//evil.com")                    = "/"   (blocked)
CleanReferer("\\\\evil.com")                  = "/"   (blocked)
CleanReferer("/\\evil.com?next=1")            = "/"   (blocked)
CleanReferer("https://good.com/normal/path")  = "/normal/path"  (legit passes)
CleanReferer("/my")                            = "/my"           (legit passes)
CleanReferer("/server/1?tab=2")               = "/server/1?tab=2" (legit passes)
```

Test suite includes all required adversarial cases (`template_test.go:602-622`). Vector closed.

### Finding 6 [LOW] Locale normalization — **FIXED & VERIFIED**

- `negotiateLocaleWithFallback` normalizes query param and both cookies (`strings.ToLower(strings.TrimSpace(...))`) before validation; DB appearance fallback normalized too
- Tests cover `?lang=RU`, `%20Ru%20`, `+ru+`, `Fa`, `FR`, `ZH`, cookie `" RU "` — all pass

### Findings 7 & 10 [LOW/INFO] Performance — **FIXED**

- `all_translations_json` removed from the render context (zero template references, zero construction — confirmed by grep)
- `translationsJSONCache` pre-marshals per-language JSON once, guarded by `sync.Once` (no data race; confirmed by `-race` pass)
- Appearance settings fetched once per render and threaded into `negotiateLocaleWithFallback` (no duplicate `GetSetting("appearance")` query — confirmed in `RenderTemplate` source)

**New minor inefficiency introduced**: the exported `NegotiateLocale` now queries `GetSetting("appearance")` from the DB **before** checking query/cookies — inverting cheap-first ordering. No production callers were found outside tests, so impact is nil today; noted for hygiene.

### Finding 8 [LOW] Fail-fast engine init — **FIXED (acceptable)**

- `InitTemplateEngine` returns the error; `GetTemplateEngine` panics on failure; `ReloadTemplates` builds a fresh map and swaps atomically under the write lock (`internal/handlers/template.go:59-155`)
- Caveat: startup (`main.go`) does not call `InitTemplateEngine` eagerly, and `Recoverer` middleware converts a runtime panic into a 500 rather than killing the process — so a shipped broken template yields persistent 500s on first render instead of process death. Calling `InitTemplateEngine` in `main()` would be strictly better.

### Finding 9 [LOW] showToast — **FIXED**

`showToast` builds DOM nodes with `textContent` for icon and message (`web/templates/base.html:212-226`). Verified.

### Finding 11 [INFO] Public leaderboard — **NOT FIXED, BY DESIGN**

Still public (`internal/router/router.go:127`, `268`). The rework spec deliberately scoped this out as a follow-up; it was an INFO-level pre-existing Phase 5 router decision. Correctly not claimed as fixed.

---

## CONFIRMED REGRESSIONS INTRODUCED BY THE REWORK

### Regression A [HIGH] `/server/{id}` page is functionally broken — `SERVER_ID` renders as `null`

**Location**: `internal/handlers/pages.go:42-64` (rewritten `ServerPageHandler`) vs `web/templates/server.html:567`

The rework dropped the `"server_id": serverIDStr` context key from `ServerPageHandler`, but `server.html` still contains:

```javascript
const SERVER_ID = {{ .server_id }};
```

Go's `html/template` renders a missing map key as `null` — syntactically valid JS, so the page returns 200 and `TestPageHandlers/ServerPageHandler` (status-code-only assertion) passes, giving false confidence.

**Empirically confirmed** with a probe compiled against the real handler + real embedded template:

```
SERVER_ID snippet: "const SERVER_ID =  null ;\n    let currentInstallProto = 'awg';..."
```

Every subsequent operation on the page — `apiCall(`/api/servers/${SERVER_ID}/check`)`, `/stats`, `/reachability`, `/install`, `/uninstall`, `/container/toggle`, `/server_config` (15+ call sites) — issues requests to `/api/servers/null/...`, all of which fail. **The entire server management page is inoperable.**

**Recommended fix**: restore `"server_id": serverID` in the context map, and extend the page test to assert the rendered body contains `const SERVER_ID = <sID>;`.

### Regression B [MEDIUM] `ServerName` enrichment is a no-op — `range` copies the struct

**Location**: `internal/handlers/pages.go:132-137`

```go
for _, c := range conns {
    if sClean, ok := serversMap[c.ServerID]; ok {
        c.ServerName = sClean.Name
    } else if c.ServerID > 0 {
        c.ServerName = fmt.Sprintf("Server #%d", c.ServerID)
    }
}
```

`conns` is `[]models.UserConnection`; `c` is a **value copy**. Both assignments mutate the copy and are discarded. The dev handover claims this "mapped `ServerName` onto `UserConnection` instances" — it does not.

**Empirically confirmed**: the rendered `/my` page's `initialConnections` JSON contains no `server_name` field at all (the struct tag is `omitempty` and the value stays empty):

```
initialConnections = [{"id":"c-probe","user_id":"u-probe","server_id":1,"protocol":"awg",
"client_id":"cid","name":"MyConn",...}]   <-- no server_name key
```

The client-side fallback `conn.server_name || ('Server #' + conn.server_id)` masks it visually, so user impact is cosmetic — but the handler code and the handover claim are both wrong, and the server-rendered connection card (`my_connections.html:80`) shows the raw `Server #{{ $c.ServerID }}` number instead of the name.

**Recommended fix**: use `for i := range conns { conns[i].ServerName = ... }`.

### Regression C [LOW] `fmt.Sscanf` replaced `parseServerID` — accepts partial garbage

**Location**: `internal/handlers/pages.go:45-48`

The rework replaced the strict `parseServerID` (`strconv.ParseInt`) with `fmt.Sscanf(serverIDStr, "%d", &serverID)`. Empirically verified parsing behavior:

```
Sscanf("12abc") -> 12  (accepted; ParseInt rejects)
Sscanf("1e2")   -> 1   (accepted; ParseInt rejects)
Sscanf("+5")    -> 5   (accepted)
Sscanf(" 7 ")   -> 7   (accepted)
```

So `/server/12abc` now loads server 12 instead of redirecting to `/`. Minor, but an unforced weakening introduced by the rework — the original strict helper still exists in `servers.go:931` and is simply unused here.

**Recommended fix**: revert to `parseServerID(r)`.

---

## Discrepancies in the Handover Documents

1. **`TestAdversarialXSSQuoteBreakout`** is named as a standalone test in the dev handover and QA report; it actually exists as subtest "Adversarial XSS Quote Breakout" inside `TestAdversarialTemplateSecurityAndResilience`. Content present; name claim wrong.
2. The credential-leak test does not use a "realistic RSA/ED25519 private key" as the rework spec mandated — the key fixture is `[REDACTED PRIVATE KEY]` and the corresponding assertions check strings never present in the input.
3. The claim "mapped `ServerName` onto `UserConnection` instances" is false (Regression B — the loop is a no-op).
4. The QA report repeats the dev handover's claims without catching Regressions A/B/C — its verification of the server page evidently only checked status codes. The status-200 assertion in `TestPageHandlers/ServerPageHandler` is precisely why Regression A slipped through both dev and QA.

---

## Final Assessment

**REQUEST CHANGES**

The security findings from the original review are genuinely and correctly fixed: the SSH credential leak is closed (verified by rendering the real page with credentialed fixtures and by `json:"-"` confirmation), the XSS attribute-breakout vector is closed (verified by independent HTML tokenization of the new client-side escaping), and the open-redirect bypass is closed (verified by executing the new `CleanReferer` against every variant). However, the rework introduced a HIGH-severity functional regression that makes the entire server management page inoperable (`SERVER_ID = null`), a dead-code no-op that contradicts the handover's explicit claims, and a small parsing weakening — all three invisible to the existing status-code-only tests. Fix Regressions A (restore the `server_id` context key and assert it in the test) and B (index-based assignment), preferably revert C, and re-run QA; the security work itself — the hard part — is done and done correctly.