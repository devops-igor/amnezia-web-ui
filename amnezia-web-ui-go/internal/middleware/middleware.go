package middleware

import (
	"context"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

type contextKey string

const (
	sessionKey  contextKey = "amnezia_session"
	clientIPKey contextKey = "amnezia_client_ip"
	// #nosec G101 -- Context key identifier string, not a secret credential
	csrfTokenKey contextKey = "amnezia_csrf_ctx"
)

// WithSession returns a new context containing the given SessionData.
func WithSession(ctx context.Context, session *models.SessionData) context.Context {
	return context.WithValue(ctx, sessionKey, session)
}

// GetSession retrieves the SessionData from context, returning nil if not found.
func GetSession(ctx context.Context) *models.SessionData {
	if ctx == nil {
		return nil
	}
	if val, ok := ctx.Value(sessionKey).(*models.SessionData); ok {
		return val
	}
	return nil
}

// WithClientIP returns a new context containing the client IP address.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey, ip)
}

// GetClientIP retrieves the client IP address from context.
func GetClientIP(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if val, ok := ctx.Value(clientIPKey).(string); ok {
		return val
	}
	return ""
}

// WithCSRFToken returns a new context containing the CSRF token.
func WithCSRFToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, csrfTokenKey, token)
}

// GetCSRFToken retrieves the CSRF token from context.
func GetCSRFToken(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if val, ok := ctx.Value(csrfTokenKey).(string); ok {
		return val
	}
	return ""
}
