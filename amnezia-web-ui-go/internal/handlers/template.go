package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/devops-igor/amnezia-web-ui-go/internal/config"
	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/middleware"
	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
	"github.com/devops-igor/amnezia-web-ui-go/web"
)

// TemplateEngine manages parsing and rendering of HTML templates.
type TemplateEngine struct {
	templates map[string]*template.Template
	mu        sync.RWMutex
}

var (
	globalEngine    *TemplateEngine
	globalEngineErr error
	engineOnce      sync.Once

	translationsJSONCache = make(map[string]string)
	translationsCacheOnce sync.Once
)

func getTranslationsJSON(lang string) string {
	translationsCacheOnce.Do(func() {
		all := config.GetTranslations()
		for l, dict := range all {
			b, _ := json.Marshal(dict)
			translationsJSONCache[l] = string(b)
		}
	})
	if s, ok := translationsJSONCache[lang]; ok {
		return s
	}
	if s, ok := translationsJSONCache["en"]; ok {
		return s
	}
	return "{}"
}

// InitTemplateEngine initializes and loads templates, returning an error if parsing fails.
func InitTemplateEngine() (*TemplateEngine, error) {
	engineOnce.Do(func() {
		engine := &TemplateEngine{
			templates: make(map[string]*template.Template),
		}
		if err := engine.loadTemplates(); err != nil {
			log.Printf("[FATAL] Failed to load templates: %v", err)
			globalEngineErr = fmt.Errorf("failed to load templates: %w", err)
			return
		}
		globalEngine = engine
	})
	if globalEngineErr != nil {
		return nil, globalEngineErr
	}
	return globalEngine, nil
}

// GetTemplateEngine returns the singleton TemplateEngine instance.
// Panics if template loading fails, ensuring startup and parsing defects fail fast.
func GetTemplateEngine() *TemplateEngine {
	engine, err := InitTemplateEngine()
	if err != nil {
		panic(err)
	}
	return engine
}

// ReloadTemplates forces reloading of templates from the embedded filesystem atomically.
func (te *TemplateEngine) ReloadTemplates() error {
	templatesFS, err := web.GetTemplatesSubFS()
	if err != nil {
		return fmt.Errorf("failed to get templates sub-FS: %w", err)
	}

	fm := baseFuncMap()

	standalonePages := map[string]bool{
		"login.html":           true,
		"setup.html":           true,
		"change_password.html": true,
	}

	layoutPages := []string{
		"index.html",
		"server.html",
		"users.html",
		"my_connections.html",
		"settings.html",
		"leaderboard.html",
		"user_share.html",
	}

	fresh := make(map[string]*template.Template)

	baseTmpl, err := template.New("base.html").Funcs(fm).ParseFS(templatesFS, "base.html")
	if err != nil {
		return fmt.Errorf("failed to parse base.html: %w", err)
	}
	fresh["base.html"] = baseTmpl

	for name := range standalonePages {
		tmpl, err := template.New(name).Funcs(fm).ParseFS(templatesFS, name)
		if err != nil {
			return fmt.Errorf("failed to parse standalone template %s: %w", name, err)
		}
		fresh[name] = tmpl
	}

	for _, name := range layoutPages {
		tmpl, err := template.New("base.html").Funcs(fm).ParseFS(templatesFS, "base.html", name)
		if err != nil {
			return fmt.Errorf("failed to parse layout template %s: %w", name, err)
		}
		fresh[name] = tmpl
	}

	te.mu.Lock()
	te.templates = fresh
	te.mu.Unlock()

	return nil
}

// TemplateFuncMap returns the base FuncMap helpers for templates.
func TemplateFuncMap() template.FuncMap {
	return baseFuncMap()
}

func titleCase(s string) string {
	if s == "" {
		return ""
	}
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		if len(r) > 0 {
			r[0] = unicode.ToUpper(r[0])
			words[i] = string(r)
		}
	}
	return strings.Join(words, " ")
}

func jsonFuncMap() template.FuncMap {
	return template.FuncMap{
		"json": func(v any) template.JS {
			b, err := json.Marshal(v)
			if err != nil {
				return template.JS("null")
			}
			// #nosec G203 -- Deliberately generating raw JSON for inline script evaluation
			return template.JS(b)
		},
		"tojson": func(v any) template.JS {
			b, err := json.Marshal(v)
			if err != nil {
				return template.JS("null")
			}
			// #nosec G203 -- Deliberately generating raw JSON for inline script evaluation
			return template.JS(b)
		},
	}
}

