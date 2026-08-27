package middleware

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"
)

var (
	filePathRe    = regexp.MustCompile(`/(?:[a-zA-Z0-9._-]+/)+[a-zA-Z0-9._-]+|[a-zA-Z]:\\[a-zA-Z0-9._\-\\]+`)
	ipAddressRe   = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	emailRe       = regexp.MustCompile(`\b[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}\b`)
	hexAddressRe  = regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`)
	secretParamRe = regexp.MustCompile(`(?i)(password|passwd|secret|token|key|api_key|auth)=([^\s&@]+)`)
)

// ErrorResponse defines the standard structured error JSON payload.
type ErrorResponse struct {
	Error                  string `json:"error"`
	Detail                 string `json:"detail"`
	PasswordChangeRequired *bool  `json:"password_change_required,omitempty"`
}

// WriteJSONError sends a sanitized structured JSON error response.
func WriteJSONError(w http.ResponseWriter, statusCode int, errCode string, detail string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Error:  errCode,
		Detail: SanitizeErrorMessage(detail, "An unexpected error occurred"),
	})
}

// WriteJSONErrorWithFlag sends a structured error response with optional password_change_required flag.
func WriteJSONErrorWithFlag(w http.ResponseWriter, statusCode int, errCode string, detail string, pwChangeReq bool) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Error:                  errCode,
		Detail:                 SanitizeErrorMessage(detail, "An unexpected error occurred"),
		PasswordChangeRequired: &pwChangeReq,
	})
}

// SanitizeErrorMessage strips internal paths, private IPs, emails, hex pointers, and credentials.
func SanitizeErrorMessage(message string, fallback string) string {
	if strings.TrimSpace(message) == "" {
		if fallback == "" {
			return "An unexpected error occurred"
		}
		return fallback
	}

	sanitized := message
	sanitized = secretParamRe.ReplaceAllString(sanitized, "$1=***")
	sanitized = filePathRe.ReplaceAllString(sanitized, "***")
	sanitized = ipAddressRe.ReplaceAllString(sanitized, "***")
	sanitized = emailRe.ReplaceAllString(sanitized, "***")
	sanitized = hexAddressRe.ReplaceAllString(sanitized, "***")

	trimmed := strings.TrimSpace(sanitized)
	if trimmed == "" || strings.Trim(trimmed, "* ") == "" {
		if fallback == "" {
			return "An unexpected error occurred"
		}
		return fallback
	}

	return sanitized
}

// Recoverer is a middleware that recovers from panics, logs the stack trace, and returns a 500 response.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rvr := recover(); rvr != nil {
				if rvr == http.ErrAbortHandler {
					panic(rvr)
				}

				stack := debug.Stack()
				// #nosec G706 -- Standard structured error logging for panic recovery
				slog.Error("HTTP request panicked",
					"panic", fmt.Sprintf("%v", rvr),
					"method", r.Method,
					"path", r.URL.Path,
					"stack", string(stack),
				)

				if isAPIRequest(r) {
					WriteJSONError(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred")
					return
				}

				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("<h1>500 Internal Server Error</h1><p>An unexpected error occurred.</p>"))
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// isAPIRequest checks if a request is targeting an API route or expects JSON.
func isAPIRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html") {
		return true
	}
	return false
}
