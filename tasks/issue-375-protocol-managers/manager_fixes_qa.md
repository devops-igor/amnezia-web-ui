# QA Audit Report: Protocol Manager Improvements & Gap Fixes (`manager_fixes_qa.md`)

**Date**: 2026-08-28  
**Auditor**: qa_bot (Quality Gatekeeper)  
**Verdict**: **APPROVED**  
**Task Specification**: [`tasks/issue-375-protocol-managers/manager_fixes.md`](file:///home/igor/Amnezia-Web-Panel/tasks/issue-375-protocol-managers/manager_fixes.md)  
**Dev Handover**: [`tasks/issue-375-protocol-managers/manager_fixes_dev_handover.md`](file:///home/igor/Amnezia-Web-Panel/tasks/issue-375-protocol-managers/manager_fixes_dev_handover.md)  
**Verification Reference**: [`tasks/issue-375-protocol-managers/DEV_VERIFICATION_REVIEW.md`](file:///home/igor/Amnezia-Web-Panel/tasks/issue-375-protocol-managers/DEV_VERIFICATION_REVIEW.md)  

---

## 1. Executive Summary

An independent, rigorous quality audit was conducted on the Protocol Manager improvements and gap remediation for AmneziaWG, MTProxyL, and AmneziaDNS under Issue #375.

All findings and functional gaps from `DEV_VERIFICATION_REVIEW.md` have been fully resolved with clean cross-language parity, high test coverage, zero static/security analysis findings, and 0 data races. Specifically:
- **AWG Client CRUD & Mimicry**: [`EditClient`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/manager/awg/awg.go#L874-L924) and [`RotateMimicry`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/manager/awg/awg.go#L966-L1029) are implemented with complete parameter updates, Linux TC speed-limit synchronization, CPS $I1-I5$ packet header regeneration, and persistent serialization to `/opt/amnezia/awg/clientsTable`.
- **Noise IK Cryptographic Standard Parity**: [`HMACBlake2s`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/manager/awg/health/noise.go#L45-L52) is implemented using standard RFC 2104 HMAC-BLAKE2s-256 (`crypto/hmac` + `blake2s.New256(nil)`), matching Python's `hmac.new(..., hashlib.blake2s)`. Captured Python golden test vectors in [`noise_test.go`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/manager/awg/health/noise_test.go#L45-L270) verify byte-for-byte wire packet assembly, chaining keys, hash states, and response decryption.
- **MTProxyL Directional Parser Hardening**: [`ParseTraffic`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/manager/mtproxyl/stats.go#L34-L78) uses an anchored single-pass directional regex (`↓\s*([\d.]+)\s*(ТБ|ГБ|МБ|КБ|Б)\s*↑\s*([\d.]+)\s*(ТБ|ГБ|МБ|КБ|Б)`), completely eliminating Cyrillic unit substring collision vulnerabilities. Adversarial test cases verify resilience against mixed units.
- **Specification Accuracy**: [`docs/specs/04-external-services.md`](file:///home/igor/Amnezia-Web-Panel/docs/specs/04-external-services.md) was updated to accurately specify §6.4 response wire offsets, §6.2 HMAC-BLAKE2s standard implementation, §5.2 QUIC 216-byte initial format, and §5.3 SIP REGISTER format.

---

## 2. Quality Gate & Compilation Audit Table

| Gate | Execution Command | Result | Findings |
|---|---|---|---|
| **Code Formatting** | `go fmt ./...` | **PASS** | 0 diffs / 0 unformatted files |
| **Go Vet** | `go vet ./...` | **PASS** | 0 warnings (exit code 0) |
| **Go Build** | `go build ./...` | **PASS** | Clean build (exit code 0) |
| **Go Unit & Race Tests** | `go test -count=1 -race -cover ./internal/manager/...` | **PASS** | 0 failures, 0 data races |
| **Static Linter** | `golangci-lint run ./...` | **PASS** | 0 issues |
| **Security Audit** | `gosec ./...` | **PASS** | 0 security issues |
| **Python Regression Suite** | `pytest tests/test_*.py -q` | **PASS** | 1130 passed, 0 failures |

---

## 3. Package Statement Coverage Matrix (`internal/manager/...`)

All eight manager packages exceed the $\ge 85.0\%$ coverage requirement:

| Package Path | Measured Coverage | Target Requirement | Status |
|---|---|---|---|
| `internal/manager` | **85.7%** | $\ge 85.0\%$ | **PASS** |
| `internal/manager/awg` | **86.6%** | $\ge 85.0\%$ | **PASS** |
| `internal/manager/awg/cps` | **85.6%** | $\ge 85.0\%$ | **PASS** |
| `internal/manager/awg/health` | **85.5%** | $\ge 85.0\%$ | **PASS** |
| `internal/manager/awg/tc` | **86.1%** | $\ge 85.0\%$ | **PASS** |
| `internal/manager/dns` | **87.8%** | $\ge 85.0\%$ | **PASS** |
| `internal/manager/mtproxyl` | **87.1%** | $\ge 85.0\%$ | **PASS** |
| `internal/manager/ssh` | **88.6%** | $\ge 85.0\%$ | **PASS** |

---

## 4. Detailed Technical Verification of Gaps & Fixes

### A. Finding 1: AWG `EditClient` & TC Speed Limit Synchronization
- Verified implementation in [`awg.go:L874-L924`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/manager/awg/awg.go#L874-L924):
  - Modifies `clientName`, `enabled` status, `awg_mimicry`, and bandwidth limits (`speed_limit_down`, `speed_limit_up`).
  - Correctly toggles the peer entry in `awg0.conf` via `updateServerConfigPeer`.
  - Invokes `tc.ApplySpeedLimit` when limits $>0$ or `tc.RemoveSpeedLimit` when cleared/zero via `syncClientTC`.
  - Atomically persists the updated client record to `/opt/amnezia/awg/clientsTable`.
  - Unit tests in `TestAWGManager_EditClient` cover property updates, limit clears, enable/disable toggling, and missing client error handling.

### B. Finding 2: AWG `RotateMimicry` & CPS Packet Generation
- Verified implementation in [`awg.go:L966-L1029`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/manager/awg/awg.go#L966-L1029):
  - Strictly follows the rotation sequence `auto -> tls -> quic -> dns -> sip -> tls`.
  - Regenerates $I1-I5$ packet headers via `cps.GenerateMimicryPackets`.
  - Enriches client `userData` with `rotated_at` timestamp (RFC 3339 UTC) and persists to `clientsTable`.
  - Unit tests in `TestAWGManager_RotateMimicry` assert full cycling across all protocols.

### C. Finding 3: Noise IK Cryptographic Standard Parity & Captured Golden Vectors
- Standard RFC 2104 HMAC-BLAKE2s-256 implementation:
  - [`HMACBlake2s`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/manager/awg/health/noise.go#L45-L52) correctly wraps `blake2s.New256(nil)` inside Go standard library `crypto/hmac.New`.
- Python Golden Vectors verified in [`noise_test.go:L45-L270`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/manager/awg/health/noise_test.go#L45-L270):
  - Tested with deterministic Curve25519 static/ephemeral keypairs, PSK, and TAI64N timestamp.
  - Golden Initiation packet byte-for-byte exact match with Python reference.
  - Intermediate chaining keys (`ck`) and hash states (`h`) verified at each step.
  - Server-side static public key and timestamp decryption verified.
  - Golden Handshake Response packet fully verified with `VerifyAWGResponsePacket`, and negative tamper bit-flip test passes.

### D. Finding 4: MTProxyL `ParseTraffic` Directional Regex Hardening
- Single-pass directional regex in [`stats.go:L29-L78`](file:///home/igor/Amnezia-Web-Panel/amnezia-web-ui-go/internal/manager/mtproxyl/stats.go#L29-L78):
  - `↓\s*([\d.]+)\s*(ТБ|ГБ|МБ|КБ|Б)\s*↑\s*([\d.]+)\s*(ТБ|ГБ|МБ|КБ|Б)` matches both download and upload metrics in a single anchored pass.
  - Prevents substring matching of single-character units (`Б`) inside multi-character units (`МБ`, `ГБ`, `ТБ`, `КБ`).
  - Adversarial unit tests in `TestParseTraffic_AdversarialUnits` prove immunity to byte count corruption across all Cyrillic unit combinations.

### E. Findings 5 & 6: Specification Text Corrections
- Updated [`docs/specs/04-external-services.md`](file:///home/igor/Amnezia-Web-Panel/docs/specs/04-external-services.md):
  - §6.4 response wire offsets: `msg_type = payload[0:4]`, `sender_idx = payload[4:8]`, `receiver_idx = payload[8:12]`, `server_e_pub = payload[12:44]`, `encrypted_empty = payload[44:60]`, `MAC1 = payload[60:76]`, `MAC2 = payload[76:92]`.
  - §6.2 code block updated with standard RFC 2104 `crypto/hmac` `HMACBlake2s`.
  - §5.2 updated with 216-byte compact QUIC Initial packet layout.
  - §5.3 updated with SIP REGISTER layout.

---

## 5. Security & Static Analysis Summary

- **`golangci-lint run ./...`**: 0 issues reported.
- **`gosec ./...`**: 51 files, 14,040 lines scanned, 0 issues reported.
- **Data Races**: Zero data races reported under `go test -race` across all packages.
- **Command Injection**: Remote execution parameters remain strictly whitelisted, validated, and quoted.
- **Memory & Crypto Safety**: Curve25519, ChaCha20-Poly1305, and BLAKE2s primitives are used per RFC specifications without unauthenticated paths.

---

## 6. Audit Verdict

**APPROVED**

All requirements of `manager_fixes.md` and `DEV_VERIFICATION_REVIEW.md` are completely satisfied. The Phase 4 Protocol Managers subsystem is production-ready, cryptographically robust, and fully verified.
