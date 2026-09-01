# Sub-Task Specification: Template Fixes & Security Hardening Rework

**Issue**: #383
**Task File**: `tasks/issue-383-templates-web-ui/template_fixes_rework.md`
**Source Review**: `tasks/issue-383-templates-web-ui/CODE_REVIEW.md`
**Target Codebase**: `amnezia-web-ui-go/`

---

## 1. Objective & Scope

Remediate all findings (Critical, High, Medium, Low) identified in the senior code review (`tasks/issue-383-templates-web-ui/CODE_REVIEW.md`), implement strict regression test suites, and satisfy all Go compilation and race-detection gates.

---

## 2. Detailed Remediation Requirements

### Finding 1 [CRITICAL] — Prevent SSH Root Credential Leak to Non-Admin Pages (`/my`)
- **Locations**: `internal/handlers/pages.go`, `internal/models/models.go`, `web/templates/my_connections.html`
- **Actions**:
  1. In `MyConnectionsPageHandler`, sanitize servers before passing to template context. Pass only sanitized server objects (e.g. `ID`, `Name`, `Protocols: {"installed": bool}`).
  2. In `internal/models/models.go`, update `models.Server` to mark sensitive fields (`SSHPass`, `SSHKey`, `WireguardPrivateKey`, etc.) with `json:"-"` so that even if a `models.Server` is accidentally serialized to JSON, credentials are never exposed.
  3. In `web/templates/my_connections.html`, ensure only safe server properties are rendered in `availableServers`.
  4. **Mandatory Regression Test**: Write `TestRenderMyConnectionsNoCredentialLeak` in `internal/handlers/template_test.go` that renders `my_connections.html` with a fully credentialed server (containing realistic SSH passwords and RSA/ED25519 private keys) and asserts that the rendered HTML contains zero fragments of the passwords or keys.

### Finding 2 [HIGH] — Fix XSS in HTML Attributes & Remove Unsafe String Interpolation
- **Locations**: `web/templates/base.html`, `web/templates/user_share.html`, `web/templates/my_connections.html`, `web/templates/server.html`, `web/templates/users.html`
- **Actions**:
  1. Fix `escapeHtml` in `web/templates/base.html` to escape quotes (`"`, `'`) in addition to `&`, `<`, `>` (e.g. using a dedicated replacer converting `&` $\to$ `&amp;`, `<` $\to$ `&lt;`, `>` $\to$ `&gt;`, `"` $\to$ `&quot;`, `'` $\to$ `&#39;`).
  2. Fix `escapeJs` in `web/templates/base.html` or ensure JS escaping does not leak into raw double-quoted HTML attribute contexts.
  3. Refactor inline event handlers (`onclick="showConfig('${escapeJs(c.id)}', '${escapeJs(c.name)}')"` and `data-cname="${escapeHtml(c.name)}"`) to use safe data attributes (`data-cid`, `data-cname`) and event listener binding or `this.dataset` lookup, eliminating inline quote breakout vectors.
  4. **Mandatory Regression Test**: Write an adversarial test in `internal/handlers/template_test.go` asserting that connection/user names with payloads like `x" onmouseover="window.pwned=1//` and `<script>alert(1)</script>` produce no executable unescaped attributes or rogue event handlers.

### Finding 3 [MEDIUM] — Restore Leaderboard Initial Server-Side Data Contract
- **Locations**: `internal/handlers/pages.go`, `web/templates/leaderboard.html`
- **Actions**:
  1. In `LeaderboardPageHandler`, aggregate initial data (defaulting to `"all-time"` or current period): query and populate `entries`, `current_user_rank`, and `monthly_label` into the template data map, matching the legacy Python handler (`app/routers/pages.py`).
  2. Ensure `leaderboard.html` properly renders the initial server-side table and personal ranking badge on first paint.
  3. **Mandatory Regression Test**: Add a test verifying that `LeaderboardPageHandler` with seeded users renders the leaderboard table with user rows and rank badge on initial load.

### Finding 4 [MEDIUM] — Handle `.not_found` in `user_share.html` (404 UI Contract)
- **Locations**: `internal/handlers/share.go`, `web/templates/user_share.html`
- **Actions**:
  1. In `web/templates/user_share.html`, add condition `{{ if .not_found }}` to render the translated "share_not_found / share_not_found_desc" error UI with 404 styling when an invalid/expired token is passed.
  2. In `UserSharePageHandler` (`internal/handlers/share.go`), write `http.StatusNotFound` (404) header when `.not_found` is true.
  3. **Mandatory Regression Test**: Add a test asserting that requesting `/share/invalid-token` returns HTTP 404 and renders the not-found banner.

### Finding 5 [MEDIUM] — Fix `CleanReferer` Single-Backslash & Encoded Backslash Bypass
- **Locations**: `internal/handlers/template.go`, `internal/handlers/template_test.go`
- **Actions**:
  1. In `CleanReferer`, reject or sanitize backslashes (`\`) after URL parsing and path extraction (e.g. `if strings.Contains(path, "\\") || strings.Contains(rawReferer, "\\") || strings.Contains(rawReferer, "%5C") { return "/" }`).
  2. Ensure leading slashes normalize properly (`///`, `//`, `/\`, etc. all return fallback `/`).
  3. **Mandatory Regression Test**: Add test cases in `TestCleanReferer`: `https://evil.com/\evil.com`, `https://evil.com/%5Cevil.com`, `/\evil.com`, `/%5Cevil.com`, `//evil.com`, `\\\\evil.com`.

### Finding 6 [LOW] — Normalize Language Codes in `NegotiateLocale`
- **Locations**: `internal/handlers/template.go`, `internal/handlers/template_test.go`
- **Actions**:
  1. In `NegotiateLocale`, normalize input (`strings.ToLower(strings.TrimSpace(lang))`) before validating with `config.IsValidLanguage` and returning it.
  2. **Mandatory Regression Test**: Verify `/my?lang=RU`, `/my?lang= Ru `, and `/my?lang=Fa` return `"ru"` and `"fa"` respectively, with Persian enabling `dir="rtl"`.

### Finding 7 [LOW] & Finding 10 [INFO] — Performance & Allocation Cleanup
- **Locations**: `internal/handlers/template.go`
- **Actions**:
  1. Remove redundant per-request marshaling of `all_translations_json` (105KB) if unused by templates, or pre-compute / cache the JSON bytes.
  2. Avoid querying `appearance` setting twice during `RenderTemplate` (pass negotiated locale or reuse settings fetched from DB).

### Finding 8 [LOW] — Fail Loudly on Template Engine Initialization Failures
- **Locations**: `internal/handlers/template.go`
- **Actions**:
  1. In `GetTemplateEngine()`, return an error or log a fatal message if embedded template parsing fails during `loadTemplates()`.

### Finding 9 [LOW] — `showToast` XSS Hardening
- **Locations**: `web/templates/base.html`
- **Actions**:
  1. In `showToast()`, set message using `textContent` on the message element or wrap through `escapeHtml(message)`.

---

## 3. Compilation & Hard Quality Gates

You MUST satisfy the compilation gate:
```bash
cd amnezia-web-ui-go
go fmt ./...
go vet ./...
go build ./...
go test -race -cover ./...
golangci-lint run ./...
gosec -quiet ./...
govulncheck ./...
```

---

## 4. Required Output Documents

- Write your developer handover strictly to:
  `tasks/issue-383-templates-web-ui/template_fixes_rework_dev_handover.md`
- Append your `DEV_REWORK` start and completion logs to `WORKLOG.md`.
- Do NOT run `git commit` or `git push`.
