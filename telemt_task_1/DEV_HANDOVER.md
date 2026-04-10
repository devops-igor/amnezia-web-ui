# DEV_HANDOVER: Telemt proxy links default port fix

## Files Changed

### 1. telemt_manager.py (line 78)
**Change:** Default parameter `port="443"` → `port="18443"`

```diff
     def install_protocol(
         self,
         protocol_type="telemt",
-        port="443",
+        port="18443",
         tls_emulation=True,
```

**Rationale:** This is the default external port passed through the API when installing the Telemt protocol. Changing this ensures new installations use port 18443.

### 2. templates/server.html (line 818)
**Change:** `portInput.value = '443'` → `portInput.value = '18443'`

```diff
         } else if (proto === 'telemt') {
             portLabel.textContent = _('port') + ' (TCP)';
-            portInput.value = '443';
-            portHint.textContent = 'Порт для Telegram-прокси (обычно 443)';
+            portInput.value = '18443';
+            portHint.textContent = 'Порт для Telegram-прокси (обычно 18443)';
             telemtOpts.style.display = 'block';
```

**Rationale:** This sets the default port value in the UI when the user selects the Telemt protocol. Without this change, the UI would override the server-side default.

### 3. protocol_telemt/config.toml (line 28)
**Change:** `public_port = 443` → `public_port = 18443`

```diff
 # public_host = "proxy.example.com"  # Host (IP or domain) for tg:// links
-public_port = 443                  # Port for tg:// links (default: server.port)
+public_port = 18443                  # Port for tg:// links (default: server.port)
```

**Rationale:** This is the template default for the `public_port` setting used in `tg://proxy` link generation. It gets patched during install, but the raw template should reflect the correct default.

## Smoke Test Results

```
$ python3 -m py_compile telemt_manager.py
SMOKE TEST PASSED
```

Exit code: 0

## Full Git Diff

```diff
diff --git a/protocol_telemt/config.toml b/protocol_telemt/config.toml
index b0b91f4..9c9eabb 100644
--- a/protocol_telemt/config.toml
+++ b/protocol_telemt/config.toml
@@ -25,7 +25,7 @@ show = "*"
 # show = ["alice", "bob"] # Only show links for alice and bob
 # show = "*"              # Show links for all users
 # public_host = "proxy.example.com"  # Host (IP or domain) for tg:// links
-public_port = 443                  # Port for tg:// links (default: server.port)
+public_port = 18443                  # Port for tg:// links (default: server.port)
 
 # === Server Binding ===
 [server]
diff --git a/telemt_manager.py b/telemt_manager.py
index 793f074..879cdf6 100644
--- a/telemt_manager.py
+++ b/telemt_manager.py
@@ -75,7 +75,7 @@ class TelemtManager:
     def install_protocol(
         self,
         protocol_type="telemt",
-        port="443",
+        port="18443",
         tls_emulation=True,
         tls_domain="",
         max_connections=0,
diff --git a/templates/server.html b/templates/server.html
index 9193383..23b4e15 100644
--- a/templates/server.html
+++ b/templates/server.html
@@ -815,8 +815,8 @@
             portHint.textContent = _('port_xray_hint');
         } else if (proto === 'telemt') {
             portLabel.textContent = _('port') + ' (TCP)';
-            portInput.value = '443';
-            portHint.textContent = 'Порт для Telegram-прокси (обычно 443)';
+            portInput.value = '18443';
+            portHint.textContent = 'Порт для Telegram-прокси (обычно 18443)';
             telemtOpts.style.display = 'block';
```

## Issues Encountered

- None. All three changes applied cleanly.
- `python` command not found; used `python3` instead.

## Notes for QA

- **Existing installations:** Servers already deployed with port 443 retain their stored port in `server["protocols"]["telemt"]["port"]`. This change only affects new installations.
- **Internal ports unchanged:** The internal container port (443) in `docker-compose.yml`, `Dockerfile`, and `config.toml` line 32 (`port = 443`) remains unchanged. These are correct.
- **Edge case:** Users manually entering a custom port in the UI will still work as expected — the default is just a starting value.
