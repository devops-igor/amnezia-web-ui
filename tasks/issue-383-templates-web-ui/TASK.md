# Task Specification: Phase 7 — Templates, Static Assets & Web UI

## Issue: #383
**Task Directory**: `tasks/issue-383-templates-web-ui/`
**Target Codebase**: `amnezia-web-ui-go/`

---

## 1. Objective & Scope

Port, harden, and verify the server-side template rendering engine, all 11 HTML web templates, static assets, and internationalization in `amnezia-web-ui-go` to achieve 100% visual, functional, and semantic parity with the legacy Python/FastAPI/Jinja2 implementation.

### Target Deliverables:
1. **Template Engine Architecture (`internal/handlers/template.go`)**:
   - Safe and isolated `html/template` per-page parsing with `base.html` inheritance (using isolated template cloning or dedicated template trees to avoid block definition collision across pages).
   - Rich `FuncMap` containing helpers: `format_bytes`, `format_time`, `json`, `t`, `translate`, `_`, `has_role`, `is_admin`, `safe_html`, `safe_js`, etc.
   - Dynamic locale negotiation (`Cookie: lang`, `Cookie: panel_lang`, query param, appearance settings) and pass-through of `translations_json` and `all_translations_json` to client-side scripts.
   - Resilient rendering with structured error logging, CSRF token injection, session user injection, and appearance settings fallback.
2. **Full Port & Audit of All 11 HTML Templates (`web/templates/`)**:
   - `base.html`: Main layout wrapper, responsive navbar, navigation links, theme switcher, language selector, flash toast notifications, CSRF meta headers, modal infrastructure.
   - `index.html`: Admin dashboard overview, active servers overview cards, quick user stats, VPN status highlights.
   - `server.html`: Server configuration and protocol management (AmneziaWG, MTProxy, AmneziaDNS), service toggle buttons, log viewers, live client stats, and container status.
   - `users.html`: User directory, bulk operations (enable, disable, delete), quota limits, expiry dates, connection provisioning modals, Telegram ID linking, RemnaWave sync indicators.
   - `my_connections.html`: Client self-service portal, protocol download buttons (.conf, qr code, vpn profiles), connection state.
   - `settings.html`: Appearance customizations (logo, title, subtitle), Telegram bot notifications, RemnaWave integration configurations, backup/restore, audit logs.
   - `login.html`: Clean login screen, optional Turnstile / SVG CAPTCHA challenge handling, session remember-me.
   - `setup.html`: Initial setup wizard for first-run administrator creation.
   - `change_password.html`: Self-service password change form with validation.
   - `leaderboard.html`: Traffic consumption leaderboard, monthly snapshot viewer, rankings.
   - `user_share.html`: Temporary/public link sharing page for quick client config download.
3. **Static Assets & Route Serving (`web/static/`, `internal/router/router.go`)**:
   - Embedded static asset file system (`embed.FS`) mounted at `/static/`.
   - Asset routing with proper MIME type detection, cache control headers, and gzip support where appropriate.
4. **Testing & Quality Assurance**:
   - Comprehensive unit and integration tests in `internal/handlers/template_test.go`, `internal/handlers/pages_test.go`, and `web/web_test.go`.
   - Render every single template with edge-case data shapes (empty lists, nil users, complex nested models, long strings, special characters) to verify 0 template execution panics.
   - Statement coverage target $\ge 85.0\%$ on `internal/handlers` and `web`.

---

## 2. Technical Guidelines & Anti-Drift Guardrails

1. **Template Parsing Isolation**:
   - In Go standard `html/template`, executing multiple templates that each define `{{ define "content" }}` on a shared root template causes overwrite races.
   - Each page template must be constructed either by parsing `base.html` together with the target page on an isolated `template.New(name)` instance or via `template.Clone()` during engine startup.
2. **Translation Compatibility**:
   - Both `_("key")` and `t("key")` must resolve against the active request language from `config.T(lang, key)`.
   - Client-side scripts rely on `window.translations` populated from `{{ .translations_json }}`.
3. **CSRF & Security**:
   - Forms must include `<input type="hidden" name="csrf_token" value="{{ .csrf_token }}">`.
   - AJAX requests must be able to read `<meta name="csrf-token" content="{{ .csrf_token }}">`.
   - Strictly avoid unescaped user inputs. Any custom safe HTML helpers must only be used on trusted static content.
4. **Data Race & Concurrency Safety**:
   - `TemplateEngine` must be thread-safe for concurrent read access (`sync.RWMutex`).

---

## 3. Compilation & Quality Gates

The developer (`dev_bot`) MUST satisfy the compilation gate before generating `DEV_HANDOVER.md`:
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

## 4. Expected Output Artifacts

- Implementation in `amnezia-web-ui-go/internal/handlers/`, `amnezia-web-ui-go/web/`, `amnezia-web-ui-go/internal/router/`.
- Handover document strictly at: `tasks/issue-383-templates-web-ui/DEV_HANDOVER.md`.
- State log entry in `WORKLOG.md`.
