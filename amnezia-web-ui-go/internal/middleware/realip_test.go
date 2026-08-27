package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRealIPResolver(t *testing.T) {
	tests := []struct {
		name           string
		trustedProxies string
		remoteAddr     string
		headers        map[string]string
		wantIP         string
	}{
		{
			name:           "Empty trusted proxies ignores XFF",
			trustedProxies: "",
			remoteAddr:     "192.168.1.50:12345",
			headers:        map[string]string{"X-Forwarded-For": "203.0.113.195"},
			wantIP:         "192.168.1.50",
		},
		{
			name:           "Untrusted remoteAddr ignores XFF and X-Real-IP",
			trustedProxies: "127.0.0.1, 10.0.0.0/8",
			remoteAddr:     "192.168.1.50:12345",
			headers: map[string]string{
				"X-Forwarded-For": "203.0.113.195",
				"X-Real-IP":       "203.0.113.196",
			},
			wantIP: "192.168.1.50",
		},
		{
			name:           "Trusted IP exact match honors XFF",
			trustedProxies: "127.0.0.1, 172.18.0.2",
			remoteAddr:     "172.18.0.2:45678",
			headers:        map[string]string{"X-Forwarded-For": "203.0.113.195"},
			wantIP:         "203.0.113.195",
		},
		{
			name:           "Trusted CIDR honors XFF multiple proxies",
			trustedProxies: "172.18.0.0/16",
			remoteAddr:     "172.18.0.5:45678",
			headers:        map[string]string{"X-Forwarded-For": "198.51.100.42, 172.18.0.5"},
			wantIP:         "198.51.100.42",
		},
		{
			name:           "Trusted CIDR falls back to X-Real-IP if XFF empty",
			trustedProxies: "10.0.0.0/8",
			remoteAddr:     "10.1.2.3:5555",
			headers:        map[string]string{"X-Real-IP": "198.51.100.99"},
			wantIP:         "198.51.100.99",
		},
		{
			name:           "Trusted CIDR without proxy headers uses direct IP",
			trustedProxies: "10.0.0.0/8",
			remoteAddr:     "10.1.2.3:5555",
			headers:        map[string]string{},
			wantIP:         "10.1.2.3",
		},
		{
			name:           "IPv6 trusted proxy honors XFF",
			trustedProxies: "::1, 2001:db8::/32",
			remoteAddr:     "[2001:db8::1]:12345",
			headers:        map[string]string{"X-Forwarded-For": "203.0.113.50"},
			wantIP:         "203.0.113.50",
		},
		{
			name:           "Invalid trusted proxy entries are gracefully ignored",
			trustedProxies: "not-an-ip, 127.0.0.1/32,   ",
			remoteAddr:     "127.0.0.1:9090",
			headers:        map[string]string{"X-Forwarded-For": "203.0.113.77"},
			wantIP:         "203.0.113.77",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewRealIPResolver(tt.trustedProxies)
			req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			var capturedIP string
			handler := resolver.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedIP = GetClientIPFromRequest(r)
			}))

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if capturedIP != tt.wantIP {
				t.Errorf("got IP %q, want %q", capturedIP, tt.wantIP)
			}
		})
	}
}

func TestNilRequestSafety(t *testing.T) {
	resolver := NewRealIPResolver("127.0.0.1")
	if ip := resolver.ResolveClientIP(nil); ip != "" {
		t.Errorf("expected empty IP for nil request, got %q", ip)
	}
	if ip := GetClientIPFromRequest(nil); ip != "" {
		t.Errorf("expected empty IP for nil request, got %q", ip)
	}
	if resolver.IsTrusted(nil) {
		t.Errorf("nil IP should not be trusted")
	}
}