func safeFuncMap() template.FuncMap {
	return template.FuncMap{
		"safe_html": func(s any) template.HTML {
			// #nosec G203 -- Deliberate safe HTML pass-through helper for trusted markup
			return template.HTML(fmt.Sprintf("%v", s))
		},
		"safe_js": func(v any) template.JS {
			switch val := v.(type) {
			case string:
				// #nosec G203 -- Deliberate JS pass-through
				return template.JS(val)
			case template.JS:
				return val
			default:
				b, _ := json.Marshal(v)
				// #nosec G203 -- Deliberate JS pass-through
				return template.JS(b)
			}
		},
		"safe_css": func(s any) template.CSS {
			// #nosec G203 -- Deliberate safe CSS pass-through helper for trusted styling
			return template.CSS(fmt.Sprintf("%v", s))
		},
		"safe_attr": func(s any) template.HTMLAttr {
			// #nosec G203 -- Deliberate safe HTML attribute pass-through helper
			return template.HTMLAttr(fmt.Sprintf("%v", s))
		},
		"safe_url": func(s any) template.URL {
			// #nosec G203 -- Deliberate safe URL pass-through helper
			return template.URL(fmt.Sprintf("%v", s))
		},
	}
}

func translationFuncMap() template.FuncMap {
	return template.FuncMap{
		"t": func(args ...any) string {
			if len(args) == 0 {
				return ""
			}
			return config.Translate("en", fmt.Sprintf("%v", args[0]))
		},
		"_": func(args ...any) string {
			if len(args) == 0 {
				return ""
			}
			return config.Translate("en", fmt.Sprintf("%v", args[0]))
		},
		"translate": func(args ...any) string {
			if len(args) == 0 {
				return ""
			}
			return config.Translate("en", fmt.Sprintf("%v", args[0]))
		},
	}
}

func roleFuncMap() template.FuncMap {
	return template.FuncMap{
		"has_role": func(u any, roles ...string) bool {
			if u == nil {
				return false
			}
			role := extractRole(u)
			for _, r := range roles {
				if strings.EqualFold(role, r) {
					return true
				}
			}
			return false
		},
		"is_admin": func(u any) bool {
			if u == nil {
				return false
			}
			return extractRole(u) == "admin"
		},
		"proto_title": func(proto any) string {
			p := fmt.Sprintf("%v", proto)
			switch p {
			case "awg":
				return "AmneziaWG"
			case "telemt":
				return "MTProxyL"
			case "dns":
				return "AmneziaDNS"
			default:
				return strings.ToUpper(p)
			}
		},
		"is_installed": func(v any) bool {
			if v == nil {
				return false
			}
			switch val := v.(type) {
			case bool:
				return val
			case map[string]any:
				if inst, ok := val["installed"].(bool); ok {
					return inst
				}
				if inst, ok := val["Installed"].(bool); ok {
					return inst
				}
			}
			return false
		},
	}
}

func extractRole(u any) string {
	switch user := u.(type) {
	case models.User:
		return string(user.Role)
	case *models.User:
		if user != nil {
			return string(user.Role)
		}
	case models.SessionData:
		return string(user.Role)
	case *models.SessionData:
		if user != nil {
			return string(user.Role)
		}
	case map[string]any:
		if r, ok := user["role"].(string); ok {
			return r
		}
		if r, ok := user["Role"].(string); ok {
			return r
		}
	}
	return ""
}

func dataAccessFuncMap() template.FuncMap {
	return template.FuncMap{
		"get": func(target any, key any, defaultVal ...any) any {
			if target == nil {
				if len(defaultVal) > 0 {
					return defaultVal[0]
				}
				return nil
			}

			keyStr := fmt.Sprintf("%v", key)
			rv := reflect.ValueOf(target)
			if rv.Kind() == reflect.Ptr {
				rv = rv.Elem()
			}

			if rv.Kind() == reflect.Map {
				for _, mapKey := range rv.MapKeys() {
					if fmt.Sprintf("%v", mapKey.Interface()) == keyStr {
						val := rv.MapIndex(mapKey).Interface()
						if val != nil {
							return val
						}
					}
				}
			} else if rv.Kind() == reflect.Struct {
				field := rv.FieldByName(keyStr)
				if !field.IsValid() {
					field = rv.FieldByName(titleCase(keyStr))
				}
				if field.IsValid() && field.CanInterface() {
					return field.Interface()
				}
			}

			if len(defaultVal) > 0 {
				return defaultVal[0]
			}
			return nil
		},
	}
}

