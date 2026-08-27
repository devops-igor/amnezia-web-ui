package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

const (
	// CSRFCookieName is the standard cookie name for double-submit CSRF protection.
	CSRFCookieName = "csrftoken"
	// CSRFHeaderName is the request header containing the client's CSRF token.
	CSRFHeaderName = "X-CSRF-Token"
	// CSRFFormField is the form field containing the client's CSRF token.
	CSRFFormField = "csrf_token"
)

// GenerateCSRFToken generates a cryptographically secure 32-byte (64 hex characters) random string.
func GenerateCSRFToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// IsCSRFExempt checks if an endpoint is explicitly exempted from CSRF validation per specification.
func IsCSRFExempt(method string, path string) bool {
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch && method != http.MethodDelete {
		return true
	}

	cleanPath := strings.TrimRight(path, "/")
	if cleanPath == "" {
		cleanPath = "/"
	}

	if cleanPath == "/api/auth/login" || cleanPath == "/api/auth/setup" {
		return true
	}

	// Exempt: POST /api/share/{token}/auth
	if strings.HasPrefix(cleanPath, "/api/share/") && strings.HasSuffix(cleanPath, "/auth") {
		return true
	}

	return false
}

// isSafeHTTPMethod returns true for read-only HTTP methods.
func isSafeHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, "TRACE":
		return true
	default:
		return false
	}
}

// CSRF creates double-submit cookie CSRF validation middleware.
func CSRF(secure bool) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(CSRFCookieName)
			var token string

			if err == nil && cookie.Value != "" {
				token = cookie.Value
			} else {
				// Generate new CSRF token if cookie is missing
				var genErr error
				token, genErr = GenerateCSRFToken()
				if genErr == nil {
					// #nosec G124 -- Double-submit CSRF cookie must be accessible by client-side JavaScript for AJAX
					http.SetCookie(w, &http.Cookie{
						Name:     CSRFCookieName,
						Value:    token,
						Path:     "/",
						HttpOnly: false, // Must be readable by client-side JavaScript for AJAX requests
						Secure:   secure,
						SameSite: http.SameSiteLaxMode,
					})
				}
			}

			// Inject CSRF token into request context
			r = r.WithContext(WithCSRFToken(r.Context(), token))

			// Check if method is safe or exempt
			if isSafeHTTPMethod(r.Method) || IsCSRFExempt(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			// State-mutating request: must validate token
			if token == "" {
				WriteJSONError(w, http.StatusForbidden, "forbidden", "CSRF token mismatch or missing")
				return
			}

			clientToken := r.Header.Get(CSRFHeaderName)
			if clientToken == "" {
				clientToken = r.Header.Get(strings.ToLower(CSRFHeaderName))
			}
			if clientToken == "" {
				// Check form value if content type is form urlencoded or multipart
				ct := r.Header.Get("Content-Type")
				if strings.HasPrefix(ct, "application/x-www-form-urlencoded") || strings.HasPrefix(ct, "multipart/form-data") {
					_ = r.ParseForm()
					clientToken = r.FormValue(CSRFFormField)
				}
			}

			if clientToken == "" {
				WriteJSONError(w, http.StatusForbidden, "forbidden", "CSRF token mismatch or missing")
				return
			}

			// Constant-time comparison
			if len(token) != len(clientToken) || subtle.ConstantTimeCompare([]byte(token), []byte(clientToken)) != 1 {
				WriteJSONError(w, http.StatusForbidden, "forbidden", "CSRF token mismatch or missing")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
