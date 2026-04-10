# CI/CD Errors Report

**Generated:** 2026-04-10 10:16 AM
**Checked by:** git_bot

## Current Failures

### Run #24224453446 - Lint (feat/telemt-configurable-path)
**Status:** failure  
**Branch:** feat/telemt-configurable-path  
**Job:** Lint  
**Failure Time:** 2026-04-10T03:14:01Z

**Error Details:**
```
black --check --diff .
would reformat /home/runner/work/amnezia-web-ui/amnezia-web-ui/telemt_manager.py
@@ -176,13 +176,11 @@
         if code != 0:
             return ""
         return out
 
     def save_server_config(self, protocol_type, config_content):
-        self.ssh.upload_file_sudo(
-            config_content.replace("\r\n", "\n"), self._config_path()
-        )
+        self.ssh.upload_file_sudo(config_content.replace("\r\n", "\n"), self._config_path())
         # Use SIGHUP (HUP) to reload MTProxy config without restarting the process/container.
         # This keeps the traffic statistics (octets) in memory.
         self.ssh.run_sudo_command(
             f"docker kill -s HUP {self.CONTAINER_NAME} || docker restart {self.CONTAINER_NAME}"
         )

Oh no! 💥 💔 💥
1 file would be reformatted, 11 files would be left unchanged.
Process completed with exit code 1.
```

**Root Cause:** Black formatting issue in `telemt_manager.py` - `save_server_config()` method has incorrect line breaks in `upload_file_sudo()` call.

**Action Required:** This is on a different branch (`feat/telemt-configurable-path`), not `main`. The failure is pre-existing and unrelated to TASK-05.

---

### Run #24163580155 - Lint (main)
**Status:** failure  
**Branch:** main  
**Job:** Lint  
**Failure Time:** 2026-04-08T23:15:24Z

**Note:** Pre-existing failure on main from April 8th. Needs investigation by pm_bot.

---

## Summary
- 2 failed runs detected
- 1 on unrelated feature branch (feat/telemt-configurable-path)
- 1 pre-existing failure on main (April 8th)
- No failures related to TASK-05 changes
