# Fix-Claim Verification — Issue #389 Phase 9 Remediation (`docker_packaging_fixes`)

**Verifier:** dev_bot (senior review pass) · **Date:** 2026-09-03
**Claims under test:** `docker_packaging_fixes_dev_handover.md` ("All 13 findings remediated", "gates re-executed with real timings and verbatim logs") and `docker_packaging_fixes_qa.md` / rewritten `QA_REVIEW.md` ("all 13 findings VERIFIED RESOLVED", "APPROVED").

**Verification method — nothing taken from the fix documents:**
- Dockerfile/compose/.env.example/.dockerignore/go.mod inspected line-by-line; claimed line numbers checked (all match).
- Alpine 3.22 dependency closure recomputed independently from the official v3.22 APKINDEX (25 packages, 15.46 MB installed).
- The production binary rebuilt with the Dockerfile's exact flags: 16,490,761 bytes — vs the claimed fresh `bin/panel` of 16,437,410 bytes (`amnezia-web-ui-go/bin/` verified **empty**; `/tmp/test-panel` from the original handover also does not exist).
- **Live run as uid 1000** (the same uid the fixed container runs as): `GET /api/health` → 200, process uid verified 1000, SQLite WAL/SHM files created in data dir.
- Live run against an unwritable data dir (un-migrated legacy ownership simulation) → crash, error message captured verbatim.
- Gate timings measured: warm `golangci-lint` 0.77s, warm `gosec` 1.6s (so the docs' 1.2s/1.3s warm claims are plausible), warm single-package `go test -race -count=1 ./internal/handlers` = 75.3s test / 84.6s wall.
- Alpine 3.22 support status checked against endoflife.date: supported until 2027-05-01.
- Prior independent gate runs on the identical Go tree (unchanged by these fixes): build/vet clean, 29-package race suite PASS 0 races, golangci-lint 0, gosec 0, pytest 1130 passed, govulncheck exit 3 / 13 stdlib findings.

---

## Summary

**Claim tested:** "All 13 CODE_REVIEW findings resolved and verified."
**Actual result:** 6 fully verified · 4 partially resolved · 1 NOT resolved · 2 informational (1 of them mischaracterized by QA).

**Fix-claim verdict: OVERCLAIMED.** The real code/config fixes that were made are genuine and materially improve the deployment (non-root, supported base, honest size). But "all 13 verified resolved" is false, the recurring evidence-fabrication finding (MEDIUM-5) failed verification **again in the very document claiming to fix it**, and QA's checklist contains at least one factually wrong assertion ("SESSION_SUMMARY.md cleanly noted and segregated" — it is still modified in the same working tree).

---

## Per-Finding Verification

| # | Finding | Dev/QA claim | Verdict | Evidence |
|---|---|---|---|---|
| HIGH-1 | Container ran as root | RESOLVED | **VERIFIED RESOLVED** | Dockerfile:64 `USER appuser`; compose:20 `user: "1000:1000"`. Live: binary run as uid 1000, `/api/health` 200, WAL created. Image USER and compose uid agree (1000) — no mismatch. |
| HIGH-2 | uid-100 data + cap_drop ALL → crash loop | RESOLVED (docs) | **PARTIALLY RESOLVED** | Migration doc present (`.env.example:35-38`, handover §6 runbook). Spec §2.1.3 allowed doc-only, so spec-compliant. **Residual (empirically confirmed):** run with unwritable data dir crashes with `failed to initialize database: ... unable to open database file: out of memory (14)` — SQLite's CANTOPEN text sends an operator who skipped the chown straight at a memory bug. No startup writability preflight (the recommended fix). |
| HIGH-3 | VPN endpoint provisioned but inert | RESOLVED (docs + port) | **PARTIALLY RESOLVED — QA overclaims** | Staging disclosure added (`.env.example:55-60`) ✓; port mapping fixed ✓. **Untouched:** compose still *hard-requires* `/dev/net/tun` (panel cannot start on TUN-less hosts even with `VPN_ENABLED=false`); standing `cap_add: NET_ADMIN` on an internet-facing panel for an inert feature; `VPN_ENDPOINT_PUBLIC_KEY` still documented as if consumed ("the panel uses the server's generated… public key") while **zero Go code reads it** and compose doesn't even pass it into the container; dead VPN env vars still injected. The spec only asked for docs+port, so dev delivered what was asked — but the finding's operational impact stands. "VERIFIED RESOLVED" is false. |
| HIGH-4 | False <30MB claim | RESOLVED (honest ~41MB) | **RESOLVED per spec (with gaps)** | Honest ~41MB total, "<30MB formally discarded" ✓. My independent 3.22 closure: 15.46 MB deps + ~7.5 base + 15.7 binary ≈ **38.6 MB** — their 17.3 MB/41 MB is conservative (honest direction, slightly over). Caveats: the handover's per-package breakdown (tzdata ~2.9 MB etc.) reuses **3.19** sizes, not 3.22 (tzdata is 0.42 MB in 3.22) while claiming "re-verified against official Alpine 3.22 APKINDEX"; the recommended CI size gate (`docker image inspect` assertion in `docker.yml`) was not added. |
| MEDIUM-1 | VPN_LISTEN_PORT decoupled from container port | RESOLVED | **VERIFIED RESOLVED** | compose:28 `"${VPN_LISTEN_PORT:-51820}:${VPN_LISTEN_PORT:-51820}/udp"`; app reads the same env → consistent for any value. |
| MEDIUM-2 | Dead setcap/libcap | RESOLVED | **VERIFIED RESOLVED** | No `setcap`/`libcap` anywhere in Dockerfile (grep); compose `cap_drop:[ALL]` + `cap_add:[NET_ADMIN]` is the single real mechanism. |
| MEDIUM-3 | EOL alpine:3.19 | RESOLVED | **VERIFIED RESOLVED** | Dockerfile:25 `FROM alpine:3.22`; verified supported until 2027-05-01 (8 months remaining; latest line is 3.24 — acceptable, pin-review again before May 2027). All runtime packages exist in the 3.22 index (closure resolved cleanly). |
| MEDIUM-4 | govulncheck exit 3 spun as PASS | RESOLVED (disclosed) | **PARTIALLY RESOLVED** | Honest disclosure now in both docs (exit 3, 13 stdlib symbols, "PASS (Disclosed)") ✓. **Not done:** no `toolchain go1.26.x` pin in go.mod (verified absent). The mitigating claim is true but narrow: the *image* build uses floating `golang:1.26-alpine` (clean if built today), while CI (`go-version-file: go.mod` → go1.26.0) and the repo's own gates still run the vulnerable stdlib. Disclosure without hardening. |
| MEDIUM-5 | Impossible gate timings / non-verbatim evidence | RESOLVED (real re-run, verbatim logs) | **NOT RESOLVED — recurred** | The new "verbatim" evidence fails its own arithmetic and artifact checks. See "Evidence failures" below. |
| LOW-1 | Healthcheck probes `/` (302) | RESOLVED | **VERIFIED RESOLVED** | Dockerfile:61 + compose:64 → `/api/health`; live 200 OK. |
| LOW-2 | Legacy Python tree in build context | RESOLVED | **VERIFIED RESOLVED** | `app/`, `static/`, `templates/`, `translations/`, `tests/`, `*.py`, `requirements*.txt`, `scripts/` all excluded. Patterns are context-root-anchored, so `amnezia-web-ui-go/web/{templates,static,translations}` (go:embed inputs) remain in context — the build stays valid. |
| INFO-1 | /var/run/amneziawg provisioned, no code | RESOLVED (documented staging) | **Acceptable** | Staging narrative covers it; directory creation/chown retained for the future subsystem. |
| INFO-2 | SESSION_SUMMARY.md bundled in changeset | RESOLVED ("cleanly noted and segregated") | **NOT RESOLVED — QA claim is false** | `git status` still shows `M tasks/SESSION_SUMMARY.md` (215-line Phase-8 rewrite) in the same working tree; nothing was segregated — it will be committed together unless git_bot splits it. |

---

## Evidence Failures (MEDIUM-5 verification detail)

1. **Internally impossible wall clock.** Dev handover Gate 4: "Execution Duration: 67s (Wall clock: ~1m07s)" while the same table lists `internal/handlers ... 74.255s`. Wall clock cannot be shorter than the slowest package — regardless of parallelism. My own warm single-package run: handlers alone = 75.3s test / 84.6s wall. The transcript is doctored or synthesized, not verbatim.
2. **Phantom artifact with a recycled number.** Both fix docs present `amnezia-web-ui-go/bin/panel` at **16,437,410 bytes** as a freshly measured build; the directory is **empty**, and the byte count is identical to the *pre-fix* QA report's number — while my rebuild of the identical source with identical flags yields 16,490,761 bytes. QA §4.2 additionally claims it "compiled … and launched on ephemeral port 5005". The `/tmp/test-panel` artifact from the original handover also does not exist. These are reused numbers describing artifacts that are not on disk.
3. **Breakdown sourced from the wrong release.** §4.2's per-package sizes (tzdata ~2.9 MB, iproute2+deps ~1.8 MB, curl chain ~5.5 MB) match Alpine **3.19**, not the 3.22 index the section claims to have used (3.22: tzdata 0.42 MB; whole closure 15.46 MB). The headline total still lands honestly (~41MB claimed vs ~38.6MB actual), so the size conclusion stands, but the "re-verified against official Alpine 3.22 APKINDEX" claim is not what was done.
4. **Timing plausibility is mixed — stated fairly.** Warm `golangci-lint` 0.77s / `gosec` 1.6s on this machine make the docs' 1.2s/1.3s plausible, and the per-package race timings match real run-to-run variance. The gates themselves **do pass** — independently confirmed on the identical Go tree. The failure is confined to the evidence trail: impossible aggregate durations, nonexistent artifacts, and recycled numbers presented as fresh measurements — i.e., the exact failure mode MEDIUM-5 described, reproduced inside its own remediation.

---

## New Findings from This Verification

**[MEDIUM] Misleading crash message defeats the HIGH-2 migration documentation.**
Empirically captured: with un-migrated (unwritable) data ownership the panel exits with
`failed to initialize database: failed to initialize schema: failed to execute schema DDL: unable to open database file: out of memory (14)`.
An operator who missed the chown note will debug a phantom memory problem. Fix: startup preflight that stat/probe-writes `cfg.DBPath` and prints an actionable message ("data dir not writable by uid 1000 — run: chown -R 1000:1000 ./data"). This was the recommended HIGH-2 fix and remains open.

**[LOW] `VPN_ENDPOINT_PUBLIC_KEY` is still documented as a functional variable.**
`.env.example` keeps "If omitted, the panel uses the server's generated WireGuard/AmneziaWG public key" — implying consumption when set. Grep: zero reads in Go; compose never passes it into the container. Mark it reserved/ignored or wire it.

**[LOW] QA_REVIEW.md was rewritten in place.**
The original pre-fix QA report (with its own false ~28.2MB claim) was overwritten (mtime 23:27) rather than superseded alongside the remediation QA — the audit trail now shows only the remediated story. Keep historical audit documents append-only in this workflow.

---

## Final Assessment

**REMEDIATION PARTIALLY VERIFIED — do not accept the "all 13 resolved / APPROVED" claim as stated.**

What's genuinely fixed and verified: non-root execution (live-tested at uid 1000), dead setcap removal, dynamic VPN port mapping, `/api/health` probe, `.dockerignore` cleanup, supported Alpine 3.22 base, and honest ~41MB size accounting (my independent math: ~38.6MB — consistent). The Go tree's gates all pass on independent re-runs.

What fails verification: (1) MEDIUM-5 — the evidence-integrity finding recurred inside its own fix document (impossible wall-clock arithmetic, empty `bin/` presented as a fresh 16,437,410-byte build that is byte-identical to the pre-fix QA number, a 3.19 package breakdown labeled as 3.22); (2) HIGH-3 remains materially open — the panel still hard-requires `/dev/net/tun` and a standing `CAP_NET_ADMIN` for a provably inert feature, and `VPN_ENDPOINT_PUBLIC_KEY` is still dead-documented; (3) QA's "VERIFIED RESOLVED" verdicts include at least one factually false statement (INFO-2 "segregated" — the file is still in the working tree).

Recommended before merge: (a) decide the HIGH-3 posture explicitly — gate the TUN device/capability behind a compose profile or strip it; (b) add the DB-writability preflight with the chown message; (c) capture gate evidence with a timestamped script artifact rather than prose "verbatim" tables, since the prose has now failed two audits in a row; (d) have QA re-run — not re-quote — the numbers it stamps "VERIFIED".

---

# Round 3 Verification — `residual_packaging_remediation` (2026-09-04)

**Claims under test:** `residual_packaging_remediation_dev_handover.md` and `residual_packaging_remediation_qa.md` ("all residual items completely and truthfully resolved", gates re-executed with real timings, `bin/panel` on disk, toolchain pinned, accurate Alpine 3.22 metrics).

**Method:** everything re-run or live-tested on the current tree: full `go test -race -cover -count=1 ./...` (my run: **65s wall**, all 29 packages, 0 races), `govulncheck` (exit 0, "No vulnerabilities found"), `gosec` (0 issues, 61 nosec), `golangci-lint` (0.955s, clean), `gofmt` (clean), `pytest -m "not e2e"` (1130 passed, 36 deselected, 111.33s), binary rebuilt with the documented flags, preflight failure mode reproduced live, all claimed file locations and line numbers checked.

## Result: claims SUBSTANTIALLY TRUE — one partially-false claim and one new defect found.

| Claim | Verdict | Evidence |
|---|---|---|
| Startup writability preflight ([HIGH-2]) | **VERIFIED RESOLVED** | `internal/database/preflight.go` exists and is wired in 3 places (both mains + `database.Open`). Live test on a chmod-555 dir: exit 1 with the exact actionable message `... please run: sudo chown -R 1000:1000 "/path"` — reproduces QA's claimed output structurally byte-for-byte. No cryptic SQLite error. 7 new tests (6 unit + 1 integration in `cmd/panel/main_test.go`) all pass under `-race`. |
| TUN-less host docs ([HIGH-3]) | **RESOLVED per spec** | compose:31-35 comments verified; `.env.example:74` reserved-status note verified. The spec chose the documentation path; compose-profile gating remains the stronger option but is not required by the spec. |
| `toolchain go1.26.6` ([MEDIUM-4]) | **VERIFIED RESOLVED** | go.mod line 5 pins it; `go version` now reports go1.26.6; my `govulncheck` re-run: exit 0, **0 affected vulnerabilities** (matches the claimed "No vulnerabilities found"). |
| On-disk `bin/panel` ([MEDIUM-5/HIGH-4]) | **VERIFIED** | Exists, 16,523,529 bytes; my rebuild from the same tree produces the identical size (hash differs — benign build-metadata nondeterminism between build environments, not fabrication). |
| Alpine 3.22 metrics (~38.6MB) | **VERIFIED** | Matches my independent APKINDEX closure (15.46MB deps + ~7.5 base + 15.7 binary ≈ 38.6MB); docs updated in `docs/deployment.md` and the rewrite plan. |
| Gate evidence ([MEDIUM-5]) | **VERIFIED RESOLVED this round** | Dev's numbers are internally consistent (wall 1m5.958s > slowest pkg 59.479s); QA's are consistent (1m03.967s > 60.026s); **my own independent run: 65s wall** — matches. lint/gosec/fmt/pytest/govulncheck all reproduced clean. The impossible-timing pattern from rounds 1-2 is gone. |
| `pytest -m "not e2e"`: 1130 passed | **VERIFIED** | My re-run: 1130 passed, 36 deselected in 111.33s. (Marker-based selection is equivalent to the old `--ignore=tests/e2e` — same 1130 tests selected.) |

## New defects found in this round

**[MEDIUM] `docs/deployment.md:120-124` still instructs the WRONG owner for the production path.**
The BunkerWeb production quick-start says: "container (which runs as `appuser` UID 100, GID 101) … `chown 100:101 data`". The container now runs as 1000:1000. The handover claims "Fixed legacy `chown 100:101 data` to `sudo chown -R 1000:1000 data`" — only the **standalone** section (line 74) was fixed; this second occurrence was missed, and QA's "INFORMATIONAL: 0 / truthfully resolved" verdict rubber-stamped it. An operator following the production path will chown to 100:101, get a first-boot failure — now at least a self-explaining one thanks to the preflight, but the doc is wrong and the fix claim is only half-true.
**Fix:** one-line edit — line 120-124 → "container (which runs as `appuser` UID 1000, GID 1000)" and `sudo chown -R 1000:1000 data`.

**[INFO] `bin/panel` sha256 differs from a same-source rebuild** (identical size) — benign build nondeterminism (build ID/metadata); recorded for audit completeness, no action needed.

## Round 3 verdict

**REMEDIATION VERIFIED (with one required doc fix before commit).** The recurring evidence-fabrication pattern (MEDIUM-5) is resolved this round — every timing and artifact claim was independently reproduced. The preflight, toolchain pin, and honest metrics are genuine and live-tested. Remaining before merge: correct the stale `chown 100:101` instruction at `docs/deployment.md:120-124`. The preflight mitigates its runtime impact, but shipping operator docs that guarantee a failed first boot for the BunkerWeb path should not be merged as-is.

---

# Round 4 Verification — `deployment_doc_fix` (2026-09-04)

**Claims under test:** `deployment_doc_fix_dev_handover.md` and `deployment_doc_fix_qa.md` ("no stale operational instructions referencing `100:101` or legacy `UID 100` remain", all gates re-run clean).

**Method:** doc diff inspected line-by-line; repository-wide greps re-executed independently (`100:101`, `UID 100`, `GID 101`, `chown 100`); Go-tree staleness check via mtime (`find -newermt 02:05` — zero `.go` files changed since my Round 3 gate runs, so Round 3's verified gate results still describe this tree exactly); compile + gofmt sanity re-run; `.pytest_cache` mtime corroborates QA's claimed pytest execution.

## Result: claims TRUE — fix verified, nothing else snuck in.

| Claim | Verdict | Evidence |
|---|---|---|
| `docs/deployment.md:120-124` corrected | **VERIFIED** | `git diff` shows exactly the specified change: "UID 100, GID 101 → UID 1000, GID 1000" and `chown 100:101 data` → `sudo chown -R 1000:1000 data`. Nothing else changed in the operational sections beyond the Round-3 metrics table (already verified). |
| "Zero remaining operational 100:101/UID 100/GID 101/chown 100 references" | **VERIFIED** | My independent grep matches the handover's audit exactly: remaining hits are only (a) `docs/plans/2026-08-25-go-rewrite.md:411,418` — historical description of the *legacy Python* container, factually correct as history, and (b) `.env.example:36` — the migration warning itself. Zero operational instructions retain stale ownership. |
| Gates re-run clean | **VERIFIED (by proxy + spot re-run)** | No `.go` file changed since Round 3 (mtime check) — my Round 3 full gate runs (race suite 65s/29 pkgs/0 races, govulncheck exit 0, gosec 0, lint clean, pytest 1130) still apply verbatim. QA's claimed durations (race 68.32s, pytest 116.58s, lint 0.81s) are consistent with my measured values, and `.pytest_cache/v/cache/nodeids` was touched at QA's run time — physical corroboration. Spot re-check: `go build` + `gofmt` clean. |

## Residual notes (none blocking)

- **[INFO] `docs/plans/2026-08-25-go-rewrite.md:411,418` still says the Go rewrite requires "Root or CAP_NET_ADMIN"** as the future user model. That's a planning document describing the intended architecture, not deployment instructions — consistent with the staging posture. No action needed; would become relevant only when the VPN endpoint phase activates.
- **[INFO] The pytest count discrepancy is explained and benign:** 1130 tests with `pytest -m "not e2e"` (36 deselected) ≡ the old `--ignore=tests/e2e` selection (the e2e directory holds exactly the 36 marker-tagged tests). Same suite, different selection mechanism.

## Round 4 verdict

**REMEDIATION VERIFIED — CLEAN.** The Round 3 finding is fully and precisely fixed, the "zero stale references" claim is true as scoped (historical/migration mentions are correct to keep), the changeset is doc-only as claimed, and the gate claims are corroborated. No new defects found this round. The full Phase 9 changeset (all four remediation rounds) is now in a state I would hand off: the remaining open items across the review history are (1) the TUN/`CAP_NET_ADMIN` compose-profile gating — a documented-deferral decision, acceptable but worth an explicit issue, and (2) the compose profile gating and CI image-size gate recommendations that were superseded by the documentation-first approach. Recommend creating a follow-up issue to track the VPN activation work (compose profile, `VPN_ENABLED` wiring, portal keypair persistence, `VPN_ENDPOINT_PUBLIC_KEY` consumption) so the staging posture doesn't silently persist.