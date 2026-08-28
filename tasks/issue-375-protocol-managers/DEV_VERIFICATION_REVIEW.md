# Independent Verification Review: TASK-375 (Phase 4 — Protocol Managers: AWG, MTProxyL, DNS)

**Date:** 2026-08-28
**Verifier:** dev_bot (Lead Developer) — independent review
**Scope:** `amnezia-web-ui-go/internal/manager/` (`manager.go`, `awg/`, `awg/cps/`, `awg/tc/`, `awg/health/`, `mtproxyl/`, `dns/`, `ssh/`)
**References:** `TASK.md`, `DEV_HANDOVER.md`, `QA_REVIEW.md`, `docs/plans/2026-08-25-go-rewrite.md` (Phase 4, lines 500–527), `docs/specs/04-external-services.md`, Python reference (`app/managers/awg_manager.py`, `awg_cps.py`, `awg_tc.py`, `awg_health.py`, `mtproxyl_manager.py`).

---

## 1. Executive Summary

**Verdict: APPROVED_WITH_FINDINGS**

I independently re-read every source file under review, re-ran every quality gate myself (not trusting the reported numbers), cross-checked the Go code against the Python ground-truth, and specifically hunted the three known issue #344 Noise IK bug patterns.

The Phase 4 subsystem is a **real, working, well-tested port** — not stubs. All build/vet/test/race/lint/gosec gates pass with exit code 0 and all eight `internal/manager/...` packages meet the ≥85.0% statement-coverage requirement. The three issue #344 AWG bugs are **correctly fixed** in the Go Noise IK implementation. CPS packet crafting is a faithful byte-layout port of the Python reference.

However, verification uncovered **functional gaps relative to the plan's Phase 4 deliverable list and the Python reference surface**, plus **one spec-text deviation**. None of these crash anything or compromise the code that *does* exist, but they are real and should be tracked before closing the issue. Details in §7.

---

## 2. Spec Compliance Checklist (Phase 4 plan, lines 500–527)

| # | Deliverable | Status | Evidence / Notes |
|---|-------------|--------|------------------|
| 4A | AWG remote container lifecycle (`amneziavpn/amneziawg-go`, wg0.conf, clients.json sync) | **PASS** | `awg.go` Install/Uninstall/GetClients/AddClient/RemoveClient/GetClientConfig/ToggleClient; live `awg syncconf`; `awg show all` enrichment. |
| 4A | Keypair & parameter generation (Curve25519, PSK, Jc/Jmin/Jmax, S1-S4, H1-H4) | **PASS** | `params.go` `GenerateWGKeypair`/`GeneratePSK`/`GenerateQuadrantHeaders`/`GenerateAWGParams`; `\|S1−S2\|≥10` enforced; profiles lite/standard/pro. |
| 4A | Binary CPS packet crafting (`gen_quic_initial`, `gen_quic_short`, `gen_dns`, `gen_sip`, `gen_tls`, `to_cps`) | **PASS** | `cps/tls.go`, `cps/quic.go`, `cps/dns.go`, `cps/sip.go`, `cps/cps.go` (`ToCPS`, `ParseCPSBlob`). See §4. |
| 4A | Remote traffic control (`awg_tc`): tc/qdisc/ifb, peer→class-id, bandwidth limits | **PASS** | `tc/tc.go` `SetupIFB`/`SetupQdisc`/`ApplySpeedLimit`/`RemoveSpeedLimit`/`SetGlobalLimit`/`ReapplyAllLimits`/`BuildBatchTCScript`; `PeerToClassID` = last-octet+100. |
| 4A | Client CRUD: `AddClient`, `RemoveClient`, `EditClient`, `ToggleClient`, `RotateMimicry`, `GetClientConfig` | **PARTIAL** | `AddClient`, `RemoveClient`, `ToggleClient`, `GetClientConfig` present on `AWGManager`. **`EditClient` and `RotateMimicry` are absent** (see §7, Finding 1 & 2). |
| 4A | Noise protocol health probes (`awg_health`): port Noise IK to Go, byte-for-byte parity, RTT | **PASS (with a spec-text caveat)** | `health/noise.go` + `health/probe.go`. Cryptographic construction is correct and bug-free (§3). **"byte-for-byte identical to Python" is not verifiable** — see Finding 6. |
| 4A | Speed-limit config: global and per-connection, bulk apply | **PASS** | `tc.SetGlobalLimit`, `tc.ApplySpeedLimit`, `tc.ReapplyAllLimits` (2-phase batch). |
| 4B | MTProxyL remote lifecycle & CLI integration | **PASS** | `mtproxyl.go` Install/Uninstall (host CLI, BunkerWeb 18443 fallback). |
| 4B | Client secret generation, add/edit/remove/toggle clients | **PASS** | `AddClient`/`EditClient`/`RemoveClient`/`ToggleClient` on `MTProxyLManager`. |
| 4B | Quota enforcement & overquota disabling | **PASS** | `stats.go` `DisableOverquotaUsers`. |
| 4C | DNS Unbound container, `forward-records.conf`, health check | **PASS** | `dns/dns.go`, `dns/unbound.go`, `ProbeDNSQuery`. |
| Gate 4 | Golden-file diff testing vs Python baselines | **FAIL** | No golden files / `testdata/` under `internal/manager`. Python baseline diff testing is not implemented. See Finding 6. |
| Gate 4 | Noise protocol byte-for-byte verification (captured Python packet vectors) | **FAIL** | No captured Python vector comparison exists; Go tests are internally self-consistent only. See Finding 6. |
| Gate 4 | Full port of ~400 protocol-manager test cases | **PARTIAL** | 98 `func Test*` across `internal/manager`; coverage ≥85%, but not a literal ~400-case port and no cross-language golden vectors. |

