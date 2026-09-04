# QA Audit Report: Phase 10 Cutover Rework & Issue #391 Remediation

**Auditor:** `qa_bot` (Senior Quality Assurance Engineer & Adversarial Code Auditor)  
**Date:** 2026-09-04T20:50:00+03:00  
**Target Specifications & Inputs:**
- [`tasks/issue-391-production-cutover/CODE_REVIEW.md`](CODE_REVIEW.md)
- [`tasks/issue-391-production-cutover/cutover_rework.md`](cutover_rework.md)
- [`tasks/issue-391-production-cutover/cutover_rework_dev_handover.md`](cutover_rework_dev_handover.md)
- [`docs/plans/2026-08-25-go-rewrite.md`](../../docs/plans/2026-08-25-go-rewrite.md)
- [`docs/migration-runbook.md`](../../docs/migration-runbook.md)

---

## 1. Executive Verdict & Summary

### **Verdict: APPROVED**

The independent adversarial re-audit of the Phase 10 Cutover Rework (Issue #391 Remediation) has completed across all 3 protocol stages. All seven defects and documentation gaps identified in [`tasks/issue-391-production-cutover/CODE_REVIEW.md`](CODE_REVIEW.md) (**F1**, **F1b**, **F2**, **F3**, **F4**, **F7**, **F8**) have been thoroughly resolved, verified with genuine Python test vectors, and proven clean under adversarial automated and manual test harnesses.

### Remediation Verification Matrix:

| Finding ID | Severity | Description | Remediation Implemented | QA Audit Verification Status |
| :--- | :--- | :--- | :--- | :---: |
| **F1** | **CRITICAL** | Bcrypt regression breaking legacy $>72$-byte password authentication | Restored dual-path bcrypt verification in `internal/security/security.go`: direct compare first, then SHA-256 pre-hash fallback. | **VERIFIED & RESOLVED** |
| **F1b** | **GAP** | Lack of legacy PBKDF2 password authentication support | Implemented PBKDF2-HMAC-SHA256 verification (100,000 iterations, constant-time compare) in `security.go`. | **VERIFIED & RESOLVED** |
| **F2** | **HIGH** | Fabricated startup log lines and `/api/health` response body in runbook | Updated `docs/migration-runbook.md` §5.1 with verbatim live Go startup logs and §5.2 with `{"status":"ok","version":"1.0.0"}`. | **VERIFIED & RESOLVED** |
| **F3** | **HIGH** | Inaccurate in-place rollback claim without data stripping disclosure | Rewrote `docs/migration-runbook.md` §6 mandating pre-migration cold backup restoration and disclosing `reality_private_key` stripping and `schema_version` divergence. | **VERIFIED & RESOLVED** |
| **F4** | **MEDIUM** | Legacy POST endpoints (`/api/my/connections/*`) returning 404 | Mounted legacy `/api/my/connections/*` POST endpoints in `internal/router/router.go` with parameter fallback in `getConnectionID`. | **VERIFIED & RESOLVED** |
| **F7** | **LOW** | Handover coverage table omissions and slight numeric drift | Completely disclosed transparent statement coverage across all 29 Go packages in documentation and handovers. | **VERIFIED & RESOLVED** |
| **F8** | **LOW** | Seeding Go-formatted `'"1"'` in legacy SQLite snapshot test fixture | Seeded `schema_version` as `'1'` (matching real Python legacy database outputs) in `migration_compat_test.go`. | **VERIFIED & RESOLVED** |

---

## 2. Stage 1: Automated Gate Execution Battery

All 7 automated compilation, testing, code quality, security, and integration gates were re-executed independently:

| Quality / Security Gate | Command | Result & Output Summary | Status |
| :--- | :--- | :--- | :---: |
| **Gate 1: Go Format, Vet & Build** | `cd amnezia-web-ui-go && go fmt ./... && go vet ./... && go build ./...` | Exit 0, 0 formatting diffs, 0 vet errors, clean binary compilation | **PASS** |
| **Gate 2: Race & Statement Coverage** | `go test -race -cover -count=1 ./...` | Exit 0, 29/29 packages PASS, 0 data races, transparent coverage reported across all 29 packages | **PASS** |
| **Gate 3: Linter** | `golangci-lint run ./...` | Exit 0, 0 linter issues | **PASS** |
| **Gate 4: Static Security Scanner** | `gosec -quiet ./...` | Exit 0, 0 security findings | **PASS** |
| **Gate 5: Vulnerability Audit** | `govulncheck ./...` | Exit 0, 0 application code vulnerabilities | **PASS** |
| **Gate 6: Python Unit Test Suite** | `pytest -m "not e2e"` | Exit 0, **1130 passed**, 36 deselected in 118.88s | **PASS** |
| **Gate 7: E2E Playwright Suite** | `make test-e2e` | Exit 0, **31 passed, 5 skipped**, rate-limiting verified in 54.76s | **PASS** |

### Complete & Transparent Go Statement Coverage Breakdown (All 29 Packages):

```
ok  	github.com/devops-igor/amnezia-web-ui-go/cmd/panel	1.171s	coverage: 79.1% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/cmd/server	1.127s	coverage: 70.1% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/config	1.025s	coverage: 84.8% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/database	176.438s	coverage: 89.3% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/handlers	75.405s	coverage: 85.3% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager	1.018s	coverage: 85.7% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg	1.036s	coverage: 86.7% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/cps	1.018s	coverage: 85.3% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/health	1.096s	coverage: 85.5% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/awg/tc	1.025s	coverage: 86.1% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/dns	1.020s	coverage: 88.7% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/mtproxyl	1.028s	coverage: 88.5% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh	3.901s	coverage: 88.1% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/middleware	1.442s	coverage: 81.2% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/models	1.028s	coverage: 92.0% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/router	4.697s	coverage: 90.9% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/security	57.652s	coverage: 89.6% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/service	1.082s	coverage: 93.8% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/service/orchestrator	5.503s	coverage: 87.2% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/service/reconciliation	1.745s	coverage: 90.1% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/service/remnawave	1.291s	coverage: 88.2% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/service/supervisor	1.328s	coverage: 92.9% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/service/userops	1.666s	coverage: 86.7% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/vpn	1.724s	coverage: 90.2% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/vpn/endpoint	1.890s	coverage: 88.6% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/vpn/forwarder	1.884s	coverage: 95.4% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/vpn/loadbalancer	1.139s	coverage: 97.9% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/internal/vpn/tunnel	1.637s	coverage: 92.9% of statements
ok  	github.com/devops-igor/amnezia-web-ui-go/web	1.031s	coverage: 100.0% of statements
```

---

## 3. Stage 2: Test & Mock Fidelity Audit (Anti-Drift Verification)

1. **Dual-Path Bcrypt Verification**:
   - `internal/security/security.go` implements:
     1. Direct `bcrypt.CompareHashAndPassword` evaluating full raw password bytes against hash. For legacy hashes generated via Python `bcrypt.hashpw(pw[:72], salt)`, `golang.org/x/crypto/bcrypt` succeeds because it evaluates up to 72 bytes.
     2. If direct compare fails, SHA-256 pre-hashed compare evaluates `sha256.Sum256([]byte(password))` against the hash, authenticating new Go-generated $>72$-byte passwords.
     3. Tested with Python-generated test vectors in `TestBcryptPythonCrossVerification` and `TestLegacyBcryptPasswordEdgeCases`:
        - Standard passwords ($\le 72$ bytes) $\to$ PASS
        - Boundary passwords ($72$ bytes, $73$ bytes) $\to$ PASS
        - Long legacy passwords ($101$ bytes, truncated) $\to$ PASS
        - New Go pre-hashed passwords ($>72$ bytes) $\to$ PASS
        - Unicode passwords with multi-byte characters $\to$ PASS
        - Tampered/incorrect passwords $\to$ Correctly rejected with `false`

2. **PBKDF2 Legacy Hash Authentication**:
   - `security.CheckPasswordHash` inspects non-`$2` hashes with `$` as `salt$hex` PBKDF2 hashes.
   - Evaluates `pbkdf2.Key([]byte(password), []byte(salt), 100000, 32, sha256.New)` with constant-time equality check `subtle.ConstantTimeCompare`.
   - Verified with authentic Python PBKDF2 test vectors in `TestPBKDF2LegacyPasswordVerification` and `migration_compat_test.go`.

3. **Legacy POST Route Aliases Parity**:
   - `internal/router/router.go` mounts `/api/my/connections/*` alongside `/api/connections/*`.
   - `getConnectionID(r)` helper extracts either `{connection_id}` or `{id}` seamlessly.
   - `TestLegacyMyConnectionsRoutesParity` in `router_test.go` confirms parity across `add`, `rename`, `config`, `kit`, and `delete`.

4. **Authentic SQLite Legacy Snapshot Seeding**:
   - In `internal/database/migration_compat_test.go`, `schema_version` is seeded as `'1'`, strictly matching legacy Python outputs.
   - `db.GetSchemaVersion(ctx)` properly deserializes this value.

---

## 4. Stage 3: Adversarial Correctness, Security & Failure-Mode Audit

1. **Migration Runbook Accuracy (`docs/migration-runbook.md`)**:
   - §5.1 contains verbatim startup logs matching live application output.
   - §5.2 contains `{"status":"ok","version":"1.0.0"}` matching `HealthResponse`.
   - §6 mandates pre-migration cold backup restoration and provides explicit technical rationale:
     - First Go boot executes `migrateXraySensitiveKeys`, stripping `reality_private_key` from `servers.protocols` JSON.
     - Go writes JSON-encoded string `'"1"'` to `schema_version`, which Python's `int(row["value"])` cannot parse without backup restoration.

2. **Security & Cryptography Integrity**:
   - Zero hardcoded production secrets.
   - Fernet decryption cross-verified with authentic Python cryptography tokens.
   - Constant-time HMAC and PBKDF2 comparisons prevent timing side-channel attacks.

3. **Coverage Honesty**:
   - All 29 Go packages are transparently listed with exact statement coverage figures in `docs/plans/2026-08-25-go-rewrite.md` and handover documentation.

---

## 5. Audit Conclusion

All defects from [`CODE_REVIEW.md`](CODE_REVIEW.md) are **FULLY REMEDIATED**. The codebase, documentation, tests, and runbooks are in an exemplary state.

**Final Recommendation:** Issue #391 is **APPROVED** for release sign-off and git handoff.
