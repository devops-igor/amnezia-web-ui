# Sub-Task: Phase 10 Cutover Rework & Code Review Remediation (`cutover_rework.md`)

## 1. Overview & Objectives
An independent code review ([`tasks/issue-391-production-cutover/CODE_REVIEW.md`](CODE_REVIEW.md)) identified several critical, high, and medium defects in the Phase 10 deliverables:
1. **F1 (CRITICAL)**: `internal/security/security.go` `CheckPasswordHash` regression breaking legacy >72-byte bcrypt password authentication.
2. **F1b (GAP)**: Absence of PBKDF2 password verification for legacy pre-bcrypt installations (`app/utils/helpers.py:149-163`).
3. **F2 (HIGH)**: Fabricated startup log lines in `docs/migration-runbook.md` §5.1 and incorrect `/api/health` response payload in §5.2.
4. **F3 (HIGH)**: Inaccurate in-place rollback claim in `docs/migration-runbook.md` §6 failing to mandate backup restoration and omit data stripping caveats (`reality_private_key`).
5. **F4 (MEDIUM)**: Legacy POST paths (`/api/my/connections/*`) returning 404 instead of maintaining contract compatibility with legacy clients and scripts.
6. **F7 / F8 (LOW)**: Selective coverage reporting and seeding Go-formatted `'"1"'` instead of genuine Python `"1"` in migration compatibility test snapshots.

---

## 2. Requirements & Deliverables

### 2.1 Password Verification Hardening (`internal/security/security.go`)
1. **Dual-Path Bcrypt Verification**:
   - In `CheckPasswordHash(hashedPassword, password string) bool`:
     - If `hashedPassword` starts with legacy PBKDF2 format (`salt$hex` or PBKDF2 SHA-256 with 100,000 iterations per Python `app/utils/helpers.py`), verify using `crypto/sha256` and `crypto/subtle.ConstantTimeCompare`.
     - Otherwise, attempt direct bcrypt comparison first:
       ```go
       if err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password)); err == nil {
           return true
       }
       ```
     - If direct comparison fails, attempt SHA-256 pre-hashed comparison:
       ```go
       preHash := sha256.Sum256([]byte(password))
       return bcrypt.CompareHashAndPassword([]byte(hashedPassword), preHash[:]) == nil
       ```
2. **Exhaustive Test Vectors**:
   - In `internal/security/security_test.go` and `internal/database/migration_compat_test.go`:
     - Add real Python-generated bcrypt test vectors for passwords $> 72$ bytes (generated via Python's legacy `bcrypt.hashpw(pw[:72].encode(), ...)`).
     - Add test vectors for legacy PBKDF2 hashes.
     - Verify passwords $\le 72$ bytes, $> 72$ bytes, unicode characters, and boundary lengths ($72$, $73$) pass under all scenarios.

### 2.2 Legacy Route Aliases (`internal/router/router.go`)
1. In `internal/router/router.go`, add legacy alias routes under the authenticated user router:
   - `POST /api/my/connections/add` $\to$ `handlers.UserAddConnectionHandler`
   - `POST /api/my/connections/{id}/delete` $\to$ `handlers.UserDeleteConnectionHandler`
   - `POST /api/my/connections/{id}/kit` $\to$ `handlers.UserDownloadKitHandler`
   - `POST /api/my/connections/{id}/rename` $\to$ `handlers.UserRenameConnectionHandler`
   - `POST /api/my/connections/{id}/config` $\to$ `handlers.UserGetConnectionConfigHandler`
2. Add router unit tests in `internal/router/router_test.go` asserting that calls to both `/api/connections/*` and `/api/my/connections/*` route properly.

### 2.3 Migration Runbook Remediation (`docs/migration-runbook.md`)
1. **Startup Logs (§5.1)**: Replace fabricated log messages with verbatim log entries from live Go application startup.
2. **Health Response (§5.2)**: Update expected JSON payload to `{"status":"ok","version":"1.0.0"}`.
3. **Rollback Procedure (§6)**:
   - State explicitly that rolling back requires restoring the pre-migration cold backup (`panel.db` and `.secret_key`).
   - Document that Go's first boot irreversibly strips `reality_private_key` from `servers.protocols` JSON for security, making pre-migration backup restore mandatory if reverting to a legacy Python Xray deployment.
   - Document the `schema_version` difference (`"1"` in Python vs `'"1"'` in Go).

### 2.4 Test Fixture & Documentation Accuracy
1. In `internal/database/migration_compat_test.go`, seed `schema_version` as `"1"` (matching real Python legacy database output).
2. Update `docs/plans/2026-08-25-go-rewrite.md` and all handover documents to report honest, complete coverage numbers across all 29 packages without omissions.

---

## 3. Hard Compilation & Quality Gates
Before handing off, `dev_bot` must run:
1. `cd amnezia-web-ui-go && go fmt ./... && go vet ./... && go build ./...`
2. `go test -race -cover -count=1 ./...`
3. `golangci-lint run ./...`
4. `gosec -quiet ./...`
5. `govulncheck ./...`
6. `pytest -m "not e2e"`
7. `make test-e2e`

---

## 4. Handoff Deliverables
`dev_bot` must emit `tasks/issue-391-production-cutover/cutover_rework_dev_handover.md` containing:
- File change manifest.
- Verbatim terminal transcripts of all quality gates.
- Honest, complete statement coverage table for all 29 Go packages.
- Notes for `qa_bot` adversarial re-audit.
