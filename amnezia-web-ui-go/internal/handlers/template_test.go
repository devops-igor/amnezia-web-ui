package handlers

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestTemplateEngineAndHelpers(t *testing.T) {
	_, db, _ := setupTestHandlers(t)

	t.Run("FormatBytes", func(t *testing.T) {
		byteTests := []struct {
			input any
			want  string
		}{
			{0, "0 B"},
			{int64(0), "0 B"},
			{nil, "0 B"},
			{500, "500 B"},
			{1024, "1.00 KB"},
			{1536, "1.50 KB"},
			{1048576, "1.00 MB"},
			{1073741824, "1.00 GB"},
			{1099511627776, "1.00 TB"},
			{1125899906842624, "1.00 PB"},
			{1152921504606846976, "1.00 EB"},
			{-1024, "-1.00 KB"},
			{-1073741824, "-1.00 GB"},
			{uint64(2048), "2.00 KB"},
			{float64(1048576), "1.00 MB"},
			{"1048576", "1.00 MB"},
			{json.Number("1048576"), "1.00 MB"},
		}

		for _, tt := range byteTests {
			got := FormatBytes(tt.input)
			if got != tt.want {
				t.Errorf("FormatBytes(%v) = %q, want %q", tt.input, got, tt.want)
			}
		}
	})

	t.Run("FormatTime", func(t *testing.T) {
		if got := FormatTime(time.Time{}); got != "" {
			t.Errorf("expected empty string for zero time, got %q", got)
		}
		if got := FormatTime(nil); got != "" {
			t.Errorf("expected empty string for nil, got %q", got)
		}
		fixedTime := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
		if got := FormatTime(fixedTime); got != "2026-08-28 12:00:00" {
			t.Errorf("FormatTime failed: %q", got)
		}
		if got := FormatTime(&fixedTime); got != "2026-08-28 12:00:00" {
			t.Errorf("FormatTime ptr failed: %q", got)
		}
		if got := FormatTime("2026-08-28T12:00:00Z"); got != "2026-08-28 12:00:00" {
			t.Errorf("FormatTime ISO string failed: %q", got)
		}
		if got := FormatTime("2026-08-28"); got != "2026-08-28 00:00:00" {
			t.Errorf("FormatTime date string failed: %q", got)
		}
	})

	t.Run("CleanReferer", func(t *testing.T) {
		refTests := []struct {
			input string
			want  string
		}{
			{"", "/"},
			{"/my", "/my"},
			{"/server/1?tab=1", "/server/1?tab=1"},
			{"http://evil.com/hack", "/hack"},
			{"https://example.com/dashboard?view=grid", "/dashboard?view=grid"},
			{"://invalid-url", "/"},
			{"//evil.com/hack", "/"},
		}
		for _, tt := range refTests {
			got := CleanReferer(tt.input)
			if got != tt.want {
				t.Errorf("CleanReferer(%q) = %q, want %q", tt.input, got, tt.want)
			}
		}
		if got := CleanReferer("", "/default"); got != "/default" {
			t.Errorf("CleanReferer with default failed, got %q", got)
		}
	})

	t.Run("FuncMap Helpers", func(t *testing.T) {
		fm := TemplateFuncMap()

		// Safe helpers
		safeHTML := fm["safe_html"].(func(any) template.HTML)
		if safeHTML("<b>test</b>") != "<b>test</b>" {
			t.Error("safe_html failed")
		}

		safeJS := fm["safe_js"].(func(any) template.JS)
		if safeJS("var x = 1;") != "var x = 1;" {
			t.Error("safe_js failed")
		}

		safeCSS := fm["safe_css"].(func(any) template.CSS)
		if safeCSS("color: red;") != "color: red;" {
			t.Error("safe_css failed")
		}

		safeAttr := fm["safe_attr"].(func(any) template.HTMLAttr)
		if safeAttr("disabled") != "disabled" {
			t.Error("safe_attr failed")
		}

		safeURL := fm["safe_url"].(func(any) template.URL)
		if safeURL("/path") != "/path" {
			t.Error("safe_url failed")
		}

		// JSON helpers
		jsonFn := fm["json"].(func(any) template.JS)
		if string(jsonFn(map[string]string{"a": "b"})) != `{"a":"b"}` {
			t.Error("json helper failed")
		}

		tojsonFn := fm["tojson"].(func(any) template.JS)
		if string(tojsonFn("test")) != `"test"` {
			t.Error("tojson helper failed")
		}

		// Translation helpers
		tFn := fm["t"].(func(...any) string)
		if tFn("nav_servers") == "" {
			t.Error("t helper failed")
		}
		if tFn() != "" {
			t.Error("t empty args failed")
		}

		underFn := fm["_"].(func(...any) string)
		if underFn("nav_servers") == "" {
			t.Error("_ helper failed")
		}

		transFn := fm["translate"].(func(...any) string)
		if transFn("nav_servers") == "" {
			t.Error("translate helper failed")
		}

		// Role / Admin helpers
		hasRoleFn := fm["has_role"].(func(any, ...string) bool)
		uAdmin := &models.User{Role: models.RoleAdmin}
		uUser := &models.User{Role: models.RoleUser}
		uSupport := &models.User{Role: models.RoleSupport}
		sessAdmin := &models.SessionData{Role: models.RoleAdmin}
		mapAdmin := map[string]any{"role": "admin"}

		if !hasRoleFn(uAdmin, "admin") {
			t.Error("expected hasRole admin true")
		}
		if !hasRoleFn(uSupport, "support") {
			t.Error("expected hasRole support true")
		}
		if hasRoleFn(uUser, "admin") {
			t.Error("expected hasRole user admin false")
		}
		if !hasRoleFn(sessAdmin, "admin") {
			t.Error("expected hasRole sess admin true")
		}
		if !hasRoleFn(mapAdmin, "admin") {
			t.Error("expected hasRole map admin true")
		}
		if hasRoleFn(nil, "admin") {
			t.Error("expected hasRole nil false")
		}

		isAdminFn := fm["is_admin"].(func(any) bool)
		if !isAdminFn(uAdmin) {
			t.Error("expected isAdmin true")
		}
		if isAdminFn(uUser) {
			t.Error("expected isAdmin false")
		}
		if !isAdminFn(sessAdmin) {
			t.Error("expected isAdmin sess true")
		}
		if !isAdminFn(mapAdmin) {
			t.Error("expected isAdmin map true")
		}
		if isAdminFn(nil) {
			t.Error("expected isAdmin nil false")
		}

		// Proto title
		protoTitleFn := fm["proto_title"].(func(any) string)
		if protoTitleFn("awg") != "AmneziaWG" {
			t.Errorf("proto_title(awg) = %q", protoTitleFn("awg"))
		}
		if protoTitleFn("telemt") != "MTProxyL" {
			t.Errorf("proto_title(telemt) = %q", protoTitleFn("telemt"))
		}
		if protoTitleFn("dns") != "AmneziaDNS" {
			t.Errorf("proto_title(dns) = %q", protoTitleFn("dns"))
		}
		if protoTitleFn("unknown") != "UNKNOWN" {
			t.Errorf("proto_title(unknown) = %q", protoTitleFn("unknown"))
		}

		// is_installed
		isInstalledFn := fm["is_installed"].(func(any) bool)
		if !isInstalledFn(true) {
			t.Error("is_installed(true) failed")
		}
		if isInstalledFn(false) {
			t.Error("is_installed(false) failed")
		}
		if !isInstalledFn(map[string]any{"installed": true}) {
			t.Error("is_installed(map) failed")
		}
		if isInstalledFn(nil) {
			t.Error("is_installed(nil) failed")
		}

		// get helper
		getFn := fm["get"].(func(any, any, ...any) any)
		testMap := map[string]any{"key1": "val1", "nested": map[string]any{"key2": "val2"}}
		if getFn(testMap, "key1") != "val1" {
			t.Error("get(map, key1) failed")
		}
		if getFn(testMap, "missing", "default") != "default" {
			t.Error("get(map, missing, default) failed")
		}
		if getFn(nil, "key", "def") != "def" {
			t.Error("get(nil, key, def) failed")
		}
		type TestStruct struct {
			Name string
		}
		s := TestStruct{Name: "Amnezia"}
		if getFn(s, "Name") != "Amnezia" {
			t.Error("get(struct, Name) failed")
		}

		// String helpers
		upperFn := fm["upper"].(func(any) string)
		if upperFn("amnezia") != "AMNEZIA" {
			t.Error("upper failed")
		}

		lowerFn := fm["lower"].(func(any) string)
		if lowerFn("AMNEZIA") != "amnezia" {
			t.Error("lower failed")
		}

		titleFn := fm["title"].(func(any) string)
		if titleFn("amnezia panel") != "Amnezia Panel" {
			t.Error("title failed")
		}

		containsFn := fm["contains"].(func(any, any) bool)
		if !containsFn("amnezia", "nez") {
			t.Error("contains failed")
		}

		hasPrefixFn := fm["has_prefix"].(func(any, any) bool)
		if !hasPrefixFn("amnezia", "am") {
			t.Error("has_prefix failed")
		}

		hasSuffixFn := fm["has_suffix"].(func(any, any) bool)
		if !hasSuffixFn("amnezia", "zia") {
			t.Error("has_suffix failed")
		}

		trimFn := fm["trim"].(func(any) string)
		if trimFn("  amnezia  ") != "amnezia" {
			t.Error("trim failed")
		}

		replaceFn := fm["replace"].(func(any, any, any) string)
		if replaceFn("hello world", "world", "amnezia") != "hello amnezia" {
			t.Error("replace failed")
		}

		// Math / Logic helpers
		defaultFn := fm["default"].(func(any, any) any)
		if defaultFn("def", "") != "def" {
			t.Error("default(def, '') failed")
		}
		if defaultFn("def", "val") != "val" {
			t.Error("default(def, 'val') failed")
		}

		ternaryFn := fm["ternary"].(func(bool, any, any) any)
		if ternaryFn(true, "yes", "no") != "yes" {
			t.Error("ternary(true) failed")
		}
		if ternaryFn(false, "yes", "no") != "no" {
			t.Error("ternary(false) failed")
		}

		addFn := fm["add"].(func(any, any) int64)
		if addFn(5, 3) != 8 {
			t.Error("add failed")
		}

		subFn := fm["sub"].(func(any, any) int64)
		if subFn(10, 4) != 6 {
			t.Error("sub failed")
		}

		mulFn := fm["mul"].(func(any, any) int64)
		if mulFn(3, 4) != 12 {
			t.Error("mul failed")
		}

		divFn := fm["div"].(func(any, any) int64)
		if divFn(12, 3) != 4 {
			t.Error("div failed")
		}
		if divFn(12, 0) != 0 {
			t.Error("div by zero failed")
		}

		modFn := fm["mod"].(func(any, any) int64)
		if modFn(10, 3) != 1 {
			t.Error("mod failed")
		}
		if modFn(10, 0) != 0 {
			t.Error("mod by zero failed")
		}

		seqFn := fm["seq"].(func(int, int) []int)
		seq := seqFn(1, 4)
		if len(seq) != 4 || seq[0] != 1 || seq[3] != 4 {
			t.Errorf("seq failed: %v", seq)
		}
		if len(seqFn(5, 2)) != 0 {
			t.Error("seq inverted range failed")
		}

		dictFn := fm["dict"].(func(...any) (map[string]any, error))
		d, err := dictFn("k1", "v1", "k2", 42)
		if err != nil || d["k1"] != "v1" || d["k2"] != 42 {
			t.Errorf("dict failed: %v, %v", d, err)
		}
		if _, err := dictFn("odd"); err == nil {
			t.Error("dict odd args should fail")
		}
		if _, err := dictFn(123, "val"); err == nil {
			t.Error("dict non-string key should fail")
		}

		sliceStrFn := fm["slice_str"].(func(any, int, ...int) string)
		if sliceStrFn("2026-08-28 12:00:00", 0, 10) != "2026-08-28" {
			t.Errorf("slice_str failed: %q", sliceStrFn("2026-08-28 12:00:00", 0, 10))
		}
		if sliceStrFn("short", 2) != "ort" {
			t.Errorf("slice_str single arg failed: %q", sliceStrFn("short", 2))
		}
		if sliceStrFn("short", -5, 100) != "short" {
			t.Errorf("slice_str out-of-bounds failed: %q", sliceStrFn("short", -5, 100))
		}
		if sliceStrFn("short", 5, 2) != "" {
			t.Errorf("slice_str start > end failed: %q", sliceStrFn("short", 5, 2))
		}

		intEqFn := fm["int_eq"].(func(any, any) bool)
		if !intEqFn(42, "42") || intEqFn(42, 43) {
			t.Error("int_eq failed")
		}

		strEqFn := fm["str_eq"].(func(any, any) bool)
		if !strEqFn("amnezia", "amnezia") || strEqFn("a", "b") {
			t.Error("str_eq failed")
		}

		strFn := fm["str"].(func(any) string)
		if strFn(123) != "123" {
			t.Error("str failed")
		}

		intFn := fm["int"].(func(any) int64)
		if intFn("456") != 456 {
			t.Error("int failed")
		}
	})

	// Render all 11 HTML templates with realistic payloads
	templates := []string{
		"base.html",
		"login.html",
		"index.html",
		"users.html",
		"server.html",
		"settings.html",
		"my_connections.html",
		"setup.html",
		"change_password.html",
		"leaderboard.html",
		"user_share.html",
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	reqCtx := middleware.WithSession(req.Context(), &models.SessionData{
		UserID:   "admin-1",
		Username: "admin",
		Role:     models.RoleAdmin,
	})
	reqCtx = middleware.WithCSRFToken(reqCtx, "csrf-test-token")
	reqWithCtx := req.WithContext(reqCtx)

	testServer := &models.Server{
		ID:      1,
		Name:    "Test Server",
		Host:    "192.168.1.100",
		SSHPort: 22,
		SSHUser: "root",
		Protocols: map[string]any{
			"awg": map[string]any{
				"installed":         true,
				"container_running": true,
				"port":              55424,
				"clients_count":     5,
			},
			"telemt": map[string]any{
				"installed":         true,
				"container_running": false,
				"container_exists":  true,
				"port":              443,
			},
			"dns": map[string]any{
				"installed": false,
			},
		},
	}

	testConnection := models.UserConnection{
		ID:             "conn-1",
		UserID:         "admin-1",
		ServerID:       1,
		Protocol:       "awg",
		Name:           "My AWG Connection",
		AWGMimicry:     "tls",
		TrafficTotalRx: 104857600,
		TrafficTotalTx: 52428800,
		CreatedAt:      time.Now(),
	}

	sampleData := map[string]any{
		"server_id":         "1",
		"server":            testServer,
		"servers":           []*models.Server{testServer},
		"users":             []models.User{{ID: "u1", Username: "user1", Role: models.RoleUser}},
		"connections":       []models.UserConnection{testConnection},
		"settings":          map[string]any{"appearance": map[string]any{"title": "Amnezia Test", "language": "en"}},
		"share_user":        &models.User{ID: "share-u", Username: "shared_user"},
		"need_password":     false,
		"period":            "all-time",
		"current_user_rank": 1,
		"entries": []models.LeaderboardEntry{
			{Rank: 1, Username: "admin", Download: 1000000, Upload: 500000, Total: 1500000},
			{Rank: 2, Username: "user2", Download: 200000, Upload: 100000, Total: 300000},
		},
		"token":  "test-share-token",
		"forced": true,
	}

	for _, tmplName := range templates {
		t.Run("Render_"+tmplName, func(t *testing.T) {
			w := httptest.NewRecorder()
			err := RenderTemplate(w, reqWithCtx, db, tmplName, sampleData)
			if err != nil {
				t.Fatalf("RenderTemplate(%s) failed: %v", tmplName, err)
			}
			if w.Code != http.StatusOK {
				t.Errorf("RenderTemplate(%s) returned status %d", tmplName, w.Code)
			}
			body := w.Body.String()
			if len(body) == 0 {
				t.Errorf("RenderTemplate(%s) produced empty output", tmplName)
			}
		})
	}

	t.Run("Language Negotiation", func(t *testing.T) {
		// 1. Query param
		reqQ := httptest.NewRequest(http.MethodGet, "/?lang=ru", nil)
		if lang := NegotiateLocale(reqQ, db); lang != "ru" {
			t.Errorf("expected ru from query, got %q", lang)
		}

		// 2. Cookie "lang"
		reqC := httptest.NewRequest(http.MethodGet, "/", nil)
		reqC.AddCookie(&http.Cookie{Name: "lang", Value: "fr"})
		if lang := NegotiateLocale(reqC, db); lang != "fr" {
			t.Errorf("expected fr from cookie lang, got %q", lang)
		}

		// 3. Cookie "panel_lang"
		reqPL := httptest.NewRequest(http.MethodGet, "/", nil)
		reqPL.AddCookie(&http.Cookie{Name: "panel_lang", Value: "zh"})
		if lang := NegotiateLocale(reqPL, db); lang != "zh" {
			t.Errorf("expected zh from cookie panel_lang, got %q", lang)
		}

		// 4. DB appearance fallback
		_ = db.SetSetting(context.Background(), "appearance", models.AppearanceSettings{Language: "fa"})
		reqDB := httptest.NewRequest(http.MethodGet, "/", nil)
		if lang := NegotiateLocale(reqDB, db); lang != "fa" {
			t.Errorf("expected fa from DB appearance setting, got %q", lang)
		}

		// 5. Query param overrides DB appearance (cheap-first evaluation)
		reqOverride := httptest.NewRequest(http.MethodGet, "/?lang=ru", nil)
		if lang := NegotiateLocale(reqOverride, db); lang != "ru" {
			t.Errorf("expected ru from query param overriding DB, got %q", lang)
		}

		// 6. Fallback to default when nil DB and no query
		reqDef := httptest.NewRequest(http.MethodGet, "/", nil)
		if lang := NegotiateLocale(reqDef, nil); lang != "en" {
			t.Errorf("expected default en, got %q", lang)
		}
	})

	t.Run("Concurrent Rendering Safety", func(t *testing.T) {
		var wg sync.WaitGroup
		errCh := make(chan error, 50)

		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				w := httptest.NewRecorder()
				lang := "en"
				if idx%2 == 0 {
					lang = "ru"
				}
				r := httptest.NewRequest(http.MethodGet, "/?lang="+lang, nil)
				r = r.WithContext(reqCtx)
				if err := RenderTemplate(w, r, db, "index.html", sampleData); err != nil {
					errCh <- err
				}
			}(i)
		}

		wg.Wait()
		close(errCh)

		for err := range errCh {
			t.Errorf("concurrent render error: %v", err)
		}
	})

	t.Run("Nonexistent Template Returns 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		err := RenderTemplate(w, reqWithCtx, db, "nonexistent.html", nil)
		if err == nil {
			t.Error("expected error for nonexistent template")
		}
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404 for nonexistent template, got %d", w.Code)
		}
	})

	t.Run("ReloadTemplates", func(t *testing.T) {
		engine := GetTemplateEngine()
		if err := engine.ReloadTemplates(); err != nil {
			t.Fatalf("ReloadTemplates failed: %v", err)
		}
	})
}

