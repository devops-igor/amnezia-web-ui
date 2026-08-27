package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/web"
)

// TemplateEngine manages server-side rendered HTML templates.
type TemplateEngine struct {
	mu        sync.RWMutex
	templates map[string]*template.Template
}

var (
	defaultEngine *TemplateEngine
	engineOnce    sync.Once
)

// FormatBytes formats byte counts into human-readable strings (e.g., "1.50 GB").
func FormatBytes(n int64) string {
	if n == 0 {
		return "0 B"
	}
	negative := n < 0
	val := float64(n)
	if negative {
		val = -val
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	for _, unit := range units {
		if val < 1024.0 {
			var result string
			if unit == "B" {
				result = fmt.Sprintf("%d %s", int64(val), unit)
			} else {
				result = fmt.Sprintf("%.2f %s", val, unit)
			}
			if negative {
				return "-" + result
			}
			return result
		}
		val /= 1024.0
	}
	result := fmt.Sprintf("%.2f EB", val)
	if negative {
		return "-" + result
	}
	return result
}

// FormatTime formats a time into standard display format.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

// GetTemplateEngine returns the singleton template engine instance.
func GetTemplateEngine() *TemplateEngine {
	engineOnce.Do(func() {
		defaultEngine = &TemplateEngine{
			templates: make(map[string]*template.Template),
		}
		_ = defaultEngine.loadTemplates()
	})
	return defaultEngine
}

func (te *TemplateEngine) loadTemplates() error {
	te.mu.Lock()
	defer te.mu.Unlock()

	templatesFS, err := web.GetTemplatesSubFS()
	if err != nil {
		return fmt.Errorf("failed to open templates filesystem: %w", err)
	}

	entries, err := fs.ReadDir(templatesFS, ".")
	if err != nil {
		return fmt.Errorf("failed to read templates directory: %w", err)
	}

	funcMap := template.FuncMap{
		"format_bytes": FormatBytes,
		"format_time":  FormatTime,
		"json": func(v any) template.JS {
			b, _ := json.Marshal(v)
			// #nosec G203 -- Safely serialized JSON data for embedded scripts
			return template.JS(string(b))
		},
		"t": func(key string) string {
			return config.T("en", key)
		},
		"translate": func(key string) string {
			return config.T("en", key)
		},
		"_": func(key string) string {
			return config.T("en", key)
		},
		"has_role": func(u *models.User, role string) bool {
			if u == nil {
				return false
			}
			return string(u.Role) == role
		},
		"is_admin": func(u *models.User) bool {
			return u != nil && u.IsAdmin()
		},
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		name := entry.Name()

		var tmpl *template.Template
		if name == "base.html" {
			tmpl, err = template.New(name).Funcs(funcMap).ParseFS(templatesFS, name)
		} else {
			tmpl, err = template.New(name).Funcs(funcMap).ParseFS(templatesFS, "base.html", name)
		}

		if err != nil {
			// Fallback: parse single template file if base parsing failed
			tmpl, err = template.New(name).Funcs(funcMap).ParseFS(templatesFS, name)
		}
		if err == nil && tmpl != nil {
			te.templates[name] = tmpl
		}
	}

	return nil
}

// GetLang extracts preferred language code from request cookie or defaults.
func GetLang(r *http.Request) string {
	if r != nil {
		if c, err := r.Cookie("lang"); err == nil && c.Value != "" {
			return c.Value
		}
		if c, err := r.Cookie("panel_lang"); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return "en"
}

// RenderTemplate renders the specified template with standard context variables.
func RenderTemplate(w http.ResponseWriter, r *http.Request, db *database.DB, name string, data map[string]any) error {
	lang := GetLang(r)

	// Fetch dynamic settings from DB
	appearance := models.AppearanceSettings{
		Title:    "Amnezia",
		Logo:     "🛡",
		Subtitle: "Web Panel",
		Language: lang,
	}
	captchaSettings := models.CaptchaSettings{
		Enabled: false,
	}

	if db != nil {
		ctx := r.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		_ = db.GetSetting(ctx, "appearance", &appearance)
		_ = db.GetSetting(ctx, "captcha", &captchaSettings)
	}

	csrfToken := middleware.GetCSRFToken(r.Context())

	engine := GetTemplateEngine()
	engine.mu.RLock()
	tmpl, exists := engine.templates[name]
	engine.mu.RUnlock()

	if exists && tmpl != nil {
		allTranslations := config.GetTranslations()
		langTranslations := allTranslations[lang]
		if langTranslations == nil {
			langTranslations = allTranslations["en"]
		}
		translationsJSON, _ := json.Marshal(langTranslations)
		allTranslationsJSON, _ := json.Marshal(allTranslations)

		ctxData := map[string]any{
			"current_user":          middleware.GetSession(r.Context()),
			"site_settings":         appearance,
			"captcha_settings":      captchaSettings,
			"lang":                  lang,
			"translations_json":     string(translationsJSON),
			"all_translations_json": string(allTranslationsJSON),
			"csrf_token":            csrfToken,
			"app_version":           config.AppVersion,
			"_": func(key string) string {
				return config.T(lang, key)
			},
			"t": func(key string) string {
				return config.T(lang, key)
			},
			"format_bytes": FormatBytes,
		}

		for k, v := range data {
			ctxData[k] = v
		}

		buf := new(bytes.Buffer)
		if err := tmpl.Execute(buf, ctxData); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, writeErr := w.Write(buf.Bytes())
			return writeErr
		}
	}

	// Fallback to reading raw embedded template file
	templatesFS, err := web.GetTemplatesSubFS()
	if err != nil {
		http.Error(w, "Failed to load templates", http.StatusInternalServerError)
		return err
	}

	content, err := fs.ReadFile(templatesFS, name)
	if err != nil {
		http.Error(w, fmt.Sprintf("Template %s not found", name), http.StatusNotFound)
		return err
	}

	rendered := string(content)
	rendered = strings.ReplaceAll(rendered, "{{ csrf_token }}", csrfToken)
	rendered = strings.ReplaceAll(rendered, "{{ lang }}", lang)
	rendered = strings.ReplaceAll(rendered, "{{ app_version }}", config.AppVersion)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// #nosec G705 G203 -- Server-rendered embedded template content
	_, writeErr := w.Write([]byte(rendered))
	return writeErr
}

// CleanReferer strips any external domain/protocol from the referer to prevent open redirect vulnerabilities.
func CleanReferer(rawReferer string) string {
	if rawReferer == "" {
		return "/"
	}
	u, err := url.Parse(rawReferer)
	if err != nil {
		return "/"
	}
	if u.Scheme != "" || u.Host != "" {
		path := u.Path
		if path == "" {
			path = "/"
		}
		if u.RawQuery != "" {
			path += "?" + u.RawQuery
		}
		return path
	}
	return rawReferer
}
