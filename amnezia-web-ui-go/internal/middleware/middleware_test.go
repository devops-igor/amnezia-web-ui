package middleware

import (
	"context"
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestContextHelpers(t *testing.T) {
	// Empty context
	ctx := context.Background()
	if sess := GetSession(ctx); sess != nil {
		t.Errorf("expected nil session from empty context, got %+v", sess)
	}
	var nilCtx context.Context
	if sess := GetSession(nilCtx); sess != nil {
		t.Errorf("expected nil session from nil context, got %+v", sess)
	}
	if ip := GetClientIP(ctx); ip != "" {
		t.Errorf("expected empty IP from empty context, got %q", ip)
	}
	if ip := GetClientIP(nilCtx); ip != "" {
		t.Errorf("expected empty IP from nil context, got %q", ip)
	}
	if csrf := GetCSRFToken(ctx); csrf != "" {
		t.Errorf("expected empty CSRF from empty context, got %q", csrf)
	}
	if csrf := GetCSRFToken(nilCtx); csrf != "" {
		t.Errorf("expected empty CSRF from nil context, got %q", csrf)
	}

	// Populated context
	sampleSession := &models.SessionData{
		UserID:   "u-123",
		Username: "admin",
		Role:     models.RoleAdmin,
	}

	ctx = WithSession(ctx, sampleSession)
	ctx = WithClientIP(ctx, "192.168.1.100")
	ctx = WithCSRFToken(ctx, "csrf-token-abc-123")

	if sess := GetSession(ctx); sess == nil || sess.UserID != "u-123" {
		t.Errorf("GetSession failed, got %+v", sess)
	}
	if ip := GetClientIP(ctx); ip != "192.168.1.100" {
		t.Errorf("GetClientIP failed, got %q", ip)
	}
	if csrf := GetCSRFToken(ctx); csrf != "csrf-token-abc-123" {
		t.Errorf("GetCSRFToken failed, got %q", csrf)
	}
}