func TestAdversarialTemplateSecurityAndResilience(t *testing.T) {
	_, db, _ := setupTestHandlers(t)

	templates := []string{
		"base.html",
		"login.html",
		"index.html",
		"users.html",
		"server.html",
		"settings.html",
		"my_connections.html",
		"setup.html",
		"change_password.html",
		"leaderboard.html",
		"user_share.html",
	}

	t.Run("Zero Panic On Nil Data", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		for _, tmplName := range templates {
			w := httptest.NewRecorder()
			// Must never panic even with completely nil data map and unauthenticated context
			err := RenderTemplate(w, req, db, tmplName, nil)
			if err != nil {
				t.Errorf("RenderTemplate(%s, nil) returned error: %v", tmplName, err)
			}
			if w.Code != http.StatusOK {
				t.Errorf("RenderTemplate(%s, nil) returned status %d", tmplName, w.Code)
			}
		}
	})

	t.Run("Adversarial CleanReferer Suite", func(t *testing.T) {
		tests := []struct {
			input      string
			defaultVal string
			want       string
		}{
			{"", "/", "/"},
			{"/valid/path", "/", "/valid/path"},
			{"/path?arg=1&foo=bar", "/", "/path?arg=1&foo=bar"},
			{"http://attacker.com/steal", "/", "/steal"},
			{"https://attacker.com", "/", "/"},
			{"//attacker.com", "/", "/"},
			{"///attacker.com", "/", "/"},
			{"\\\\attacker.com", "/", "/"},
			{"https://evil.com/\\evil.com", "/", "/"},
			{"https://evil.com/%5Cevil.com", "/", "/"},
			{"https://evil.com/%5cevil.com", "/", "/"},
			{"/\\evil.com", "/", "/"},
			{"/%5Cevil.com", "/", "/"},
			{"/%5cevil.com", "/", "/"},
			{"//evil.com", "/", "/"},
			{"\\\\evil.com", "/", "/"},
			{"/\\\\evil.com", "/", "/"},
			{"javascript:alert(1)", "/", "/"},
			{"data:text/html,<script>alert(1)</script>", "/", "/"},
			{"   ", "/fallback", "/fallback"},
			{"/my", "/fallback", "/my"},
			{"/my?lang=ru", "/", "/my?lang=ru"},
		}

		for _, tt := range tests {
			got := CleanReferer(tt.input, tt.defaultVal)
			if got != tt.want {
				t.Errorf("CleanReferer(%q, %q) = %q, want %q", tt.input, tt.defaultVal, got, tt.want)
			}
		}
	})

	t.Run("All 5 Supported Locales", func(t *testing.T) {
		supportedLangs := []string{"en", "ru", "fr", "zh", "fa"}
		for _, lang := range supportedLangs {
			req := httptest.NewRequest(http.MethodGet, "/?lang="+lang, nil)
			negotiated := NegotiateLocale(req, db)
			if negotiated != lang {
				t.Errorf("NegotiateLocale for %q = %q, want %q", lang, negotiated, lang)
			}

			w := httptest.NewRecorder()
			err := RenderTemplate(w, req, db, "base.html", map[string]any{
				"lang": lang,
			})
			if err != nil {
				t.Fatalf("RenderTemplate failed for lang %s: %v", lang, err)
			}
			body := w.Body.String()
			if lang == "fa" && !strings.Contains(body, `dir="rtl"`) {
				t.Errorf("Persian (fa) layout missing dir=\"rtl\": %s", body[:200])
			}
		}
	})

	t.Run("NegotiateLocale Normalization", func(t *testing.T) {
		cases := []struct {
			query string
			want  string
		}{
			{"/my?lang=RU", "ru"},
			{"/my?lang=%20Ru%20", "ru"},
			{"/my?lang=+ru+", "ru"},
			{"/my?lang=Fa", "fa"},
			{"/my?lang=FR", "fr"},
			{"/my?lang=ZH", "zh"},
			{"/my?lang=en", "en"},
			{"/my?lang=unknown", "en"},
		}
		for _, tc := range cases {
			req := httptest.NewRequest(http.MethodGet, tc.query, nil)
			got := NegotiateLocale(req, db)
			if got != tc.want {
				t.Errorf("NegotiateLocale(%q) = %q, want %q", tc.query, got, tc.want)
			}
		}

		// Test Cookie normalization
		reqCookie := httptest.NewRequest(http.MethodGet, "/my", nil)
		reqCookie.AddCookie(&http.Cookie{Name: "lang", Value: " RU "})
		if got := NegotiateLocale(reqCookie, db); got != "ru" {
			t.Errorf("NegotiateLocale from cookie ' RU ' = %q, want 'ru'", got)
		}
	})

	t.Run("CSRF Token Propagation", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := middleware.WithCSRFToken(req.Context(), "expected-secret-csrf-token-12345")
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()
		err := RenderTemplate(w, req, db, "base.html", nil)
		if err != nil {
			t.Fatalf("RenderTemplate failed: %v", err)
		}
		body := w.Body.String()
		expectedMeta := `<meta name="csrf-token" content="expected-secret-csrf-token-12345">`
		if !strings.Contains(body, expectedMeta) {
			t.Errorf("base.html missing expected CSRF meta header. Body:\n%s", body[:400])
		}
	})

	t.Run("XSS Escaping In Templates", func(t *testing.T) {
		xssPayload := `<script>alert("xss")</script>`
		server := &models.Server{
			ID:   99,
			Name: xssPayload,
			Host: "10.0.0.1",
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		err := RenderTemplate(w, req, db, "index.html", map[string]any{
			"servers": []*models.Server{server},
		})
		if err != nil {
			t.Fatalf("RenderTemplate failed: %v", err)
		}
		body := w.Body.String()
		// Raw script tag must NOT exist in unescaped form inside HTML body
		if strings.Contains(body, `<div class="server-name"><script>alert("xss")</script></div>`) {
			t.Errorf("XSS payload was rendered unescaped in server name!")
		}
		if !strings.Contains(body, `&lt;script&gt;alert(&#34;xss&#34;)&lt;/script&gt;`) {
			t.Errorf("Expected escaped script tags in server name output")
		}
	})

	t.Run("Adversarial XSS Quote Breakout", func(t *testing.T) {
		payloads := []string{
			`x" onmouseover="window.pwned=1//`,
			`"><script>alert(1)</script>`,
			`' onclick='window.pwned=2`,
			`" autofocus onfocus="alert(1)"`,
			`"><img src=x onerror=alert(1)>`,
		}

		for _, payload := range payloads {
			conn := &models.UserConnection{
				ID:         "conn-xss",
				Name:       payload,
				ServerName: payload,
				Protocol:   "awg",
			}
			req := httptest.NewRequest(http.MethodGet, "/my", nil)
			w := httptest.NewRecorder()
			err := RenderTemplate(w, req, db, "my_connections.html", map[string]any{
				"connections": []*models.UserConnection{conn},
				"servers": []SanitizedServerForUser{
					{ID: 1, Name: payload, Status: "online"},
				},
			})
			if err != nil {
				t.Fatalf("RenderTemplate failed on payload %q: %v", payload, err)
			}
			body := w.Body.String()
			if strings.Contains(body, ` onmouseover="window.pwned=1//"`) ||
				strings.Contains(body, ` onclick='window.pwned=2'`) ||
				strings.Contains(body, ` autofocus onfocus="alert(1)"`) {
				t.Errorf("Unescaped attribute XSS handler executed in my_connections.html with payload: %s", payload)
			}
			if strings.Contains(body, `<script>alert(1)</script>`) || strings.Contains(body, `<img src=x onerror=alert(1)>`) {
				t.Errorf("Unescaped tag injection in my_connections.html with payload: %s", payload)
			}

			wShare := httptest.NewRecorder()
			err = RenderTemplate(wShare, req, db, "user_share.html", map[string]any{
				"token": "tok123",
				"share_user": &models.User{
					Username: payload,
				},
			})
			if err != nil {
				t.Fatalf("RenderTemplate user_share failed: %v", err)
			}
			shareBody := wShare.Body.String()
			if strings.Contains(shareBody, `<script>alert(1)</script>`) {
				t.Errorf("Unescaped tag in user_share.html with payload: %s", payload)
			}
		}
	})
}

