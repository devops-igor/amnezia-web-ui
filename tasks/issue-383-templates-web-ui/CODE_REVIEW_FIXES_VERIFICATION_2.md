# Verification Review #2: Residual Fixes Rework Claims (Issue #383)

- **Reviewer**: dev_bot (independent verification of remediation claims)
- **Date**: 2026-09-01
- **Scope**: Verification of all claims in `residual_fixes_rework_dev_handover.md` and `residual_fixes_rework_qa.md` against the actual working-tree code
- **Source verification review**: `CODE_REVIEW_FIXES_VERIFICATION.md` (identified 3 regressions + 2 caveats in the first rework)

---

## Summary

* Overall claim: **TRUE for all 3 regressions** — verified with independent probes against the real handlers and real embedded templates
* Regressions fixed and independently verified: **3 / 3** (A, B, C)
* Caveat items fixed: **1 / 2** (locale cheap-first done; fixture hardening 3/4 done — private-key assertion still vacuous)
* New regressions introduced by this rework: **0**
* Full test suite under `-race`: **29/29 packages pass** (re-run independently)
* Compilation gates re-run independently: `go build` ✓, `go vet` ✓, `gofmt` ✓, `go test -race ./...` ✓ (golangci-lint/gosec/govulncheck not installed on this machine — claimed clean, not independently re-verified)

**Verdict: APPROVE WITH MINOR CHANGES** (one-line test fix recommended; non-blocking)

**Method**: Code diff inspection of every changed file, re-execution of the dev's new tests, and — decisively — re-deployment of the same independent Go test probes that originally **confirmed** the three regressions last cycle, compiled against the real `ServerPageHandler`/`MyConnectionsPageHandler` and the real embedded templates.

---

## Per-Claim Verification

### Regression A [HIGH] `SERVER_ID = null` on `/server/{id}` — **FIXED & INDEPENDENTLY VERIFIED**

**Code**: `"server_id": serverID` restored in the template context map (`internal/handlers/pages.go`, diff confirmed).

**Independent probe** (same probe that confirmed the regression last cycle, run against the real handler + real embedded template):

```
RAW SNIPPET: "const SERVER_ID =  1 ;\n    let currentInstallProto = 'awg';..."
```

The rendered page now carries the correct numeric server ID. Note: Go's template engine emits double spaces around actions, so the literal string is `const SERVER_ID =  1 ;` — my initial literal-substring assertion (`const SERVER_ID = 1;`) failed on whitespace, which is why the dev's regex-based test (`const SERVER_ID\s*=\s*%d\s*;`) is the correct assertion form and passes legitimately. Functional behavior verified: SERVER_ID equals the seeded server's ID; no `null` in the output.

**Test coverage**: `TestPageHandlers/ServerPageHandler` now asserts the regex match AND that the body does not contain `const SERVER_ID =  null ;` — this closes the false-confidence gap (status-200-only assertion) that let the regression through last cycle. Re-run: PASS.

### Regression B [MEDIUM] ServerName value-copy no-op — **FIXED & INDEPENDENTLY VERIFIED**

**Code**: loop converted to index-based mutation (`internal/handlers/pages.go:132-138`):

```go
for i := range conns {
    if sClean, ok := serversMap[conns[i].ServerID]; ok {
        conns[i].ServerName = sClean.Name
    } else if conns[i].ServerID > 0 {
        conns[i].ServerName = fmt.Sprintf("Server #%d", conns[i].ServerID)
    }
}
```

**Independent probe** (real handler + real template):

```
REGRESSION B FIXED: initialConnections carries server_name probe-srv
REGRESSION B FIXED: rendered card uses server name
```

The `initialConnections` JSON now contains `"server_name":"probe-srv"`, and the server-rendered connection card displays the server name. Last cycle this exact probe showed the field entirely absent.

**Test coverage**: `TestPageHandlers/MyConnectionsPageHandler` asserts `Main-Server` in the rendered HTML card and `"server_name":"Main-Server"` in the `initialConnections` script block. Re-run: PASS.

### Regression C [LOW] `fmt.Sscanf` accepting partial garbage — **FIXED & INDEPENDENTLY VERIFIED**

**Code**: `ServerPageHandler` reverted to the strict `parseServerID(r)` helper (`strconv.ParseInt`); grep confirms zero remaining `fmt.Sscanf` occurrences in non-test code.

**Independent probe**: `/server/12abc` → 302; additionally probed `/server/1e2` → 302, `/server/+5` → 302, `/server/%207` → 302 (all the partial-garbage variants `Sscanf` used to accept). All rejected cleanly.