**TASK.md §A.5 interface items:** `Protocol()`, `Install`, `Uninstall`, `GetClients`, `AddClient`, `RemoveClient`, `GetClientConfig` — all present and satisfy `manager.ProtocolManager`. Registration in `manager.Registry` works via `models.NormalizeProtocol`.

---

## 3. Noise IK Protocol Audit (issue #344 bug hunt)

I specifically checked the three bug patterns fixed in Python PR #353:

### Bug 1 — S1 padding position: **FIXED / CORRECT**
- Go (`noise.go:211-240`): builds `msg_body` (116 B), computes MAC1/MAC2, then **prepends** random `S1` junk: `packet = padding || msgBody || mac1 || mac2`. Total = `S1 + 148`.
- Python (`awg_health.py` lines 272-276): `message = msg_body + mac1 + mac2` then `padding + message`. Identical ordering.
- Go test `noise_test.go:89-96` asserts `len == s1+148` and carves `msgBody := initPacket[s1 : s1+116]` — confirming the front-padding layout. ✅

### Bug 2 — MAC1 coverage (116-byte body, not the padded 167): **FIXED / CORRECT**
- Go (`noise.go:218-226`): MAC1 is keyed BLAKE2s-128 with key `BLAKE2s-256("mac1----" || ServerPubKey)` and the data written is **only** `msgBody` (the 116-byte body), *before* any S1 padding. ✅
- Python (`awg_health.py` lines 267-269): MAC1 over `msg_body` only. Identical. ✅

### Bug 3 — Response parsing offset (start from S2, not 0): **FIXED / CORRECT**
- Go (`noise.go:268-273`): `payload := respPacket[s2:]` — skips S2 junk before reading fields. ✅
- Python (`awg_health.py` line 325-326): `offset = s2`, then fields from `packet[offset:]`. Identical. ✅

### Cryptographic correctness — verified line-by-line against spec §6.3/§6.4 and Python
- Constants `INITIAL_CHAIN_KEY`, `INITIAL_HASH`, `LABEL_MAC1="mac1----"` match spec §6.1 and Python `awg_health.py`.
- KDF1/KDF2/KDF3 (HMAC-BLAKE2s) match spec §6.2 exactly.
- Initiation: DH1 (`e_priv × ServerPub`) → AEAD1(static pub) → DH2 (`ClientPriv × ServerPub`) → AEAD2(TAI64N `0x400000000000000A + unix_sec`, nsec masked `& ~0xFFFFFF`). Matches spec and Python. ✅
- Handshake is exercised end-to-end through a real mock UDP server in `TestMockUDPEndpointProbe`, plus tamper/edge negatives (corrupted tag, short packet, nil state, bad key lengths) in `TestAWGHandshakeRoundtrip`. Round-trip passes under `-race`. ✅

### Spec-text deviation discovered (not a Go bug)
The **spec §6.4 field map is off by 4 bytes**: it lists `receiver_idx = payload[4:8]` and `server_e_pub = payload[8:40]`. Both the production Python (`awg_health.py` lines 336-343: `receiver_idx = packet[offset+8:+12]`, `server_e_pub = packet[offset+12:+44]`) **and** the Go (`noise.go:279-285`) actually use the correct wire layout `receiver_idx = payload[8:12]`, `server_e_pub = payload[12:44]`. The Go code follows the working implementation, not the mistaken spec figure. **Recommend correcting §6.4 of the spec** to match (receiver_idx at +8, e_pub at 12:44, encrypted_empty at 44:60). This is a documentation fix, not code.

