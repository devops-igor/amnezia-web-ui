package handlers

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestTemplateEngineAndHelpers(t *testing.T) {
	_, db, _ := setupTestHandlers(t)

	// FormatBytes test cases
	byteTests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.00 KB"},
		{1048576, "1.00 MB"},
		{1073741824, "1.00 GB"},
		{1099511627776, "1.00 TB"},
		{1125899906842624, "1.00 PB"},
		{1152921504606846976, "1.00 EB"},
		{-1024, "-1.00 KB"},
	}

	for _, tt := range byteTests {
		got := FormatBytes(tt.input)
		if got != tt.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}

	// FormatTime test cases
	if got := FormatTime(time.Time{}); got != "" {
		t.Errorf("expected empty string for zero time, got %q", got)
	}
	fixedTime := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	if got := FormatTime(fixedTime); got != "2026-08-28 12:00:00" {
		t.Errorf("FormatTime failed: %q", got)
	}

	// CleanReferer test cases
	refTests := []struct {
		input string
		want  string
	}{
		{"", "/"},
		{"/my", "/my"},
		{"/server/1?tab=1", "/server/1?tab=1"},
		{"https://evil.com/phish", "/phish"},
		{"http://localhost:5000/users", "/users"},
	}
	for _, tt := range refTests {
		got := CleanReferer(tt.input)
		if got != tt.want {
			t.Errorf("CleanReferer(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}

	// RenderTemplate test all templates
	templates := []string{
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
		UserID: "admin-1",
		Role:   models.RoleAdmin,
	})
	reqCtx = middleware.WithCSRFToken(reqCtx, "csrf-test-token")
	reqWithCtx := req.WithContext(reqCtx)

	for _, tmplName := range templates {
		t.Run("Render_"+tmplName, func(t *testing.T) {
			w := httptest.NewRecorder()
			err := RenderTemplate(w, reqWithCtx, db, tmplName, map[string]any{
				"server_id": "1",
				"server": &models.Server{
					ID:   1,
					Name: "Test Server",
				},
				"users": []models.User{},
			})
			if err != nil {
				t.Fatalf("RenderTemplate(%s) failed: %v", tmplName, err)
			}
			if w.Code != http.StatusOK {
				t.Errorf("RenderTemplate(%s) returned status %d", tmplName, w.Code)
			}
		})
	}

	t.Run("RenderTemplate Language Cookie", func(t *testing.T) {
		reqLang := httptest.NewRequest(http.MethodGet, "/", nil)
		reqLang.AddCookie(&http.Cookie{Name: "lang", Value: "ru"})
		w := httptest.NewRecorder()
		err := RenderTemplate(w, reqLang, db, "login.html", nil)
		if err != nil {
			t.Fatalf("RenderTemplate failed: %v", err)
		}
	})

	t.Run("RenderTemplate Panel Lang Cookie Fallback", func(t *testing.T) {
		reqPanel := httptest.NewRequest(http.MethodGet, "/", nil)
		reqPanel.AddCookie(&http.Cookie{Name: "panel_lang", Value: "ru"})
		w := httptest.NewRecorder()
		err := RenderTemplate(w, reqPanel, db, "login.html", nil)
		if err != nil {
			t.Fatalf("RenderTemplate failed: %v", err)
		}
	})

	t.Run("RenderTemplate Unknown Lang Cookie Falls Back to EN", func(t *testing.T) {
		reqUnknown := httptest.NewRequest(http.MethodGet, "/", nil)
		reqUnknown.AddCookie(&http.Cookie{Name: "lang", Value: "xx-UNKNOWN"})
		w := httptest.NewRecorder()
		err := RenderTemplate(w, reqUnknown, db, "login.html", nil)
		if err != nil {
			t.Fatalf("RenderTemplate failed: %v", err)
		}
	})

	t.Run("RenderTemplate with Nil DB", func(t *testing.T) {
		w := httptest.NewRecorder()
		err := RenderTemplate(w, reqWithCtx, (*database.DB)(nil), "login.html", nil)
		if err != nil {
			t.Fatalf("RenderTemplate failed: %v", err)
		}
	})

	t.Run("RenderTemplate with Nil Request", func(t *testing.T) {
		w := httptest.NewRecorder()
		//nolint:staticcheck // intentionally testing nil request branch
		err := RenderTemplate(w, nil, db, "login.html", nil)
		if err != nil {
			t.Fatalf("RenderTemplate failed: %v", err)
		}
	})

	t.Run("RenderTemplate Fallback Nonexistent", func(t *testing.T) {
		w := httptest.NewRecorder()
		_ = RenderTemplate(w, reqWithCtx, (*database.DB)(nil), "nonexistent.html", nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404 for nonexistent template, got %d", w.Code)
		}
	})

	t.Run("TemplateEngine Direct Load and FuncMap", func(t *testing.T) {
		engine := &TemplateEngine{templates: make(map[string]*template.Template)}
		err := engine.loadTemplates()
		if err != nil {
			t.Fatalf("loadTemplates failed: %v", err)
		}

		fm := templateFuncMap()
		jsonFn := fm["json"].(func(any) template.JS)
		_ = jsonFn(map[string]string{"key": "val"})

		tFn := fm["t"].(func(string) string)
		_ = tFn("login.title")

		transFn := fm["translate"].(func(string) string)
		_ = transFn("login.title")

		underFn := fm["_"].(func(string) string)
		_ = underFn("login.title")

		hasRoleFn := fm["has_role"].(func(*models.User, string) bool)
		uAdmin := &models.User{Role: models.RoleAdmin}
		uUser := &models.User{Role: models.RoleUser}
		if !hasRoleFn(uAdmin, "admin") {
			t.Error("expected hasRole admin")
		}
		if hasRoleFn(uUser, "admin") {
			t.Error("expected hasRole not admin")
		}
		if hasRoleFn(nil, "admin") {
			t.Error("expected hasRole nil false")
		}

		isAdminFn := fm["is_admin"].(func(*models.User) bool)
		if !isAdminFn(uAdmin) {
			t.Error("expected isAdmin true")
		}
		if isAdminFn(uUser) {
			t.Error("expected isAdmin false")
		}
		if isAdminFn(nil) {
			t.Error("expected isAdmin nil false")
		}
	})

	t.Run("RenderTemplate Fallback Raw File", func(t *testing.T) {
		engine := GetTemplateEngine()
		engine.mu.Lock()
		delete(engine.templates, "login.html")
		engine.mu.Unlock()

		w := httptest.NewRecorder()
		err := RenderTemplate(w, reqWithCtx, db, "login.html", nil)
		if err != nil {
			t.Fatalf("fallback render failed: %v", err)
		}

		// Restore templates
		_ = engine.loadTemplates()
	})
}