func TestRenderMyConnectionsNoCredentialLeak(t *testing.T) {
	h, db, _ := setupTestHandlers(t)
	ctx := context.Background()

	sshPassSecret := "dummy-ssh-password-secret-12345"
	sshKeySecret := "-----BEGIN RSA PRIVATE KEY-----\ndummy-rsa-private-key-data-xyz-998877\n-----END RSA PRIVATE KEY-----"

	srv := &models.Server{
		Name:      "Production-VPN-Node",
		Host:      "198.51.100.25",
		SSHUser:   "root",
		SSHPort:   22,
		SSHPass:   sshPassSecret,
		SSHKey:    sshKeySecret,
		Protocols: map[string]any{"awg": map[string]any{"installed": true}},
		CreatedAt: time.Now(),
	}
	sID, err := db.CreateServer(ctx, srv)
	if err != nil {
		t.Fatalf("CreateServer failed: %v", err)
	}

	userPassHashSecret := "$2a$14$dummy-bcrypt-user-password-hash-secret-value-12345"
	sharePassHashSecret := "$2a$14$dummy-bcrypt-share-password-hash-secret-value-67890"

	u := &models.User{
		ID:                "reg-user-1",
		Username:          "regularuser",
		Role:              models.RoleUser,
		PasswordHash:      userPassHashSecret,
		SharePasswordHash: &sharePassHashSecret,
		Enabled:           true,
		CreatedAt:         time.Now(),
	}
	if _, err := db.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	conn := &models.UserConnection{
		ID:        "conn-123",
		UserID:    u.ID,
		ServerID:  sID,
		Protocol:  "awg",
		ClientID:  "client-key-1",
		Name:      "My AWG",
		CreatedAt: time.Now(),
	}
	if _, err := db.CreateConnection(ctx, conn); err != nil {
		t.Fatalf("CreateConnection failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/my", nil)
	reqCtx := middleware.WithSession(req.Context(), &models.SessionData{
		UserID:   u.ID,
		Username: u.Username,
		Role:     models.RoleUser,
	})
	w := httptest.NewRecorder()

	h.MyConnectionsPageHandler(w, req.WithContext(reqCtx))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	htmlOutput := w.Body.String()

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

	// Verify that models.User JSON marshaling doesn't emit credential hashes
	marshaledUser, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("json.Marshal(User) failed: %v", err)
	}
	if strings.Contains(string(marshaledUser), userPassHashSecret) || strings.Contains(string(marshaledUser), "dummy-bcrypt-user-password-hash-secret-value-12345") {
		t.Fatalf("models.User JSON marshaling leaked PasswordHash: %s", string(marshaledUser))
	}
	if strings.Contains(string(marshaledUser), sharePassHashSecret) || strings.Contains(string(marshaledUser), "dummy-bcrypt-share-password-hash-secret-value-67890") {
		t.Fatalf("models.User JSON marshaling leaked SharePasswordHash: %s", string(marshaledUser))
	}
}
