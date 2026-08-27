package middleware

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/devops-igor/amnezia-web-ui-go/internal/database"
)

var setupCompleted atomic.Bool

// InvalidateSetupCache resets the cached setup status flag.
func InvalidateSetupCache() {
	setupCompleted.Store(false)
}

// MarkSetupCompleted manually sets the cached setup status flag to true.
func MarkSetupCompleted() {
	setupCompleted.Store(true)
}

// IsSetupCompleted returns whether the panel setup has been completed.
func IsSetupCompleted() bool {
	return setupCompleted.Load()
}

var setupAllowedPrefixes = []string{
	"/static/",
	"/set_lang/",
}

var setupAllowedExact = map[string]bool{
	"/setup":          true,
	"/api/auth/setup": true,
	"/api/health":     true,
	"/api/version":    true,
	"/login":          true,
	"/logout":         true,
}

func isPathAllowedDuringSetup(path string) bool {
	clean := strings.TrimRight(path, "/")
	if clean == "" {
		clean = "/"
	}
	if setupAllowedExact[clean] || setupAllowedExact[path] {
		return true
	}
	for _, prefix := range setupAllowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// SetupRedirect creates middleware that enforces first-run wizard completion.
func SetupRedirect(db *database.DB) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			cleanPath := strings.TrimRight(path, "/")
			if cleanPath == "" {
				cleanPath = "/"
			}

			// 1. Fast-path: If setup is already known to be complete
			if setupCompleted.Load() {
				if cleanPath == "/setup" {
					http.Redirect(w, r, "/login", http.StatusFound)
					return
				}
				if cleanPath == "/api/auth/setup" {
					WriteJSONError(w, http.StatusForbidden, "forbidden", "Setup already completed")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// 2. Query DB to determine user count
			ctx := r.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			count, err := db.CountUsers(ctx)
			if err != nil {
				// If DB error occurs, proceed to allow recovery/logging
				next.ServeHTTP(w, r)
				return
			}

			if count > 0 {
				setupCompleted.Store(true)
				if cleanPath == "/setup" {
					http.Redirect(w, r, "/login", http.StatusFound)
					return
				}
				if cleanPath == "/api/auth/setup" {
					WriteJSONError(w, http.StatusForbidden, "forbidden", "Setup already completed")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// 3. User count is 0: Setup mode
			if isPathAllowedDuringSetup(path) {
				next.ServeHTTP(w, r)
				return
			}

			if isAPIRequest(r) {
				WriteJSONError(w, http.StatusForbidden, "forbidden", "Setup wizard required")
				return
			}

			http.Redirect(w, r, "/setup", http.StatusFound)
		})
	}
}