func stringFuncMap() template.FuncMap {
	return template.FuncMap{
		"upper": func(v any) string {
			return strings.ToUpper(fmt.Sprintf("%v", v))
		},
		"lower": func(v any) string {
			return strings.ToLower(fmt.Sprintf("%v", v))
		},
		"title": func(v any) string {
			return titleCase(fmt.Sprintf("%v", v))
		},
		"contains": func(s, substr any) bool {
			return strings.Contains(fmt.Sprintf("%v", s), fmt.Sprintf("%v", substr))
		},
		"has_prefix": func(s, prefix any) bool {
			return strings.HasPrefix(fmt.Sprintf("%v", s), fmt.Sprintf("%v", prefix))
		},
		"has_suffix": func(s, suffix any) bool {
			return strings.HasSuffix(fmt.Sprintf("%v", s), fmt.Sprintf("%v", suffix))
		},
		"trim": func(v any) string {
			return strings.TrimSpace(fmt.Sprintf("%v", v))
		},
		"replace": func(s, old, new any) string {
			return strings.ReplaceAll(fmt.Sprintf("%v", s), fmt.Sprintf("%v", old), fmt.Sprintf("%v", new))
		},
		"slice_str": func(s any, start int, end ...int) string {
			str := fmt.Sprintf("%v", s)
			runes := []rune(str)
			n := len(runes)
			if start < 0 {
				start = 0
			}
			if start > n {
				start = n
			}
			finish := n
			if len(end) > 0 {
				finish = end[0]
				if finish < 0 {
					finish = 0
				}
				if finish > n {
					finish = n
				}
			}
			if start > finish {
				return ""
			}
			return string(runes[start:finish])
		},
		"str": func(v any) string {
			return fmt.Sprintf("%v", v)
		},
	}
}

func mathLogicFuncMap() template.FuncMap {
	return template.FuncMap{
		"format_bytes": FormatBytes,
		"format_time":  FormatTime,
		"default": func(def any, val any) any {
			if val == nil {
				return def
			}
			rv := reflect.ValueOf(val)
			if rv.Kind() == reflect.Ptr && rv.IsNil() {
				return def
			}
			if rv.IsZero() {
				return def
			}
			return val
		},
		"ternary": func(cond bool, trueVal, falseVal any) any {
			if cond {
				return trueVal
			}
			return falseVal
		},
		"add": func(a, b any) int64 {
			return toInt64(a) + toInt64(b)
		},
		"sub": func(a, b any) int64 {
			return toInt64(a) - toInt64(b)
		},
		"mul": func(a, b any) int64 {
			return toInt64(a) * toInt64(b)
		},
		"div": func(a, b any) int64 {
			bVal := toInt64(b)
			if bVal == 0 {
				return 0
			}
			return toInt64(a) / bVal
		},
		"mod": func(a, b any) int64 {
			bVal := toInt64(b)
			if bVal == 0 {
				return 0
			}
			return toInt64(a) % bVal
		},
		"seq": func(start, end int) []int {
			if end < start {
				return []int{}
			}
			res := make([]int, end-start+1)
			for i := range res {
				res[i] = start + i
			}
			return res
		},
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict requires an even number of arguments")
			}
			dict := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
		"int_eq": func(a, b any) bool {
			return toInt64(a) == toInt64(b)
		},
		"str_eq": func(a, b any) bool {
			return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
		},
		"int": func(v any) int64 {
			return toInt64(v)
		},
	}
}

// baseFuncMap merges all grouped FuncMap helpers.
func baseFuncMap() template.FuncMap {
	fm := make(template.FuncMap)
	groups := []template.FuncMap{
		jsonFuncMap(),
		safeFuncMap(),
		translationFuncMap(),
		roleFuncMap(),
		dataAccessFuncMap(),
		stringFuncMap(),
		mathLogicFuncMap(),
	}
	for _, g := range groups {
		for k, v := range g {
			fm[k] = v
		}
	}
	return fm
}

