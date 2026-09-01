# Sub-Task Specification: Credential Leak Assertion Fixture Alignment

**Issue**: #383
**Task File**: `tasks/issue-383-templates-web-ui/test_fixture_rework.md`
**Target Codebase**: `amnezia-web-ui-go/`

---

## 1. Objective

In `internal/handlers/template_test.go` (`TestRenderMyConnectionsNoCredentialLeak`), directly assert `strings.Contains(htmlOutput, sshKeySecret)` and `strings.Contains(string(marshaledServer), sshKeySecret)` so that the private key variable `sshKeySecret` is explicitly verified against both the rendered HTML and JSON marshaling, eliminating any vacuous assertion paths.

---

## 2. Requirements

1. In `internal/handlers/template_test.go` within `TestRenderMyConnectionsNoCredentialLeak`:
   - Ensure `sshKeySecret` is set to a realistic dummy key (e.g. `"-----BEGIN RSA PRIVATE KEY-----\ndummy-rsa-private-key-data-xyz-998877\n-----END RSA PRIVATE KEY-----"`).
   - Assert directly:
     ```go
     if strings.Contains(htmlOutput, sshKeySecret) || strings.Contains(htmlOutput, "dummy-rsa-private-key-data-xyz") || strings.Contains(htmlOutput, "BEGIN RSA PRIVATE KEY") {
         t.Fatalf("CRITICAL SECURITY VULNERABILITY: SSH private key leaked in /my rendered HTML:\n%s", htmlOutput)
     }
     ```
   - Assert directly on `marshaledServer`:
     ```go
     if strings.Contains(string(marshaledServer), sshKeySecret) || strings.Contains(string(marshaledServer), "dummy-rsa-private-key-data-xyz") || strings.Contains(string(marshaledServer), "BEGIN RSA PRIVATE KEY") {
         t.Fatalf("models.Server JSON marshaling leaked SSHKey: %s", string(marshaledServer))
     }
     ```
2. Satisfy all Go compilation gates (skipping Python tests):
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
3. Write developer handover strictly to:
   `tasks/issue-383-templates-web-ui/test_fixture_rework_dev_handover.md`
   and update `WORKLOG.md`.
