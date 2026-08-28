package router

import (
	"net/http"
	"time"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
	"github.com/devops-igor/amnezia-web-ui-go/internal/handlers"
)

// TemplateEngine aliases handlers.TemplateEngine for backwards compatibility.
type TemplateEngine = handlers.TemplateEngine

// GetTemplateEngine returns the singleton template engine instance.
func GetTemplateEngine() *TemplateEngine {
	return handlers.GetTemplateEngine()
}

// FormatBytes formats byte counts into human-readable strings.
func FormatBytes(n int64) string {
	return handlers.FormatBytes(n)
}

// FormatTime formats a time into standard display format.
func FormatTime(t time.Time) string {
	return handlers.FormatTime(t)
}

// RenderTemplate renders the specified template with standard context variables.
func RenderTemplate(w http.ResponseWriter, r *http.Request, db *database.DB, name string, data map[string]any) error {
	return handlers.RenderTemplate(w, r, db, name, data)
}

// CleanReferer strips any external domain/protocol from the referer to prevent open redirect vulnerabilities.
func CleanReferer(rawReferer string) string {
	return handlers.CleanReferer(rawReferer)
}
