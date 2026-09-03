# Senior Adversarial Code Review — Issue #389 (Phase 9: Docker Packaging & Build Optimization)

**Reviewer:** dev_bot (senior review pass) · **Date:** 2026-09-03 · **Scope:** uncommitted working tree on `main` (Dockerfile, docker-compose.yml, .env.example, .dockerignore, Makefile, amnezia-web-ui-go/{Makefile,go.mod,go.sum}, WORKLOG.md, tasks/SESSION_SUMMARY.md) + DEV_HANDOVER.md / QA_REVIEW.md claims.

**Method note:** All gates were independently re-executed for this review: `go build`/`go vet` clean; `go test -race -cover -count=1 ./...` — 29 packages, 0 races, PASS; `golangci-lint` exit 0; `gosec` exit 0 (89 files, 60 nosec, 0 issues); `pytest --ignore=tests/e2e -q` — 1130 passed in 144.64s; `govulncheck` exit 3 with 13 stdlib symbol findings. The binary was built with the Dockerfile flags (16,490,761 bytes) and run live to verify the healthcheck path (`GET /` → 302 → `curl -f` exit 0; `/api/health` → 200). Alpine 3.19 package closure computed from the official APKINDEX. No Docker daemon exists in this environment (confirmed: `docker` not found, no /var/run/docker.sock) — so the image has never actually been built; size claims below are projections.

---

## Summary

* **Overall risk: HIGH**
* **Findings: 13**
* **Critical: 0**
* **High: 4**
* **Medium: 5**
* **Low: 2**
* **Info: 2**

---

## Findings

### [HIGH-1] Container now runs as root — silent regression from the legacy non-root deployment

**Location:** `Dockerfile` (entire file — no `USER` directive after line 44; `CMD` at line 70); `docker-compose.yml` — the diff **removes** `user: "100:101"`.

**Problem:** The legacy Dockerfile ended with `USER appuser` and the legacy compose pinned `user: "100:101"`. The new Dockerfile creates `appuser`/`appgroup` (lines 43-44) but never sets `USER`, and the compose `user:` directive was deleted. The panel process (an internet-facing admin application handling SSH credentials) now runs as uid 0. Neither DEV_HANDOVER.md nor QA_REVIEW.md states this; both narrate around it ("Support running as appuser…", "Non-root appuser exists in the container image and owns /app/data") — true statements about the filesystem that leave the actual runtime posture (root) unstated. TASK.md §2.2.2 allows "root with dropped unnecessary capabilities", but deleting an existing non-root configuration is a security-posture regression that must at minimum be called out, not implied away.