**Test coverage**: `TestPageHandlers/ServerPageHandler` asserts `/server/12abc` → 302. Re-run: PASS.

### Caveat 1: `NegotiateLocale` cheap-first evaluation — **FIXED & VERIFIED**

**Code**: `NegotiateLocale` now calls `extractLocaleFromRequest(r)` first (query param `?lang=`, cookies `lang`/`panel_lang`, all normalized+validated), and only queries `GetSetting("appearance")` from the DB when no request-level locale is present (`internal/handlers/template.go:603-620, 576-597`). The DB query is no longer executed on the common cookie-present path.

**Test coverage**: "Language Negotiation" subtest covers query param, cookie `lang`, cookie `panel_lang`, DB fallback, query-param-overrides-DB, and nil-DB default. Re-run: PASS.

### Caveat 2: Credential test fixture fidelity — **PARTIALLY FIXED (3/4)**

**What was done**:
- SSH password fixture `dummy-ssh-password-secret-12345` — real value, asserted by both variable and literal. ✓
- User password hash `$2a$14$dummy-bcrypt-user-password-hash-secret-value-12345` — real value, asserted. ✓
- Share password hash `$2a$14$dummy-bcrypt-share-password-hash-secret-value-67890` — real value, asserted. ✓

**What was NOT done — residual discrepancy [LOW]**:
- The private key fixture is still the placeholder `[REDACTED PRIVATE KEY]` (template_test.go:808), while the assertions check for `"dummy-rsa-private-key-data-xyz"` — a string that is **never in the input**. The `sshKeySecret` variable is assigned and seeded into the fixture but **never directly asserted** (grep for `Contains(htmlOutput, sshKeySecret)` returns nothing). The private-key half of the leak test therefore remains vacuous — identical to the gap flagged in the first verification review.
- The dev handover is honest about this (it lists `[REDACTED PRIVATE KEY]` as the fixture), but the QA report overstates: it claims "realistic test fixtures containing dummy SSH passwords, **RSA private key blocks**, and bcrypt password/share hashes" — there is no RSA private key block in the fixture.

**Impact assessment (why non-blocking)**: the actual leak vector is closed and covered — `json:"-"` on `SSHPass`/`SSHKey` plus the `SanitizedServerForUser` projection are all in place, and the SSH **password** assertion exercises the identical marshaling/rendering path that the key would traverse. If someone reintroduced the raw-server leak, the password assertion would fire even though the key assertion would not. The key-specific gap only matters if a future change leaked `SSHKey` through a path that doesn't also leak `SSHPass` — unlikely but not impossible.

**Recommended fix (one line)**: add `|| strings.Contains(htmlOutput, sshKeySecret)` to the private-key assertion (and to the `marshaledServer` assertion), or seed a realistic multi-line dummy PEM block. Until then the test is weaker than the handover implies.

---

## New-Regression Sweep

- `serverID` is now passed as `int64` instead of the previous `string` — template renders it identically for the numeric use in `server.html`; no consumer of a string form exists in the template. No issue.
- `negotiateLocaleWithFallback` (used by `RenderTemplate`) and the new `extractLocaleFromRequest` share identical normalization/validation semantics — no behavioral divergence between the render path and the exported `NegotiateLocale` path.
- Full suite re-run under `-race` with `-count=1`: **29/29 packages pass, 0 failures** — no regressions detected anywhere in the codebase.

---

## Discrepancies in the Handover Documents

1. **QA report**: claims fixtures include "RSA private key blocks" — false; the fixture is the `[REDACTED PRIVATE KEY]` placeholder and the corresponding assertion is vacuous (detailed above). Dev handover is accurate on this point; QA overstated.
2. No other discrepancies found. Test names, locations, and gate results in the dev handover all check out against the working tree.

---

## Final Assessment

**APPROVE WITH MINOR CHANGES**

All three regressions identified in the previous verification review are genuinely fixed — confirmed not by trusting the new tests, but by re-running the same independent probes that originally exposed the bugs, against the real handlers and real templates: `SERVER_ID` now renders with the correct numeric ID, `server_name` now appears in the rendered output and `initialConnections` JSON, and partial-garbage server IDs redirect strictly. The cheap-first locale optimization is in place, and the credential test fixtures are now mostly realistic, with the password and hash assertions capable of actually firing. The one residual item is a vacuous private-key assertion (the fixture value is never asserted), which the QA report misrepresents as an "RSA private key block" — a one-line test fix should ride along in the next commit but does not warrant another full rework cycle, since the key's leak path is shared with the password (which IS asserted) and the structural defenses (`json:"-"`, sanitized projection) are verified. This is ready for git handoff once that one-line test fix lands.