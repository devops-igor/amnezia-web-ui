# Task: Phase 10 — Production Cutover, Staging Verification & Release Finalization (Issue #391)

## 1. Overview & Objectives
Phase 10 represents the final milestone of the Amnezia Web Panel Go rewrite. The goal is to perform exhaustive database compatibility verification against legacy data snapshots, validate real-world credential decryption and protocol orchestration integrity, produce a comprehensive production migration runbook, finalize documentation, and verify that all 14 Done-Done Acceptance Criteria defined in [`docs/plans/2026-08-25-go-rewrite.md`](../../docs/plans/2026-08-25-go-rewrite.md) are fully satisfied.

### Primary Specifications
- [`docs/plans/2026-08-25-go-rewrite.md`](../../docs/plans/2026-08-25-go-rewrite.md) (§3 Phase 10, §4 Tech Stack Mapping, §8 Done-Done Acceptance Criteria)
- [`docs/specs/01-domain-model.md`](../../docs/specs/01-domain-model.md) through [`docs/specs/06-background-jobs.md`](../../docs/specs/06-background-jobs.md)
- [`docs/deployment.md`](../../docs/deployment.md)
- [`README.md`](../../README.md)

---

## 2. Requirements & Deliverables

### 2.1 Binary Database Compatibility & Staging Simulation Testing
1. **End-to-End Legacy Database Compatibility Tests**:
   - Create comprehensive tests (e.g. in `internal/database/` or `internal/service/`) simulating production database cutover from legacy Python installations.
   - Test loading a database containing existing data across all legacy tables (`servers`, `users`, `user_connections`, `settings`, `known_hosts`, `leaderboard_snapshots`).
   - Verify that startup schema initialization (`InitSchema` / `Open`) safely applies additive schemas (`backend_tunnels`, `vpn_sessions`, `vpn_config`) with zero data loss or row corruption.
   - Verify that legacy Fernet-encrypted fields (SSH passwords, private keys, SSL certificates/keys in settings) decrypt cleanly and identically in Go using the standard master `SECRET_KEY`.
   - Verify that Python bcrypt password hashes for existing users authenticate successfully in Go (with the 72-byte pre-hash safeguard intact).

### 2.2 Production Migration Runbook (`docs/migration-runbook.md`)
1. Create a detailed, step-by-step production cutover and migration guide:
   - **Pre-Migration Checklist**: Database backup (`data/panel.db`), secret key preservation (`data/.secret_key`), config audit.
   - **Host Data Ownership Migration**: Explicit command runbook (`sudo chown -R 1000:1000 ./data`) explaining the transition from legacy Python container permissions to non-root Go `appuser` (UID 1000, GID 1000).
   - **Docker Compose Cutover**: Updating `docker-compose.yml` to the Go image, configuring new environment variables (`VPN_ENABLED`, `VPN_LISTEN_PORT`, `VPN_SUBNET`), and container lifecycle.
   - **Post-Migration Verification**: Verifying the startup preflight probe, healthcheck endpoint (`/api/health`), admin login, user sessions (noting expected re-login), and remote server connectivity.
   - **Rollback Runbook**: Clear, fail-safe instructions to revert to the legacy container without data degradation if ever needed.

### 2.3 Documentation Finalization
1. **`README.md`**:
   - Update architecture diagrams and badges to reflect the pure Go implementation.
   - Highlight key performance and operational improvements: ~38.6MB total image size, ~15MB runtime RSS memory, instant startup (<50ms), native in-process AmneziaWG VPN endpoint, and non-root security model.
   - Update quick-start and developer instructions.
2. **`docs/deployment.md`**:
   - Verify that all operational commands, ports (`5000/tcp`, `51820/udp`), volume mount paths, and security configurations are 100% synchronized with `Dockerfile` and `docker-compose.yml`.
3. **`docs/plans/2026-08-25-go-rewrite.md`**:
   - Update Phase 10 status and verify all 14 Done-Done Acceptance Criteria matrix.

### 2.4 Final Verification & Quality Gates
Execute the complete quality and security gate battery:
1. `cd amnezia-web-ui-go && go fmt ./... && go vet ./... && go build ./...`
2. `go test -race -cover -count=1 ./...` (All 29+ packages passing with 0 data races and statement coverage $\ge 85\%$).
3. `golangci-lint run ./...` (0 issues).
4. `gosec -quiet ./...` (0 findings).
5. `govulncheck ./...` (0 affected vulnerabilities).
6. Root Python test suite: `pytest -m "not e2e"` (100% passing).
7. E2E test runner: `make test-e2e` (or `scripts/run_e2e.sh`).

---

## 3. Strict Orchestration & Guardrails
- All task files, test logs, and intermediate artifacts must be placed in `tasks/issue-391-production-cutover/`.
- Developer must NEVER run `git commit` or `git push`.
- All quality gate outputs must be captured verbatim.

---

## 4. Handoff Deliverable
`dev_bot` must emit `tasks/issue-391-production-cutover/DEV_HANDOVER.md` containing:
- File change manifest.
- Verbatim terminal transcripts of all Go quality gates, Python unit tests, and E2E tests.
- Verification matrix for all 14 Done-Done Acceptance Criteria.
- Notes for `qa_bot` adversarial auditing.