func toInt64(v any) int64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case int:
		return int64(val)
	case int8:
		return int64(val)
	case int16:
		return int64(val)
	case int32:
		return int64(val)
	case int64:
		return val
	case uint:
		if uint64(val) > math.MaxInt64 {
			return math.MaxInt64
		}
		// #nosec G115 -- Bounds verified above
		return int64(val)
	case uint8:
		return int64(val)
	case uint16:
		return int64(val)
	case uint32:
		return int64(val)
	case uint64:
		if val > math.MaxInt64 {
			return math.MaxInt64
		}
		// #nosec G115 -- Bounds verified above
		return int64(val)
	case float32:
		return int64(val)
	case float64:
		return int64(val)
	case string:
		i, _ := strconv.ParseInt(val, 10, 64)
		return i
	case json.Number:
		i, _ := val.Int64()
		return i
	default:
		return 0
	}
}

// loadTemplates parses and compiles all templates from the embedded filesystem.
func (te *TemplateEngine) loadTemplates() error {
	return te.ReloadTemplates()
}

// extractLocaleFromRequest extracts a valid normalized language code from request query params or cookies.
func extractLocaleFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	// 1. Query parameter ?lang=
	if qLang := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("lang"))); qLang != "" && config.IsValidLanguage(qLang) {
		return qLang
	}

	// 2. Cookie "lang"
	if c, err := r.Cookie("lang"); err == nil {
		if cVal := strings.ToLower(strings.TrimSpace(c.Value)); cVal != "" && config.IsValidLanguage(cVal) {
			return cVal
		}
	}

	// 3. Cookie "panel_lang"
	if c, err := r.Cookie("panel_lang"); err == nil {
		if cVal := strings.ToLower(strings.TrimSpace(c.Value)); cVal != "" && config.IsValidLanguage(cVal) {
			return cVal
		}
	}

	return ""
}

// NegotiateLocale determines the request's locale from query param, cookies, DB appearance, or defaults to "en".
func NegotiateLocale(r *http.Request, dbInstance *database.DB) string {
	if reqLang := extractLocaleFromRequest(r); reqLang != "" {
		return reqLang
	}

	appLang := ""
	if dbInstance != nil {
		var app models.AppearanceSettings
		ctx := context.Background()
		if r != nil {
			ctx = r.Context()
		}
		if err := dbInstance.GetSetting(ctx, "appearance", &app); err == nil {
			appLang = app.Language
		}
	}
	return negotiateLocaleWithFallback(r, appLang)
}

func negotiateLocaleWithFallback(r *http.Request, fallbackLang string) string {
	if reqLang := extractLocaleFromRequest(r); reqLang != "" {
		return reqLang
	}

	// 4. DB appearance settings fallback
	if fVal := strings.ToLower(strings.TrimSpace(fallbackLang)); fVal != "" && config.IsValidLanguage(fVal) {
		return fVal
	}

	// 5. Default
	return "en"
}

