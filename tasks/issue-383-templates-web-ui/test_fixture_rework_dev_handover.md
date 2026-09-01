# Developer Handover: Test Fixture Rework (Credential Leak Assertion Alignment)

**Task**: `tasks/issue-383-templates-web-ui/test_fixture_rework.md`  
**Issue**: #383  
**Date**: 2026-09-01  
**Author**: dev_bot  

---

## 1. Summary of Changes

In `amnezia-web-ui-go/internal/handlers/template_test.go` within `TestRenderMyConnectionsNoCredentialLeak`:
- Directly asserted `strings.Contains(htmlOutput, sshKeySecret)` in addition to the substring checks (`"dummy-rsa-private-key-data-xyz"`, `"BEGIN RSA PRIVATE KEY"`), ensuring the full private key fixture variable is explicitly tested against the rendered `/my` HTML output.
- Directly asserted `strings.Contains(string(marshaledServer), sshKeySecret)` in addition to the substring checks on the JSON-marshaled `models.Server` struct, confirming `json:"-"` prevents credential serialization.
- Verified `sshKeySecret` fixture contains realistic dummy RSA key data:
  ```go
  sshKeySecret := "-----BEGIN RSA PRIVATE KEY-----\ndummy-rsa-private-key-data-xyz-998877\n-----END RSA PRIVATE KEY-----"
  ```

---

## 2. Updated Code Snippets

```go
// Assert that neither the password, private key, nor password hashes appear anywhere in the rendered output
if strings.Contains(htmlOutput, sshPassSecret) || strings.Contains(htmlOutput, "dummy-ssh-password-secret-12345") {
    t.Fatalf("CRITICAL SECURITY VULNERABILITY: SSH password leaked in /my rendered HTML:\n%s", htmlOutput)
}
if strings.Contains(htmlOutput, sshKeySecret) || strings.Contains(htmlOutput, "dummy-rsa-private-key-data-xyz") || strings.Contains(htmlOutput, "BEGIN RSA PRIVATE KEY") {
    t.Fatalf("CRITICAL SECURITY VULNERABILITY: SSH private key leaked in /my rendered HTML:\n%s", htmlOutput)
}
if strings.Contains(htmlOutput, userPassHashSecret) || strings.Contains(htmlOutput, "dummy-bcrypt-user-password-hash-secret-value-12345") {
    t.Fatalf("CRITICAL SECURITY VULNERABILITY: User password hash leaked in /my rendered HTML:\n%s", htmlOutput)
}
if strings.Contains(htmlOutput, sharePassHashSecret) || strings.Contains(htmlOutput, "dummy-bcrypt-share-password-hash-secret-value-67890") {
    t.Fatalf("CRITICAL SECURITY VULNERABILITY: Share password hash leaked in /my rendered HTML:\n%s", htmlOutput)
}

// Also verify that models.Server JSON marshaling doesn't emit credentials
marshaledServer, err := json.Marshal(srv)
if err != nil {
    t.Fatalf("json.Marshal(Server) failed: %v", err)
}
if strings.Contains(string(marshaledServer), sshPassSecret) || strings.Contains(string(marshaledServer), "dummy-ssh-password-secret-12345") {
    t.Fatalf("models.Server JSON marshaling leaked SSHPass: %s", string(marshaledServer))
}
if strings.Contains(string(marshaledServer), sshKeySecret) || strings.Contains(string(marshaledServer), "dummy-rsa-private-key-data-xyz") || strings.Contains(string(marshaledServer), "BEGIN RSA PRIVATE KEY") {
    t.Fatalf("models.Server JSON marshaling leaked SSHKey: %s", string(marshaledServer))
}
```

---

## 3. Compilation & Gate Verification Results

All required compilation gates executed cleanly in `amnezia-web-ui-go/`:

1. **`go fmt ./...`**: PASS (clean, no files modified)
2. **`go vet ./...`**: PASS (exit code 0)
3. **`go build ./...`**: PASS (exit code 0)
4. **`go test -race -cover ./...`**: PASS (exit code 0, zero data races)
   - `github.com/devops-igor/amnezia-web-ui-go/internal/handlers`: coverage 85.4% of statements
   - `github.com/devops-igor/amnezia-web-ui-go/web`: coverage 100.0% of statements
   - `github.com/devops-igor/amnezia-web-ui-go/internal/router`: coverage 91.5% of statements
   - `github.com/devops-igor/amnezia-web-ui-go/internal/database`: coverage 89.7% of statements
   - `github.com/devops-igor/amnezia-web-ui-go/internal/service`: coverage 93.8% of statements
5. **`golangci-lint run ./...`**: PASS (exit code 0, 0 issues)
6. **`gosec -quiet ./...`**: PASS (exit code 0, 0 issues)
7. **`govulncheck ./...`**: Clean for application code and dependencies (0 application/third-party module vulnerabilities; stdlib toolchain vulnerabilities noted from go1.26.2).

---

## 4. Status

Ready for QA verification.