**Conclusion:** none of the three #344 bugs are present. The Noise IK crypto is trustworthy.

---

## 4. CPS Packet Audit (`cps/tls.go`, `quic.go`, `dns.go`, `sip.go`)

Cross-checked byte layouts against spec §5.1–5.3 and the Python reference (`awg_cps.py`, lines 130-335).

- **TLS 1.3/1.2 ClientHello (`tls.go`):** Record header `0x16 0x03 0x01`, Handshake `0x01`, client version `0x03 0x03`, 32 B random, 32 B session ID, exactly the 16 cipher suites, compression `0x01 0x00`, extensions SNI/supported-groups(x25519/P-256/P-384)/EC-point-formats/sig-algs/versions/key-share(X25519)/ALPN(`h2`,`http/1.1`). Byte-for-byte structurally identical to Python `gen_tls`. ✅ (Minor: `GenTLS(domain)` drops the unused `targetLen` arg present in the TASK signature — no padding behavior existed in Python either, so this is signature drift only, not a behavior bug. Noted in §7 as cosmetic.)
- **QUIC v1 Initial (`quic.go`):** first byte `0xC0`/`0xC3`, version `0x00000001`, 8 B DCID/SCID with length prefixes, zero-length token, length varint `0x4000|plen`, 1–4 B packet number, **target length 216**. Matches Python `gen_quic_initial` byte-for-byte including random (not zero) padding. ⚠️ **Spec §5.2 diverges from both implementations** (spec says "target 1200–1280 random, zero padding, synthetic ClientHello payload"); Python and Go both intentionally produce a compact 216-byte random-padded Initial. The Go port follows the reference implementation, which is the operative ground truth. Noted as a spec-accuracy observation, not a code defect.
- **QUIC Short / 1-RTT (`quic.go`):** fixed bit `0x40`, random spin/key bits, 8 B DCID, 1–4 B PN, 40–90 B payload. Matches Python `gen_quic_short`. ✅
- **DNS query (`dns.go`):** flags `0x0100`, QDCOUNT=1/ARCOUNT=1, QNAME labels capped at 63 B, QTYPE A `0x0001`, QCLASS IN `0x0001`, EDNS0 OPT-RR (UDP size 1232/4096, DO bit 0x0000/0x8000). Caller wraps 2-byte random TXID via `<r 2>`. Matches Python `gen_dns`. ✅
- **SIP (`sip.go`):** ASCII **REGISTER** with random private IP, `z9hG4bK…` branch, Call-ID, CSeq REGISTER, User-Agent pool, `Content-Length: 0`. Matches Python `gen_sip` (which is also REGISTER, not INVITE). ⚠️ TASK.md/spec said "INVITE"; reference uses REGISTER — both Go and Python use REGISTER, so this is a doc/reference naming mismatch only, not a code bug. Noted in §7.
- **CPS blob framing (`cps.go`):** `ToCPS` → `<b 0x%x>`; `ParseCPSBlob` safely handles `<b 0xHEX>` and `<r N><b 0xHEX>` with length-bound check before prefix replacement. ✅
- `GenerateCPSPackets`: lite = DNS I1 (`<r 2>`), standard = QUIC I1, pro = full I1(Initial)+I2..I5(Short) chain; `GenerateConnectionKit` stitches I1-I5 into client `[Interface]`, stripping existing I-keys. Server configs correctly **exclude** I1-I5; client configs include them. ✅

---

## 5. Test Coverage Verification (independently re-run)

I ran the gates myself. Exact output of `go test -count=1 -race -cover ./internal/manager/...`:

| Package | Measured coverage | Target | Result |
|---|---|---|---|
| `internal/manager` | 85.7% | ≥85.0% | PASS |
| `internal/manager/awg` | 86.2% | ≥85.0% | PASS |
| `internal/manager/awg/cps` | 86.2% | ≥85.0% | PASS |
| `internal/manager/awg/health` | 85.2% | ≥85.0% | PASS |
| `internal/manager/awg/tc` | 86.1% | ≥85.0% | PASS |
| `internal/manager/dns` | 87.8% | ≥85.0% | PASS |
| `internal/manager/mtproxyl` | 86.7% | ≥85.0% | PASS |
| `internal/manager/ssh` | 88.6% | ≥85.0% | PASS |

