package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devops-igor/amnezia-web-ui-go/internal/models"
)

func TestPasswordChangeRequiredMiddleware(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	middlewareHandler := PasswordChangeRequired()(okHandler)

	// 1. Unauthenticated or password change NOT required -> 200 OK
	reqNormal := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	ctxNormal := WithSession(reqNormal.Context(), &models.SessionData{
		UserID:                 "u-1",
		PasswordChangeRequired: false,
	})
	wNormal := httptest.NewRecorder()
	middlewareHandler.ServeHTTP(wNormal, reqNormal.WithContext(ctxNormal))
	if wNormal.Code != http.StatusOK {
		t.Errorf("expected 200 for user with password change not required, got %d", wNormal.Code)
	}

	// 2. Password change REQUIRED -> Allowed paths succeed
	allowedPaths := []string{
		"/api/auth/change-password",
		"/api/auth/captcha",
		"/api/auth/login",
		"/change-password",
		"/logout",
		"/static/main.js",
		"/set_lang/ru",
	}

	for _, p := range allowedPaths {
		t.Run("Allowed: "+p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			ctx := WithSession(req.Context(), &models.SessionData{
				UserID:                 "u-must-change",
				PasswordChangeRequired: true,
			})
			w := httptest.NewRecorder()
			middlewareHandler.ServeHTTP(w, req.WithContext(ctx))
			if w.Code != http.StatusOK {
				t.Errorf("expected 200 for allowed path %s, got %d", p, w.Code)
			}
		})
	}

	// 3. Password change REQUIRED -> Blocked API path returns 403 JSON
	reqBlockedAPI := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	ctxBlocked := WithSession(reqBlockedAPI.Context(), &models.SessionData{
		UserID:                 "u-must-change",
		PasswordChangeRequired: true,
	})
	wBlockedAPI := httptest.NewRecorder()
	middlewareHandler.ServeHTTP(wBlockedAPI, reqBlockedAPI.WithContext(ctxBlocked))
	if wBlockedAPI.Code != http.StatusForbidden {
		t.Errorf("expected 403 for blocked API path, got %d", wBlockedAPI.Code)
	}

	var errResp ErrorResponse
	if err := json.Unmarshal(wBlockedAPI.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if errResp.Error != "password_change_required" || errResp.PasswordChangeRequired == nil || !*errResp.PasswordChangeRequired {
		t.Errorf("expected password_change_required error response, got %+v", errResp)
	}

	// 4. Password change REQUIRED -> Blocked HTML path redirects to /change-password
	reqBlockedHTML := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqBlockedHTML.Header.Set("Accept", "text/html")
	wBlockedHTML := httptest.NewRecorder()
	middlewareHandler.ServeHTTP(wBlockedHTML, reqBlockedHTML.WithContext(ctxBlocked))
	if wBlockedHTML.Code != http.StatusFound || wBlockedHTML.Header().Get("Location") != "/change-password" {
		t.Errorf("expected 302 redirect to /change-password, got status %d location %q", wBlockedHTML.Code, wBlockedHTML.Header().Get("Location"))
	}
}
