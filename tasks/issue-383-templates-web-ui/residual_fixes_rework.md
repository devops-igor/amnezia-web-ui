# Sub-Task Specification: Phase 7 Residual Fixes & Verification Remediation

**Issue**: #383
**Task File**: `tasks/issue-383-templates-web-ui/residual_fixes_rework.md`
**Source Review**: `tasks/issue-383-templates-web-ui/CODE_REVIEW_FIXES_VERIFICATION.md`
**Target Codebase**: `amnezia-web-ui-go/`

---

## 1. Objective & Scope

Fix all 3 confirmed regressions and test discrepancies identified during the verification review of the Phase 7 rework (`tasks/issue-383-templates-web-ui/CODE_REVIEW_FIXES_VERIFICATION.md`).

---

## 2. Detailed Remediation Requirements

### Regression A [HIGH] — Restore `server_id` Context Variable in `ServerPageHandler`
- **Location**: `internal/handlers/pages.go:42-64` (`ServerPageHandler`), `web/templates/server.html:567`
- **Problem**: The rework omitted `"server_id": serverID` (or `serverIDStr`) from the template data map, causing `const SERVER_ID = {{ .server_id }};` in `server.html` to render as `const SERVER_ID = null;`, breaking all AJAX calls on `/server/{id}`.
- **Fix**:
  1. Restore `"server_id": serverID` (or `serverIDStr` / `fmt.Sprintf("%d", serverID)`) in the data map passed to `RenderTemplate`.
  2. In `internal/handlers/pages_test.go` (`TestPageHandlers/ServerPageHandler`), assert that the rendered HTML response contains `const SERVER_ID = 1;` (or the seeded server's ID), rather than only checking HTTP status 200.

### Regression B [MEDIUM] — Fix Index-Based `ServerName` Assignment in `MyConnectionsPageHandler`
- **Location**: `internal/handlers/pages.go:132-137`
- **Problem**: `for _, c := range conns` mutates a value copy of `models.UserConnection`, so `ServerName` assignments are discarded and `initialConnections` renders with empty/omitted `server_name`.
- **Fix**:
  1. Change loop to index-based mutation:
     ```go
     for i := range conns {
         if sClean, ok := serversMap[conns[i].ServerID]; ok {
             conns[i].ServerName = sClean.Name
         } else if conns[i].ServerID > 0 {
             conns[i].ServerName = fmt.Sprintf("Server #%d", conns[i].ServerID)
         }
     }
     ```
  2. In `internal/handlers/pages_test.go` (`TestPageHandlers/MyConnectionsPageHandler`), assert that the rendered HTML contains the enriched server name in `initialConnections` or the server-rendered connection card.

### Regression C [LOW] — Restore Strict `parseServerID` in `ServerPageHandler`
- **Location**: `internal/handlers/pages.go:45-48`
- **Problem**: `fmt.Sscanf(serverIDStr, "%d", &serverID)` accepts partial garbage like `12abc` and `1e2`.
- **Fix**: Revert to the strict helper `parseServerID(r)` which uses `strconv.ParseInt` with strict parsing and error handling.

### Minor Performance & Test Enhancements:
1. **`NegotiateLocale` Cheap-First Evaluation**:
   - In `internal/handlers/template.go`, check query parameters (`?lang=`) and cookies (`lang`, `panel_lang`) before querying database appearance settings, ensuring zero unnecessary DB queries when locale is supplied by request.
2. **Credential Leak Test Fixture Fidelity**:
   - In `internal/handlers/template_test.go` (`TestRenderMyConnectionsNoCredentialLeak`), ensure the test fixture contains realistic dummy credentials (e.g. `dummy-ssh-password-secret-12345` and `dummy-rsa-private-key-data-xyz`) and assert that neither substring appears anywhere in the rendered output.

---

## 3. Compilation & Hard Quality Gates

NOTE: Skip Python tests (pytest, flake8, etc.) during this development cycle — work is strictly on the Go rewrite (`amnezia-web-ui-go`).

You MUST satisfy all Go compilation gates:
```bash
cd amnezia-web-ui-go
go fmt ./...
go vet ./...
go build ./...
go test -race -cover ./...
golangci-lint run ./...
gosec -quiet ./...
govulncheck ./...
```

---

## 4. Required Handover Document

- Write developer handover strictly to:
  `tasks/issue-383-templates-web-ui/residual_fixes_rework_dev_handover.md`
- Append `DEV_REWORK` start and completion logs to `WORKLOG.md`.
- Do NOT run `git commit` or `git push`.