All `ok`, zero failures, race detector clean. **Note:** my measured `internal/manager/awg` = **86.2%** matches `DEV_HANDOVER.md` (86.2%) but QA_REVIEW.md lists 85.9% — the handover number is the accurate one; the QA figure is slightly stale. Minor documentation inconsistency only.

**Test quality (not just the number):** the AWG lifecycle test (`TestAWGManagerLifecycle`) exercises Install→GetServerStatus→GetClients→AddClient→GetClientConfig→ToggleClient→RemoveClient→Uninstall against a mock SSH provider, plus negative paths (nonexistent client, nil server/pool) — this is genuinely end-to-end, not line-ticking. Noise tests include a mock UDP server round-trip and corruption negatives. Traffic control tests cover class-ID math and mock-driven op sequences. This is high-quality coverage.

---

## 6. Security Review

- **Command injection — contained.** MTProxyL usernames are whitelisted to `[a-zA-Z0-9_-]`, ≤32 chars (`mtproxyl.go:25,217-223`) before being passed to `secret add/remove/enable/disable/link/setlimits`. AWG numeric params are bounds-checked in `ValidateAWGParams` (`params.go:385-431`); `i1..i5` must be in `<b 0x…>`/`<r N><b 0x…>` form. File uploads go through SFTP (`UploadSudoFile`) with fixed paths rather than inline shell echoing of secrets.
- **Where unsanitized input CAN reach a shell (acceptable, internal-tooling scope):** the AWG peer-management path interpolates `clientID` (a Curve25519 public key, so `[A-Za-z0-9+/=]`) and peer IPs into `docker exec …` and `tc …` strings. These come from the server's own `clientsTable`/config rather than raw user HTTP input, and IPs pass through `PeerToClassID` and net parsing; risk is low for an admin-side orchestrator. `SelectMimicryDomain` interpolates a probed *domain* into a `/dev/tcp/<domain>/<port>` probe (`cps.go:281`) — domains are drawn from internal pools plus an optional caller-supplied domain; if that domain is attacker-controlled it lands in a remote shell string. Worth a whitelist/regex note, but not a blocker for this phase.
- **Crypto correctness — sound.** Curve25519 via `golang.org/x/crypto/curve25519`, ChaCha20-Poly1305, BLAKE2s; keys generated with `crypto/rand`; TAI64N timestamp computed per spec; MAC1 keyed with `MAC1("mac1----" || server_pub)`. `#nosec G115` annotations are used deliberately on intentional int-width conversions and are consistent with the reference ranges.
- **gosec:** 24 files, 6199 lines audited, **0 issues** (29 `#nosec` acknowledgements), exit 0.
- **golangci-lint:** 0 issues, exit 0.
- **Secrets handling:** `secrets.conf` round-trip is field-preserving and guarded by `sync.RWMutex`; AWG `clientsTable` holds private keys as in the Python design (acceptable for a self-hosted panel that must re-issue configs).

---

## 7. Findings

**Finding 1 — AWG `EditClient` is missing (plan gap).** Phase 4 §4A explicitly lists `EditClient` in the AWG Client CRUD set, and the Python `awg_manager.py` implements `update_client_speed_limit`/`edit_client` and `toggle_client`. The Go `AWGManager` implements `Add/Remove/Toggle/GetClientConfig` but **no `EditClient`** (only `MTProxyLManager` has `EditClient`). Speed limits can only be applied at `AddClient` time or via `tc.SetGlobalLimit`, not edited per-client afterward. *Recommendation:* add `EditClient` (and/or a per-client `UpdateSpeedLimit`) to `AWGManager`, or formally document the deferral.

**Finding 2 — AWG `RotateMimicry` is missing and its HTTP route is a stub.** Phase 4 §4A lists `RotateMimicry`; Python `awg_manager.py:1517` implements `rotate_client_mimicry` (sequence `auto→tls→quic→dns→sip→tls`, persisted to `clientsTable`). The Go `AWGManager` has **no** `RotateMimicry`, yet the Go UI calls it (`web/templates/server.html` `rotateClientMimicry(...)` → `POST /api/servers/{id}/connections/{cid}/rotate-mimicry`) and `internal/router/router.go:244,274` registers that route to `jsonOKHandler` — i.e. **it returns a canned OK without rotating anything**. *Recommendation:* implement `RotateMimicry` on `AWGManager` (regenerate I1-I5 via `cps.GenerateMimicryPackets`, persist `awg_mimicry`, advance the sequence) and wire the route to it; otherwise the rotate button is a silent no-op. This is the most consequential gap.

