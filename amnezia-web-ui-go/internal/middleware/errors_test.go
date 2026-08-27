package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSanitizeErrorMessage(t *testing.T) {
	tests := []struct {
		input    string
		fallback string
		want     string
	}{
		{
			input:    "Failed to open /home/igor/secret/panel.db",
			fallback: "Default Error",
			want:     "Failed to open ***",
		},
		{
			input:    "Connection refused at 192.168.1.100 port 5432",
			fallback: "Default Error",
			want:     "Connection refused at *** port 5432",
		},
		{
			input:    "Contact admin@example.com for access",
			fallback: "Default Error",
			want:     "Contact *** for access",
		},
		{
			input:    "Pointer error at 0xdeadbeef",
			fallback: "Default Error",
			want:     "Pointer error at ***",
		},
		{
			input:    "Database uri: postgres://user:password=supersecret@10.0.0.1",
			fallback: "Default Error",
			want:     "Database uri: postgres://user:password=***@***",
		},
		{
			input:    "",
			fallback: "Custom Fallback",
			want:     "Custom Fallback",
		},
		{
			input:    "/tmp/only/path",
			fallback: "Sanitized Fallback",
			want:     "Sanitized Fallback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SanitizeErrorMessage(tt.input, tt.fallback)
			if got != tt.want {
				t.Errorf("SanitizeErrorMessage(%q, %q) = %q, want %q", tt.input, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestWriteJSONError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSONError(w, http.StatusNotFound, "not_found", "User /var/users/123 not found")

	if w.Code != http.StatusNotFound {
		t.Errorf("got status %d, want %d", w.Code, http.StatusNotFound)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}

	if resp.Error != "not_found" {
		t.Errorf("got error %q, want %q", resp.Error, "not_found")
	}
	if strings.Contains(resp.Detail, "/var/users") {
		t.Errorf("detail was not sanitized: %q", resp.Detail)
	}
}

func TestWriteJSONErrorWithFlag(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSONErrorWithFlag(w, http.StatusForbidden, "password_change_required", "Must change password", true)

	if w.Code != http.StatusForbidden {
		t.Errorf("got status %d, want %d", w.Code, http.StatusForbidden)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}

	if resp.Error != "password_change_required" {
		t.Errorf("got error %q, want %q", resp.Error, "password_change_required")
	}
	if resp.PasswordChangeRequired == nil || !*resp.PasswordChangeRequired {
		t.Errorf("expected PasswordChangeRequired to be true")
	}
}

func TestRecovererAPI(t *testing.T) {
	panickingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("critical database failure at /data/panel.db")
	})

	handler := Recoverer(panickingHandler)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}

	if resp.Error != "internal_error" {
		t.Errorf("got error %q, want %q", resp.Error, "internal_error")
	}
	if resp.Detail != "An unexpected error occurred" {
		t.Errorf("got detail %q, want %q", resp.Detail, "An unexpected error occurred")
	}
}

func TestRecovererHTML(t *testing.T) {
	panickingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("render template panic")
	})

	handler := Recoverer(panickingHandler)

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want %d", w.Code, http.StatusInternalServerError)
	}

	body := w.Body.String()
	if !strings.Contains(body, "500 Internal Server Error") {
		t.Errorf("expected HTML 500 error page, got %q", body)
	}
}
