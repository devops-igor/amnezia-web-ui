# Sub-Task: Phase 9 Deployment Documentation Fix (`deployment_doc_fix.md`)

## 1. Context & Objective
In `docs/deployment.md` lines 120-124 (Step 2 — Prepare Config Directory under BunkerWeb deployment), a legacy occurrence of `UID 100, GID 101` and `chown 100:101 data` remains. This must be corrected to match the Go container's `appuser` (UID 1000, GID 1000) and `sudo chown -R 1000:1000 data`.

---

## 2. Required Changes

### 2.1 `docs/deployment.md`
- Update lines 120-124 from:
  ```markdown
  Create a data directory for panel persistence and set ownership so the
  container (which runs as `appuser` UID 100, GID 101) can write to it:

  ```bash
  mkdir -p data
  chown 100:101 data
  ```
- To:
  ```markdown
  Create a data directory for panel persistence and set ownership so the
  container (which runs as `appuser` UID 1000, GID 1000) can write to it:

  ```bash
  mkdir -p data
  sudo chown -R 1000:1000 data
  ```

---

## 3. Verification & Handover
1. Verify no other stale references to `100:101` remain in operational deployment steps.
2. Run standard sanity check (`go test ./...` / markdown formatting).
3. Emit handover report to:
   `tasks/issue-389-docker-packaging/deployment_doc_fix_dev_handover.md`