**Finding 3 — Verification Gate 4 is not actually met (no golden files / cross-language vectors).** The plan mandates (a) golden-file diff testing of generated configs against Python baselines and (b) byte-for-byte Noise packet verification against captured Python packet vectors. **No `testdata/` or golden fixtures exist** under `internal/manager`. The Go handshake is internally self-consistent (client builder ↔ mock server verifier use the same Go KDFs), which proves the *protocol* is coherent but cannot prove *byte parity* with Python. The handover/QA "APPROVED" claims overstate Gate 4. *Recommendation:* add at least one captured Python initiation/response vector test (fixed keys/PSK/indices) to lock cross-language parity, or explicitly scope-cut Gate 4 in the plan before merging.

**Finding 4 — `ParseTraffic` Russian-unit substring hazard (pre-existing, faithfully ported).** `stats.go:60-70` iterates units `Б,КБ,МБ,ГБ,ТБ` and for each does `FindAllStringSubmatch("([\d.]+)\s*<unit>")`. Because the larger Cyrillic units end in `Б`, the pattern for `Б` (and `КБ` inside `МБ`, etc.) can match a value already counted under a larger unit. **The Python reference (`mtproxyl_manager.py:462-465`) has the identical nested-loop algorithm**, so this is faithful porting, not a regression; the bundled tests pass because their chosen values (e.g. `1.96 ГБ + 96.64 ГБ`, `500 КБ + 2.50 МБ`) happen not to trigger the overlap. *Recommendation (both repos):* extract per-direction values with a single anchored regex (e.g. `↓\s*([\d.]+)\s*(ТБ|ГБ|МБ|КБ|Б)\s*↑\s*([\d.]+)\s*(ТБ|ГБ|МБ|КБ|Б)`) instead of per-unit global scans, and add an adversarial unit test (a line whose `МБ`/`КБ` values would double-count under the naive scan). Not a merge blocker.

**Finding 5 — Coverage/table discrepancy in QA_REVIEW.md (documentation nit).** QA lists `internal/manager/awg` at 85.9%; both my run and DEV_HANDOVER.md measure 86.2%. Trivially stale. Also the handover/QA reference `…/awg/wg0.conf` and `…/awg/clients.json`, while the implementation and spec §2 actually use `/opt/amnezia/awg/awg0.conf` and `/opt/amnezia/awg/clientsTable`. Cosmetic doc drift; code is consistent with the live Python layout.

**Finding 6 — Spec-text corrections needed (documentation, not code).** (a) §6.4 response field map is off by 4 (see §3): should be `receiver_idx=payload[8:12]`, `server_e_pub=payload[12:44]`, `encrypted_empty=payload[44:60]`. (b) §5.2/§5.3 describe a 1200–1280 B zero-padded QUIC Initial and a SIP `INVITE`; the reference (and faithful Go port) produce a 216 B random-padded Initial and a SIP `REGISTER`. Align the spec with the implementation that actually works against amneziawg-go.

**Finding 7 — Function-signature drift vs TASK (cosmetic).** TASK.md lists `GenTLS(domain, targetLen)`, `GenQUICInitial(domain, targetLen)`, etc. The Go generators take only `domain` (target lengths are fixed internally), matching the Python signatures. The extra `targetLen` parameter exists only in TASK text. Harmless, but the TASK should be corrected to avoid implying tunable lengths.

---

## 8. Final Verdict

**APPROVED_WITH_FINDINGS.**

The Phase 4 codebase is a genuine, high-quality, well-tested port. All quality gates pass when I run them personally (build, vet, `go test -race -cover`, golangci-lint 0 issues, gosec 0 issues), all eight packages exceed the 85% coverage floor, the three issue #344 Noise IK bugs are verifiably fixed, and the CPS byte layouts faithfully mirror the Python reference.

The approval is **conditional on tracking (not necessarily blocking on) the findings above**:
- The two **functional gaps** (Finding 1 `EditClient`, Finding 2 `RotateMimicry` + stubbed route) are the items most likely to bite in production because the UI already exposes a working-looking "rotate" action that silently does nothing.
- The **overstated Verification Gate 4** (Finding 3) should either be implemented (golden vectors) or explicitly scoped down — it is currently claimed as satisfied but is not.
- The spec corrections (Findings 4–7) are documentation hygiene and one pre-existing (Python-inherited) parsing hazard; they do not block the merge.

If Findings 1–3 are scheduled into a follow-up issue before closing #375, the implementation is safe to merge as-is.
