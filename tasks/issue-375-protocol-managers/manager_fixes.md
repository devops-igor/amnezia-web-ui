# Sub-Task Specification: Protocol Manager Improvements & Gap Fixes (`manager_fixes.md`)

## Objective
Address the findings identified in [`DEV_VERIFICATION_REVIEW.md`](./DEV_VERIFICATION_REVIEW.md) to close all functional gaps, harden parsers against unit collisions, add captured Python golden test vectors, and correct specification text.

---

## 1. Scope of Fixes

### A. Finding 1: AWG `EditClient` & `UpdateSpeedLimit` (`internal/manager/awg/awg.go`)
- Implement `EditClient(ctx context.Context, server *models.Server, clientID string, params map[string]any) error`:
  - Support modifying client parameters (e.g. `name`, `limits`, `speed_limit_down`, `speed_limit_up`, `enabled`).
  - If speed limits change, invoke `tc.ApplySpeedLimit` / `tc.RemoveSpeedLimit`.
  - Update and persist `/opt/amnezia/awg/clientsTable`.

### B. Finding 2: AWG `RotateMimicry` (`internal/manager/awg/awg.go`)
- Implement `RotateMimicry(ctx context.Context, server *models.Server, clientID string) (string, error)`:
  - Sequence: `auto` -> `tls` -> `quic` -> `dns` -> `sip` -> `tls` (cycling through protocols).
  - Regenerate `I1-I5` packet headers using `cps.GenerateMimicryPackets(nextProfile)`.
  - Update `clientsTable` with new mimicry profile and CPS headers.
  - Re-render client `.conf` / connection bundle.

### C. Finding 3: Captured Python Golden Vectors for Noise IK Handshake (`internal/manager/awg/health/`)
- Add captured Python test vectors for Noise IK initiation and response packets with fixed test keys:
  - Fixed client static keypair, server static keypair, PSK, ephemeral keys, H1/H2, S1/S2, and TAI64N timestamp.
  - Assert that Go handshake packet generation and verification match expected raw bytes and decrypt cleanly.

### D. Finding 4: MTProxyL `ParseTraffic` Directional Regex Hardening (`internal/manager/mtproxyl/stats.go`)
- Replace the nested per-unit substring scan with an anchored single-pass directional regex:
  - Format: `↓\s*([\d.]+)\s*(ТБ|ГБ|МБ|КБ|Б)\s*↑\s*([\d.]+)\s*(ТБ|ГБ|МБ|КБ|Б)`
  - Prevents single-letter `Б` or `КБ` from inadvertently matching inside larger units like `МБ` or `ГБ`.
  - Add adversarial test cases in `stats_test.go`.

### E. Finding 5 & 6: Specification Text Correction (`docs/specs/04-external-services.md`)
- Update §6.4 response wire layout offsets: `receiver_idx = payload[8:12]`, `server_e_pub = payload[12:44]`, `encrypted_empty = payload[44:60]`.
- Update §5.2 & §5.3 text to accurately reflect the 216-byte QUIC Initial and SIP REGISTER formats.

---

## 2. Compilation & Verification Gate (Hard Rule)
All of the following must pass with exit code 0:
- `go fmt ./... && go vet ./... && go build ./...`
- `go test -count=1 -race -cover ./internal/manager/...` (Coverage $\ge 85.0\%$ across all packages)
- `golangci-lint run ./...` (0 issues)
- `gosec ./...` (0 issues)
- `pytest tests/test_*.py -q` (1130 passed)

---

## 3. Handover Output
- Output your report to `tasks/issue-375-protocol-managers/manager_fixes_dev_handover.md`.
- Append status to `WORKLOG.md`.
