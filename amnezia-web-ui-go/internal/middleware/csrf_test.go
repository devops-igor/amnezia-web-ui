package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCSRFMiddleware(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	csrfMiddleware := CSRF(false)(okHandler)

	// 1. Safe GET request without cookie -> sets cookie and succeeds
	reqGet := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	wGet := httptest.NewRecorder()
	csrfMiddleware.ServeHTTP(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Errorf("expected 200 for GET, got %d", wGet.Code)
	}
	cookies := wGet.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == CSRFCookieName {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil || csrfCookie.Value == "" {
		t.Fatalf("expected CSRF cookie to be issued on GET request")
	}
	if csrfCookie.HttpOnly {
		t.Errorf("CSRF cookie must not be HttpOnly (client JS must read it)")
	}

	validToken := csrfCookie.Value

	// 2. State-mutating POST with matching header -> 200 OK
	reqPostValid := httptest.NewRequest(http.MethodPost, "/api/servers/add", nil)
	reqPostValid.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: validToken})
	reqPostValid.Header.Set(CSRFHeaderName, validToken)
	wPostValid := httptest.NewRecorder()
	csrfMiddleware.ServeHTTP(wPostValid, reqPostValid)

	if wPostValid.Code != http.StatusOK {
		t.Errorf("expected 200 for valid CSRF POST, got %d", wPostValid.Code)
	}

	// 3. State-mutating POST with matching form field -> 200 OK
	form := url.Values{}
	form.Set(CSRFFormField, validToken)
	form.Set("name", "server-1")
	reqPostForm := httptest.NewRequest(http.MethodPost, "/api/servers/add", strings.NewReader(form.Encode()))
	reqPostForm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPostForm.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: validToken})
	wPostForm := httptest.NewRecorder()
	csrfMiddleware.ServeHTTP(wPostForm, reqPostForm)

	if wPostForm.Code != http.StatusOK {
		t.Errorf("expected 200 for valid form CSRF POST, got %d", wPostForm.Code)
	}

	// 4. State-mutating POST with missing header/form -> 403 Forbidden
	reqPostMissing := httptest.NewRequest(http.MethodPost, "/api/servers/add", nil)
	reqPostMissing.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: validToken})
	wPostMissing := httptest.NewRecorder()
	csrfMiddleware.ServeHTTP(wPostMissing, reqPostMissing)

	if wPostMissing.Code != http.StatusForbidden {
		t.Errorf("expected 403 for missing CSRF header, got %d", wPostMissing.Code)
	}

	// 5. State-mutating POST with mismatched token -> 403 Forbidden
	reqPostMismatch := httptest.NewRequest(http.MethodPost, "/api/servers/add", nil)
	reqPostMismatch.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: validToken})
	reqPostMismatch.Header.Set(CSRFHeaderName, "invalid_csrf_token_value_here")
	wPostMismatch := httptest.NewRecorder()
	csrfMiddleware.ServeHTTP(wPostMismatch, reqPostMismatch)

	if wPostMismatch.Code != http.StatusForbidden {
		t.Errorf("expected 403 for mismatched CSRF token, got %d", wPostMismatch.Code)
	}

	// 6. Explicit exemptions -> 200 OK without CSRF header
	exemptions := []string{
		"/api/auth/login",
		"/api/auth/setup",
		"/api/share/xyz123token/auth",
	}
	for _, path := range exemptions {
		t.Run("Exempt: "+path, func(t *testing.T) {
			reqExempt := httptest.NewRequest(http.MethodPost, path, nil)
			wExempt := httptest.NewRecorder()
			csrfMiddleware.ServeHTTP(wExempt, reqExempt)
			if wExempt.Code != http.StatusOK {
				t.Errorf("expected 200 for exempt path %s, got %d", path, wExempt.Code)
			}
		})
	}
}

func TestIsCSRFExempt(t *testing.T) {
	if !IsCSRFExempt(http.MethodGet, "/api/servers") {
		t.Errorf("GET should always be exempt")
	}
	if !IsCSRFExempt(http.MethodPost, "/api/auth/login") {
		t.Errorf("POST /api/auth/login should be exempt")
	}
	if !IsCSRFExempt(http.MethodPost, "/api/auth/setup") {
		t.Errorf("POST /api/auth/setup should be exempt")
	}
	if !IsCSRFExempt(http.MethodPost, "/api/share/abc123token/auth") {
		t.Errorf("POST /api/share/{token}/auth should be exempt")
	}
	if IsCSRFExempt(http.MethodPost, "/api/servers/add") {
		t.Errorf("POST /api/servers/add should NOT be exempt")
	}
	if IsCSRFExempt(http.MethodDelete, "/api/users/123/delete") {
		t.Errorf("DELETE /api/users/123/delete should NOT be exempt")
	}
}