// RenderTemplate renders the named template with full context data and dynamic locale.
func RenderTemplate(w http.ResponseWriter, r *http.Request, dbInstance *database.DB, name string, data map[string]interface{}) error {
	te := GetTemplateEngine()

	te.mu.RLock()
	tmpl, exists := te.templates[name]
	te.mu.RUnlock()

	if !exists {
		err := fmt.Errorf("template %q not found", name)
		log.Printf("[ERROR] RenderTemplate: %v", err)
		http.Error(w, "Template Not Found", http.StatusNotFound)
		return err
	}

	// Fetch Appearance settings once from DB
	appearance := models.AppearanceSettings{
		Title:    "Amnezia",
		Logo:     "🛡",
		Subtitle: "Web Panel",
		Language: "en",
	}
	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	if dbInstance != nil {
		_ = dbInstance.GetSetting(ctx, "appearance", &appearance)
	}

	// Negotiate language using query/cookie and appearance fallback without duplicate DB query
	lang := negotiateLocaleWithFallback(r, appearance.Language)
	appearance.Language = lang

	// Fetch current user / session from context
	var currentUser *models.User
	if r != nil {
		if sess := middleware.GetSession(r.Context()); sess != nil {
			if dbInstance != nil {
				if u, err := dbInstance.GetUser(r.Context(), sess.UserID); err == nil && u != nil {
					currentUser = u
				}
			}
			if currentUser == nil {
				currentUser = &models.User{
					ID:       sess.UserID,
					Username: sess.Username,
					Role:     sess.Role,
				}
			}
		}
	}

	// Captcha settings from DB
	captchaSettings := models.CaptchaSettings{
		Enabled: false,
	}
	if dbInstance != nil {
		_ = dbInstance.GetSetting(ctx, "captcha", &captchaSettings)
	}

	// CSRF Token
	csrfToken := ""
	if r != nil {
		if tok := middleware.GetCSRFToken(r.Context()); tok != "" {
			csrfToken = tok
		} else if c, err := r.Cookie("csrftoken"); err == nil && c.Value != "" {
			csrfToken = c.Value
		}
	}

	// Translations JSON (cached per language, avoiding per-request allocations)
	translationsJSON := getTranslationsJSON(lang)

	// Build context data
	ctxData := make(map[string]interface{}, len(data)+8)
	ctxData["current_user"] = currentUser
	ctxData["site_settings"] = appearance
	ctxData["captcha_settings"] = captchaSettings
	ctxData["lang"] = lang
	ctxData["translations_json"] = translationsJSON
	ctxData["csrf_token"] = csrfToken
	ctxData["app_version"] = config.AppVersion

	// Merge caller-provided data
	for k, v := range data {
		ctxData[k] = v
	}

	// Clone template to bind per-request translation functions
	reqTmpl, err := tmpl.Clone()
	if err != nil {
		reqTmpl = tmpl
	} else {
		reqTmpl.Funcs(template.FuncMap{
			"t": func(args ...any) string {
				if len(args) == 0 {
					return ""
				}
				return config.Translate(lang, fmt.Sprintf("%v", args[0]))
			},
			"_": func(args ...any) string {
				if len(args) == 0 {
					return ""
				}
				return config.Translate(lang, fmt.Sprintf("%v", args[0]))
			},
			"translate": func(args ...any) string {
				if len(args) == 0 {
					return ""
				}
				return config.Translate(lang, fmt.Sprintf("%v", args[0]))
			},
		})
	}

	// Execute template into buffer to catch rendering errors before writing status headers
	var buf bytes.Buffer
	if err := reqTmpl.Execute(&buf, ctxData); err != nil {
		log.Printf("[ERROR] RenderTemplate execution error on %s: %v", name, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return fmt.Errorf("template execution error: %w", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
	return nil
}

// FormatBytes formats a byte count into a human-readable string (e.g. "1.50 MB").
func FormatBytes(v any) string {
	val := toInt64(v)
	if val == 0 {
		return "0 B"
	}
	neg := false
	if val < 0 {
		neg = true
		val = -val
	}
	bytes := float64(val)
	units := []string{"B", "KB", "MB", "GB", "TB", "PB", "EB"}
	i := 0
	for bytes >= 1024 && i < len(units)-1 {
		bytes /= 1024
		i++
	}
	res := ""
	if i == 0 {
		res = fmt.Sprintf("%.0f %s", bytes, units[i])
	} else {
		res = fmt.Sprintf("%.2f %s", bytes, units[i])
	}
	if neg {
		return "-" + res
	}
	return res
}

// FormatTime formats a time value or timestamp string to "2006-01-02 15:04:05".
func FormatTime(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case time.Time:
		if val.IsZero() {
			return ""
		}
		return val.Format("2006-01-02 15:04:05")
	case *time.Time:
		if val == nil || val.IsZero() {
			return ""
		}
		return val.Format("2006-01-02 15:04:05")
	case string:
		if val == "" {
			return ""
		}
		for _, layout := range []string{
			time.RFC3339,
			time.RFC3339Nano,
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
			"2006-01-02",
		} {
			if t, err := time.Parse(layout, val); err == nil {
				return t.Format("2006-01-02 15:04:05")
			}
		}
		return val
	default:
		return fmt.Sprintf("%v", v)
	}
}

// CleanReferer validates and cleans a Referer URL to prevent Open Redirect attacks.
func CleanReferer(referer string, defaultURL ...string) string {
	def := "/"
	if len(defaultURL) > 0 && defaultURL[0] != "" {
		def = defaultURL[0]
	}
	if referer == "" {
		return def
	}
	// Reject backslashes or encoded backslashes (%5C, %5c) in raw referer
	if strings.Contains(referer, "\\") || strings.Contains(strings.ToLower(referer), "%5c") {
		return def
	}
	// Reject scheme-relative URLs
	if strings.HasPrefix(referer, "//") {
		return def
	}
	u, err := url.Parse(referer)
	if err != nil {
		return def
	}
	path := u.Path
	if path == "" {
		path = "/"
	}
	// Reject backslashes in decoded path or query
	if strings.Contains(path, "\\") || strings.Contains(u.RawQuery, "\\") {
		return def
	}
	// Path must start with single '/' and not '//' or '/\'
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "/\\") {
		return def
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return path
}
