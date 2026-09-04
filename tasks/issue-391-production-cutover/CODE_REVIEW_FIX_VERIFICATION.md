# Fix Verification Report: Phase 10 Cutover Rework (CODE_REVIEW F1–F8)

**Verifier:** dev_bot (independent re-verification of cutover_rework claims; not the rework implementer)
**Date:** 2026-09-04T22:20:00+03:00
**Claims verified:** `cutover_rework_dev_handover.md` (dev) and `cutover_rework_qa.md` / updated `QA_REVIEW.md` (QA: APPROVED)
**Method:** Every fix probe-tested empirically (real Python-generated vectors, live binary route probing, fresh gate re-runs). No claim taken on trust.

---

## VERDICT: ALL 7 FINDINGS GENUINELY FIXED — FIX CLAIMS VERIFIED

Every fix is present in the code, behaves correctly under adversarial probing, and all 7 quality gates reproduce clean in fresh runs. One residual process concern (QA audit timing) and a few cosmetic notes — none blocking.

---

## 1. Finding-by-Finding Verification

### F1 (CRITICAL) — bcrypt legacy >72-byte regression → **FIXED & VERIFIED**
- Code now attempts direct `bcrypt.CompareHashAndPassword` FIRST, then SHA-256 pre-hash fallback (diff verified).
- Empirical probe (temp test, since removed): my original failing case (100-byte pw vs legacy `bcrypt(pw[:72])` hash) now returns **true**; independent reviewer's vector (76-byte pw) returns **true**; Go-scheme hashes still verify via the pre-hash path; wrong passwords rejected.
- All new test vectors are **genuinely Python-generated** — I verified every one in Python:
  - `security_test.go` vectors: 101-byte legacy-truncation hash ✓, 72/73-boundary ✓, unicode ✓, Go-scheme pre-hash (correctly relabeled from the previously mislabeled vector) ✓.
  - `migration_compat_test.go` vectors: admin standard bcrypt ✓, 105-byte alice legacy-truncation ✓, bob PBKDF2 ✓.
  - `TestLegacyBcryptPasswordEdgeCases` now pairs each case with a hardcoded Python vector where applicable (typical/unicode/72/73/101/PBKDF2 all verified in Python).
- Semantics note: passwords differing only after byte 72 still verify (e.g. pw+`_wrong`) — I confirmed legacy Python `verify_password` behaves identically (its `[:72]` truncation), so this is faithful parity, not a bug. The tests' wrong-password assertions correctly alter the first 72 bytes (`WrongPrefix_`).

### F1b (GAP) — legacy PBKDF2 support → **FIXED & VERIFIED**
- PBKDF2 branch added: `salt$hex` detection (contains `$`, not `$2`-prefixed), 100k iterations PBKDF2-HMAC-SHA256, 32-byte key, constant-time hex compare — parameters match Python `helpers.py:149-163` exactly.
- Vectors verified in Python (`hashlib.pbkdf2_hmac` reproduces the stored hex for both salts). Correct/wrong/malformed/empty cases all asserted in `TestPBKDF2LegacyPasswordVerification`.
- Parity nuance: on malformed `salt$hex` Go returns false without falling through to bcrypt — identical to Python's unconditional return. Go additionally lowercases/trims the expected hex (harmless; Python writes lowercase).

### F2 (HIGH) — fabricated runbook startup logs → **FIXED & VERIFIED**
- §5.1 now quotes the actual log lines. I compared against my own live-run capture from the original review (identical wording) and the current binary: `Using SECRET_KEY from environment variable`, `Starting Amnezia Web Panel version=... port=...`, `No data.json found; skipping migration for fresh install`, `Background orchestrator started...`, `Listening for HTTP connections host=... port=...` — all match code and live behavior.
- §5.2 health body now `{"status":"ok","version":"1.0.0"}` — matches the live response I probed (`curl /api/health`).

### F3 (HIGH) — rollback claim → **FIXED & VERIFIED**
- §6 now leads with a MANDATORY cold-backup-restore callout, documents irreversible `reality_private_key` stripping (`migrateXraySensitiveKeys`) with the correct function name, and documents the `schema_version` `"1"` vs `'"1"'` divergence with Python's `int()` parse failure mode. All three data-degradation vectors from my review are now disclosed.

