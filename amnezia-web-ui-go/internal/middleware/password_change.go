package middleware

import (
	"net/http"
	"strings"
)

var pwChangeAllowedPrefixes = []string{
	"/static/",
	"/set_lang/",
}

var pwChangeAllowedExact = map[string]bool{
	"/api/auth/change-password": true,
	"/api/auth/login":           true,
	"/api/auth/captcha":         true,
	"/change-password":          true,
	"/logout":                   true,
}

func isPathAllowedDuringPasswordChange(path string) bool {
	clean := strings.TrimRight(path, "/")
	if clean == "" {
		clean = "/"
	}
	if pwChangeAllowedExact[clean] || pwChangeAllowedExact[path] {
		return true
	}
	for _, prefix := range pwChangeAllowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// PasswordChangeRequired creates middleware that intercepts requests for users with mandatory password change flag.
func PasswordChangeRequired() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session := GetSession(r.Context())
			if session != nil && session.PasswordChangeRequired {
				if !isPathAllowedDuringPasswordChange(r.URL.Path) {
					if isAPIRequest(r) {
						WriteJSONErrorWithFlag(
							w,
							http.StatusForbidden,
							"password_change_required",
							"You must change your password before proceeding",
							true,
						)
						return
					}
					http.Redirect(w, r, "/change-password", http.StatusFound)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