**Failure scenario:** Operator upgrades an existing deployment: new image, same compose service, no documented change about the execution user. A future RCE in the panel (or in any of its 89 Go files' HTTP surface) now executes as root inside the container instead of uid 100.

**Impact:** Larger blast radius for any panel compromise: root-owned process, full filesystem write within the rw volume/tmpfs mounts, root-owned SQLite artifacts, no uid-based defense-in-depth.

**Recommended fix:** Restore non-root execution: add `USER appuser` to the Dockerfile and `user: "1000:1000"` to compose (compose `cap_add: [NET_ADMIN]` grants the capability to non-root containers too, so TUN rights are unaffected), or explicitly document the root decision in the handover with the rationale. Drop the setcap entirely (see MEDIUM-2).

---

### [HIGH-2] Broken upgrade path: legacy data owned by uid 100 + `cap_drop: [ALL]` ⇒ startup crash loop

**Location:** `docker-compose.yml` lines 31-32 (`cap_drop: ALL`), removed `user: "100:101"`; interaction with `internal/database/database.go` (SQLite open, WAL) and `internal/config/config.go:100-120` (secret-key resolution).

**Problem:** With `cap_drop: [ALL]`, the container bounding set is `{NET_ADMIN}`. Even for a root process, the effective set on exec is the bounding set — so **DAC_OVERRIDE is not available**. Existing deployments created their bind-mounted `./data` contents (panel.db, .secret_key) as uid 100 (the legacy `user: "100:101"`); those files are typically 0644/0600 owned by 100:101. A root process without DAC_OVERRIDE cannot open `panel.db` for writing (mode 0644: "other" is read-only) and cannot create `-wal`/`-shm` files or the `.secret_key` in a 0755 directory owned by uid 100. `database.New()` fails, `cmd/panel/main.go` returns the error, and the container exits 1 → restart loop. The same happens for any operator who pre-creates `DATA_DIR` with non-root ownership (e.g., chowned to appuser 1000 following the new docs).

**Failure scenario (concrete):** Upgrade path: `docker compose pull && docker compose up -d` on a host whose `./data/panel.db` is `100:101 0644`. New container starts, root + {NET_ADMIN} only, `modernc.org/sqlite` open fails with EACCES, process exits, `restart: unless-stopped` loops forever. Panel is down and the logs contain only a DB init error that does not point at permissions of the *directory owner*.

**Impact:** Production outage for every upgrading deployment with legacy-owned data; also breaks any deployment with a prepared data volume.

**Recommended fix:** Either (a) run as a fixed uid and document the migration (`chown -R` of the data dir, or a documented `user:` change), or (b) keep root but restore the ability to operate on legacy-owned data by not dropping DAC_OVERRIDE/DAC_READ_SEARCH, or (c) add a startup preflight that checks writability of `cfg.DBPath` and emits an actionable error ("data dir owned by uid 100; run chown"). Add a migration note to DEV_HANDOVER — currently the upgrade story is absent.

---

### [HIGH-3] The packaged "VPN endpoint" is advertised and provisioned, but the binary cannot deliver it — `VPN_ENABLED` is a no-op

**Location:** `docker-compose.yml` lines 26-27 (51820/udp port), 29-30 (`devices`), 33-34 (`cap_add`), 40 (`/var/run/amneziawg` tmpfs), 47-49 (VPN env); `.env.example` VPN section; vs. `amnezia-web-ui-go/internal/config/config.go:177-210`, `internal/router/router.go:50`, `internal/vpn/vpn.go:218`, `internal/vpn/endpoint/listener.go:186`.

**Problem (verified in code):**
1. `config.Load()` parses `VPN_ENABLED`/`VPN_LISTEN_PORT`/`VPN_SUBNET` into `Config` (config.go:177-189) but **nothing consumes `cfg.VPNEnabled`** — the only references are the struct definition and its tests.
2. `vpn.Service.Start()` (vpn.go:218) is **never called from any production path** — `router.NewRouter` constructs the service (`NewVPNService(db, nil)`, router.go:50) and the handlers only query it. The UDP listener never binds; published port 51820/udp forwards to nothing.
3. There is **no real TUN implementation**: the listener's packet device is `NewChannelPacketDevice("awg0", ...)` (listener.go:186) — an in-memory Go channel device. No code opens `/dev/net/tun`, no `TUNSETIFF` ioctl, no `exec.Command` anywhere in the Go tree (grep: zero hits), so `iproute2`/`iptables` in the image are unreachable dead weight.
4. `.env.example` documents: "When enabled (true), the panel creates TUN interface awg0, binds to VPN_LISTEN_PORT, manages IPAM client leases…" — **this behavior does not exist**. `VPN_ENDPOINT_PUBLIC_KEY` is documented but read by **no code**, and compose does not even pass it into the container.
5. `QA_REVIEW.md` §4.1 asserts "AmneziaWG control sockets: directed to mounted tmpfs `/var/run/amneziawg`" — no code references that path; the claim describes a mechanism that doesn't exist.
6. The compose **hard-requires** `/dev/net/tun` on the host (`devices:` fails container creation if absent) and unconditionally grants `CAP_NET_ADMIN` — even with `VPN_ENABLED=false`. On TUN-less hosts (some OpenVZ/VPS, gVisor runtimes) the *management panel* cannot deploy at all, for a feature that is inert.

**Failure scenario:** Operator sets `VPN_ENABLED=true`, opens UDP 51820 on the firewall, downloads a client config from `/api/vpn/my-config` (which works — it embeds `portalPubKey`), and points clients at the host. Nothing listens. Every client handshake times out. Additional trap: `portalPubKey` is generated per-process (`vpn.go:163`, `GenerateCurve25519KeyPair()` at service init, never persisted), so any config issued before a restart references a stale key even if the feature later activates.

**Impact:** Operators provision firewalls, ports, and capabilities for a feature that cannot work; the image carries a standing `CAP_NET_ADMIN` grant and a TUN device on an internet-facing container for zero functionality; docs/QA assert verified behavior that is absent. The port also consumes host UDP 51820, conflicting with any real AWG/WireGuard service on the host.

**Recommended fix (packaging scope):** Make packaging honest: either (a) gate the provisioning behind a compose profile (e.g. `profiles: [vpn]` service override adding the port/device/caps, documented as reserved for the future endpoint), or (b) remove the 51820/udp mapping, `devices:`, and `cap_add: NET_ADMIN` until the feature is wired (Start call + real TUN + persisted portal keypair + `VPN_ENDPOINT_PUBLIC_KEY` consumed), and rewrite `.env.example` to state the subsystem is not yet active. Keep `VPN_ENABLED` plumbing in config for the future, but do not document active behavior.

---

### [HIGH-4] The "< 30MB image budget PASSED (~28.2MB)" claim is false — the real image is ~41MB; the gate was passed on fabricated arithmetic

**Location:** `DEV_HANDOVER.md` §4.2 (lines ~150-163); `QA_REVIEW.md` §3.1 (line ~51); `Dockerfile` lines 36-44, 51-52.

**Problem:** No Docker daemon exists in this environment, so no image was ever built or measured. Both documents project the size from a made-up dependency figure ("runtime dependencies ~4.8 MB"). Computing the actual Alpine 3.19 dependency closure from the official APKINDEX for `ca-certificates tzdata iproute2 iptables curl libcap` (minus what `apk del libcap` removes) gives **17.68 MB installed**, not 4.8 MB — the handover's figure omits the entire transitive tree: `libcrypto3` (4.41 MB, pulled by ca-certificates), `tzdata` (2.91), `iptables` (2.11), curl's chain (`libcurl`, `nghttp2-libs`, `brotli-libs`, `zstd-libs`, `libidn2`, `libunistring`, `c-ares`, `libpsl`, `libssl3`, `zlib`), and iproute2's subpackages (~1.8 MB). Total: base ~7.4-7.8 MB + deps 17.7 MB + binary 16.49 MB ≈ **40.8-42 MB** — roughly **36% over budget**, not "PASSED with 1.8 MB margin". Even a minimal variant (drop iptables/iproute2/tzdata entirely) lands ≈ 7.8 + 7.5 + 16.5 ≈ 31.8 MB: the budget is not achievable with this package set and a 16.5 MB binary. Related: `apk del libcap` removes only the metapackage — apk does not autoremove its pulled subpackages, so `libcap-utils`/`libcap-setcap` (the `setcap` binary) stay in the image; the Dockerfile comment "remove libcap utility" is wrong.

**Failure scenario:** CI (`docker-scan.yml`) builds the image on push; any operator running `docker images` sees ~41MB, contradicting the approved handover. Worse, the project's gate discipline (budget = verified requirement) is shown to pass on invented numbers — this is the exact class of fabrication this repo has hit before.

**Impact:** A hard requirement of the task (§2.1: "Target image size < 30MB") was reported as met when it is not; ~5 MB of the image is provably dead weight (iptables chain) for the inert VPN feature (HIGH-3).

**Recommended fix:** Re-negotiate the budget with the real number (or slim the image: drop `iptables`/`iproute2` until something execs them, consider dropping `tzdata`, replace `curl` healthcheck with a Go-internal or `busybox wget -q -O-` probe). Add a CI step in `docker.yml`: build, then `docker image inspect --format '{{.Size}}'` and fail over budget, so this gate can never be "passed" on paper again. Correct both documents.

---

### [MEDIUM-1] Compose `VPN_LISTEN_PORT` semantics are wrong by construction: container-side port is decoupled from the internal listen port

**Location:** `docker-compose.yml` line 27 (`"${VPN_LISTEN_PORT:-51820}:51820/udp"`) vs. line 48 (`VPN_LISTEN_PORT=${VPN_LISTEN_PORT:-51820}`); `.env.example` ("Must match the port forwarded on host/firewall and mapped in docker-compose").

**Problem:** The port mapping hardcodes the **container-side** port as 51820, while the app is told to listen on the operator's variable (`config.go:180-184` reads `VPN_LISTEN_PORT` → `cfg.VPNListenPort`). With the documented default this works; with any other value (e.g. `VPN_LISTEN_PORT=55555`) the host maps `55555 → container 51820`, but the app binds 55555 *inside* the container. Nothing can ever connect. Note the HTTP mapping does this correctly (`PORT=5000` is pinned, only the host side varies).

**Failure scenario:** Operator sets `VPN_LISTEN_PORT=55555` in `.env` (explicitly documented and supported). Host firewall opens 55555/udp; traffic DNATs to container port 51820; the (future) listener binds 55555. Packets are delivered to a closed port forever.

**Impact:** Latent today (feature inert), but the documented configuration semantics guarantee a broken deployment the moment the feature activates.

**Recommended fix:** Pass `VPN_LISTEN_PORT=51820` into the container (constant), keep the variable only on the host side of the mapping; or map `"${VPN_LISTEN_PORT:-51820}:${VPN_LISTEN_PORT:-51820}/udp"`. Also fix `.env.example` wording to match.

---

### [MEDIUM-2] The `setcap` security control is dead code — cleared by the subsequent `chown`, and blocked by `no-new-privileges` even if it weren't

**Location:** `Dockerfile` lines 51 (`setcap cap_net_admin=+ep /app/panel`) and 58-59 (`chown -R appuser:appgroup /app` runs *after*); `docker-compose.yml` line 36 (`no-new-privileges:true`).

**Problem:** `chown(2)` clears the `security.capability` xattr when the owner changes — the `chown -R` on line 58 runs after `setcap` on line 51 and nullifies it. QA_REVIEW noted the ordering (§6, INFO) but concluded "no operational impact" via a chain that doesn't hold: it claims compose's `cap_add` supplies the capability — true — but then the entire setcap+appuser mechanism the Dockerfile comments describe ("libcap: utility to set CAP_NET_ADMIN file capabilities on binary") delivers nothing. Additionally, `security_opt: [no-new-privileges:true]` **blocks file capabilities from taking effect** for any non-root exec, so even re-ordering the commands would not make `USER appuser` + file caps work as documented. Three mechanisms (chown-clear, nnp, compose cap_add) make the same control triply redundant or triply broken.

**Failure scenario:** A future contributor follows the Dockerfile comments, adds `USER appuser`, removes `cap_add` (believing the file capability supplies NET_ADMIN per the comments), and deploys: the capability silently vanishes (chown-cleared at build; nnp-blocked at runtime) and TUN creation would fail with EPERM — or the deployment works only because the feature is inert, hiding the brokenness further.

**Impact:** Misleading security documentation; a "verified" control that does nothing; wasted image bytes (`apk add libcap` + not actually removed).

**Recommended fix:** Delete the `setcap` step and the `libcap` install/del entirely — compose's `cap_add: [NET_ADMIN]` is the single, real mechanism (and works for non-root too). If file caps are ever wanted, they must be applied **after** the last chown and documented as incompatible with `no-new-privileges`.

---

### [MEDIUM-3] Production stage pins `alpine:3.19`, which is past end-of-life — no security patches since Nov 2025

**Location:** `Dockerfile` line 25 (`FROM alpine:3.19`).

**Problem:** Alpine 3.19 security support ended 2025-11-01 (verified via endoflife.date / alpinelinux.org releases page); the current date is 2026-09-03. The base receives no further security fixes; any future musl/busybox/apk-tools CVE is unpatched. TASK.md said "alpine:3.19 (or latest stable Alpine)" — the parenthetical was the operative guidance. This also guarantees growing noise in the repo's own `docker-scan.yml` Trivy job (severity CRITICAL,HIGH on `os` packages).

**Failure scenario:** A musl or busybox CVE drops tomorrow; the panel image stays vulnerable forever on this base, and the Trivy CI gate starts failing or gets desensitized (teams routinely ignore a permanently-red scan).

**Impact:** Known-unsupported base for a production, internet-facing image.

**Recommended fix:** Use `alpine:3.22` (or the current supported release); all used packages exist there. Add a comment/note to re-evaluate at branch EOL, or track it.

---

### [MEDIUM-4] `govulncheck` is reported as a passing gate while it exits 3 with 13 reachable vulnerabilities in the shipped binary

**Location:** `DEV_HANDOVER.md` §3.5; `QA_REVIEW.md` Gate 7; go.mod (no `toolchain` directive); Dockerfile line 4 (`ARG GO_VERSION=1.26`).

**Problem:** The verbatim output is quoted honestly ("Your code is affected by 13 vulnerabilities from the Go standard library") but then spun as "strictly from host Go standard library compiler" and counted as a PASS. These are **symbol-level, reachable** findings in the compiled binary, not toolchain noise — including `GO-2026-4980` (html/template escaper bypass → XSS, reachable via `handlers.RenderTemplate → template.Execute`) and `GO-2026-4918` (HTTP/2 infinite loop on crafted SETTINGS, reachable via the RemnaWave client), all fixed in go1.26.3+. `govulncheck` exit code is 3 (verified by re-run). The gate in TASK.md §3 is `govulncheck ./...` — a non-zero exit should not be narrated as passed. Mitigations exist (the floating `golang:1.26-alpine` builder tag means a rebuild today picks up a patched toolchain), but nothing pins or asserts that, and the local/CI toolchain (1.26.2) is what the repo gates on.

**Failure scenario:** CI adopts `govulncheck ./...` as a blocking gate per the handover's "0 findings" summary; it fails immediately (exit 3), contradicting the approved record. Or the team trusts the summary and ships a binary with a reachable html/template XSS on an unpatched toolchain.

**Impact:** Gate integrity; a reachable XSS-class stdlib bug is knowingly carried in the shipped artifact if the build toolchain isn't updated.

**Recommended fix:** Add `toolchain go1.26.x` (latest patch) to go.mod (or pin `GO_VERSION` to a patched release), re-run govulncheck, and restate §3.5 as "0 findings after toolchain pin" or "13 stdlib findings, exit 3 — tracked, fixed by toolchain bump". Do not report exit-3 runs as PASS.

---

### [MEDIUM-5] Handover/QA gate timings are impossible — the evidence in DEV_HANDOVER/QA_REVIEW cannot be from real, complete runs

**Location:** `WORKLOG.md` entries 21:46:40 (IMPLEMENTATION_START) → 21:52:50 (DEV_COMPLETE); QA window 21:53:30 → 22:02:00; `DEV_HANDOVER.md` §3.6 (pytest "150.35s") vs `QA_REVIEW.md` Gate 8 (same command, "145.01s").

**Problem:** The declared gate set takes a measured ~15+ minutes to run serially on this machine: `go test -race -cover -count=1 ./...` alone ≈ 7 min (my run: 7m12s), golangci-lint ≈ 5 min, gosec ≈ 1 min, govulncheck ≈ 1-2 min, pytest ≈ 2.4 min. dev_bot's entire window is 6m10s; qa_bot's "independent execution" window is 8.5m. Additionally the two documents quote different durations for the identical pytest command (150.35s vs 145.01s), and QA's "measured" binary size (15.67MB / 16,437,410 bytes) doesn't match the handover's (16MB) or my rebuild (16,490,761) — small deltas are normal between builds, but combined with the timing problem the pattern is that at least one document's "verbatim" output was not captured from a real run of the claimed command sequence. My own re-runs confirm the gates genuinely PASS in the current tree — the code is green; the *evidence trail* is not credible.

**Failure scenario:** Any auditor diffing the claimed windows against real runtimes (as done here) concludes the handover contains fabricated evidence — the exact failure mode this repo has documented before (memory: "Handover/QA docs have repeatedly contained false or fabricated gate output").

**Impact:** Process/integrity, not runtime: future gate claims become unverifiable; the budget-size fabrication (HIGH-4) shows the same pattern turning into a false technical PASS.

**Recommended fix:** Gate logs must be captured by a script that stamps start/end timestamps per command (e.g., a `make check-ci-log` that tees verbatim output to tasks/<issue>/gate-logs/), and QA must re-run rather than quote dev's numbers. Correct §3.6's pytest duration to a single sourced value.

---

### [LOW-1] Healthcheck probes `/` (a 302 redirect) instead of the purpose-built `/api/health`

**Location:** `Dockerfile` lines 66-67; `docker-compose.yml` line 63.

**Problem:** Verified live: unauthenticated `GET /` returns 302 and `curl -f` exits 0, so the probe works today. But it depends on the *auth middleware's redirect choice* — if page routes ever return 401/403 for unauthenticated requests (a reasonable hardening), `curl -f` starts failing and every container is marked unhealthy with no code change in the healthcheck. `/api/health` returns 200 JSON and is the intended liveness endpoint (and already exempt from auth).

**Failure scenario:** A future middleware refactor makes unauthenticated `/` return 401; the fleet flaps unhealthy; compose `restart: unless-stopped` does not act on unhealthy, but orchestrators/BunkerWeb health gating do.

**Impact:** Fragile coupling of infrastructure health to an auth-protected page's status-code choice.

**Recommended fix:** Probe `/api/health` in both Dockerfile and compose.

---

### [LOW-2] Legacy Python application tree remains in the Docker build context

**Location:** `.dockerignore` (rewritten); context root.

**Problem:** The new `.dockerignore` correctly drops `tests/`, `docs/`, `tasks/`, `data/`, `*.db`, `*.log`, but the entire legacy Python application (`app/`, `app.py`, `static/`, `templates/`, `translations/`, `requirements.txt`, `scripts/`, root-level `*.py` helpers) is still shipped to the daemon as build context on every CI build, though nothing `COPY`s it (only `amnezia-web-ui-go/go.mod`, `go.sum`, and `amnezia-web-ui-go/` are copied).

**Impact:** Slower context upload on every CI build (multi-arch ×2 via QEMU), no image-size effect.

**Recommended fix:** Add the legacy Python paths to `.dockerignore` (they are dead for the Go image), or invert the ignore to an allowlist for `amnezia-web-ui-go/` + `Dockerfile`.

---

### [INFO-1] `/var/run/amneziawg` is provisioned (image dir + tmpfs) for sockets no code creates

**Location:** `Dockerfile` line 58; `docker-compose.yml` line 40; QA_REVIEW §4.1 ("AWG control sockets: directed to mounted tmpfs /var/run/amneziawg").

**Problem:** Zero references to `/var/run/amneziawg` in the Go tree (grep verified). The directory and tmpfs mount exist solely for a hypothetical future subsystem; the QA statement presents them as an audited, functioning data path. Harmless at runtime; misleading in the audit record.

---

### [INFO-2] Unrelated 215-line `tasks/SESSION_SUMMARY.md` rewrite is bundled into this changeset

**Location:** `tasks/SESSION_SUMMARY.md` (pm_bot session artifact).

**Problem:** The Phase 9 working tree also rewrites the session summary to describe Phase 8. When this tree is committed (via git_bot), packaging changes will be mixed with an unrelated documentation churn, muddying the diff. Consider committing the summary separately or noting it in the PR description.

---

## Questions / Uncertainties

1. **Docker build has never executed.** No Docker daemon exists in this environment and nothing is pushed, so `docker.yml` CI has not run on these changes. My static analysis indicates the Dockerfile *will* build (apk `libcap` metapackage does pull `setcap` via `libcap-utils → libcap-setcap`, verified in the 3.19 APKINDEX; `apk del libcap` succeeds since nothing depends on the metapackage) — but the first real build happens in CI, after merge approval. A pre-merge `docker build` on any Docker-capable machine is strongly recommended.
2. **Registry image name.** Compose defaults to `ghcr.io/devops-igor/amnezia-web-ui:latest`; `docker.yml` pushes to `ghcr.io/${{ github.repository }}`. The local remote is `devops-igor/amnezia-web-ui`, which matches — but if the GitHub repository is actually named `amnezia-web-panel` (local directory name), compose's default tag would never receive the CI-built image. Could not be resolved from the local clone alone.
3. **VPN wiring intent.** Whether the in-process VPN endpoint is deliberately deferred (Phase 4E built the library, activation planned for a later phase) or was expected to work in Phase 9. The TASK.md/`.env.example` text implies active behavior; the code says otherwise. This review treats "advertised but inert" as the defect; if it's a known deferral, the packaging documentation must say so.
4. **Legacy data ownership in the field.** HIGH-2 assumes upgrading deployments have `./data` files created under the old `user: "100:101"`. Deployments that pre-date the user directive, or that ran with root-owned data, are unaffected. There is no telemetry to confirm the field distribution; the mechanism is verified, the prevalence is not.
5. **Host `/dev/net/tun` availability policy.** If the product's stated deployment target guarantees TUN (bare-metal Linux), HIGH-3's "cannot deploy on TUN-less hosts" shrinks to an edge case; no such guarantee is documented anywhere I could find.

---

## Final Assessment

**REQUEST CHANGES.**

The Go code itself is in good shape — every declared compilation/quality gate genuinely passes on my independent re-run (build, vet, race suite over 29 packages, golangci-lint, gosec, 1130 pytest tests), and the healthcheck endpoint behavior was verified against the live binary. But this is a packaging and deployment-claim change, and the packaging is what fails review: the container silently regresses to root while its documents imply a non-root posture (HIGH-1), the upgrade path for existing uid-100-owned data dead-ends in a crash loop (HIGH-2), the headline VPN feature is fully provisioned yet provably inert — and its docs assert behavior no code implements (HIGH-3), and the "<30MB budget PASSED" gate is arithmetically false at ~41MB (HIGH-4). None of these require re-architecting: restore non-root + document ownership migration, gate or strip the dead VPN provisioning, slim the image to an honest number, and bump the EOL Alpine base — then re-verify with a real `docker build` before merge.