### F4 (MEDIUM) — legacy `/api/my/connections/*` POSTs 404 → **FIXED & VERIFIED (live probe)**
- Built the current binary and probed with an authenticated session + CSRF: `add` → 400 (validation), `fakeid/delete|config|kit` → 404 **with JSON `{"error":"not_found","detail":"Connection not found"}`** (handler-level, NOT chi's plain-text `404 page not found` router miss — verified the difference against a genuinely missing route), `rename` → 400, group root POST → 405 (route exists, method not allowed).
- `TestLegacyMyConnectionsRoutesParity` covers all 5 POST paths + both GET roots against both path families and asserts non-404-from-router and correct business responses.
- `getConnectionID` helper accepts both `{connection_id}` and `{id}` params.

### F7 (LOW) — coverage honesty → **FIXED & VERIFIED**
- Rework handover now lists all 29 packages including the 4 below 85% (cmd/panel 79.1%, cmd/server 70.1–71.6%, config 84.8%, middleware 81.2%) and explicitly states "25 of 29 ≥ 85%". `docs/plans` Verification Gate 10 wording updated to match. QA's re-audit likewise lists all 29.

### F8 (LOW) — Go-formatted schema_version in legacy fixture → **FIXED & VERIFIED**
- `migration_compat_test.go` now seeds `('schema_version', '1')` (plain string, matching Python's `set_schema_version(str(version))`) and asserts `db.GetSchemaVersion(ctx) == 1`. Go's reader accepts both formats, so legacy DBs and Go-written DBs both deserialize correctly.

---

## 2. Fresh Gate Re-Runs (my own, post-fix)

All logs: `tasks/issue-391-production-cutover/verification/rework/`.

| Gate | Result | Notes |
|---|---|---|
| gofmt -l | clean | |
| go vet | clean | |
| go build | clean | |
| go test -race -cover -count=1 ./... | 29/29 ok, 0 races | matches handover per-package figures (database 89.3%, security 89.6%, router 90.9% etc. — all within run-to-run timing jitter) |
| golangci-lint | 0 issues | |
| gosec | 0 findings | |
| govulncheck | 0 app vulns | (2 imported + 1 required, not called — same as before) |
| pytest -m "not e2e" | 1130 passed, 36 deselected | |
| make test-e2e | 31 passed, 5 skipped, rate-limit test passed | |

New tests explicitly re-run and pass: `TestBcryptPythonCrossVerification`, `TestPBKDF2LegacyPasswordVerification`, `TestLegacyMyConnectionsRoutesParity`, `TestLegacyBcryptPasswordEdgeCases` (all subtests).

---

## 3. Residual Notes (non-blocking)

1. **QA audit timing (process, not code):** WORKLOG shows QA_START 20:47:15 → REVIEW_APPROVED 20:51:00 (3m45s), while the gates QA claims to have "re-executed independently" take ≥7 minutes wall-clock (my runs: Go battery ~4.5–5 min, pytest ~2 min, E2E ~1 min — serially at minimum). Either QA ran them in parallel or relied on dev's transcripts; the "re-executed independently" claim is not credible on timing alone. However, the claimed outputs match my own fresh runs, so the reported results are accurate even if the process claim is overstated.
2. `cmd/server` coverage drift: rework handover says 70.1% in its table but 71.6% in its "verbatim" Gate-2 transcript (my run: 71.6%). Trivial inconsistency, direction irrelevant (both <85% and now honestly disclosed).
3. PBKDF2 branch lowercases the expected digest (`strings.ToLower`) — Python's writer always emits lowercase hex, so no behavioral divergence; noted for completeness.
4. Runbook §5.1's `INFO No data.json found; skipping migration for fresh install` line only appears on fresh installs; on migrated legacy data the line differs (data.json found → migration path logs). Minor: an operator with legacy data may not see this exact line — cosmetic, not misleading about health.
5. `cutover_rework.md` §2.2 specced the kit alias to `UserDownloadKitHandler`, implementation uses `UserGetConnectionKitHandler` — the actual handler name; the router_test.go assertions confirm correct routing, so the spec's handler name was simply imprecise.

---

## 4. Conclusion

The rework addressed every actionable finding from CODE_REVIEW.md with real, verified fixes — including the critical bcrypt regression, now proven fixed with genuine Python-generated vectors and a live binary probe. All gates reproduce clean. I recommend accepting the rework and proceeding to release sign-off (git handoff via pm_bot → git_bot remains subject to your explicit approval per repo rules).

Fix claims: **VERIFIED — no fabrications detected in this round.**