package middleware

import (
	"net"
	"net/http"
	"strings"
)

// RealIPResolver inspects incoming HTTP requests to determine the true client IP address,
// respecting proxy headers only when the direct peer is within trusted proxy CIDRs or IPs.
type RealIPResolver struct {
	trustedCIDRs []*net.IPNet
	trustedIPs   []net.IP
}

// NewRealIPResolver parses a comma-separated list of IP addresses or CIDR notations.
func NewRealIPResolver(trustedProxies string) *RealIPResolver {
	resolver := &RealIPResolver{
		trustedCIDRs: make([]*net.IPNet, 0),
		trustedIPs:   make([]net.IP, 0),
	}

	for _, entry := range strings.Split(trustedProxies, ",") {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "/") {
			if _, ipNet, err := net.ParseCIDR(trimmed); err == nil {
				resolver.trustedCIDRs = append(resolver.trustedCIDRs, ipNet)
			}
		} else {
			if ip := net.ParseIP(trimmed); ip != nil {
				resolver.trustedIPs = append(resolver.trustedIPs, ip)
			}
		}
	}

	return resolver
}

// IsTrusted returns true if the given IP address is in the trusted proxy list.
func (r *RealIPResolver) IsTrusted(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, tip := range r.trustedIPs {
		if tip.Equal(ip) {
			return true
		}
	}
	for _, cidr := range r.trustedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// ExtractIP extracts the host IP from a host:port or bare IP string.
func ExtractIP(remoteAddr string) (string, net.IP) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	host = strings.Trim(host, "[]")
	parsed := net.ParseIP(host)
	return host, parsed
}

// ResolveClientIP determines the client IP address from the request.
func (r *RealIPResolver) ResolveClientIP(req *http.Request) string {
	if req == nil {
		return ""
	}

	directHost, directIP := ExtractIP(req.RemoteAddr)

	// If no trusted proxies are configured or the direct peer is not trusted, use direct peer address.
	if (len(r.trustedCIDRs) == 0 && len(r.trustedIPs) == 0) || directIP == nil || !r.IsTrusted(directIP) {
		return directHost
	}

	// Direct peer is trusted, check headers
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		// Leftmost IP is the original client IP
		clientCandidate := strings.TrimSpace(parts[0])
		clientHost, parsedClient := ExtractIP(clientCandidate)
		if parsedClient != nil {
			return clientHost
		}
	}

	if xrip := req.Header.Get("X-Real-IP"); xrip != "" {
		clientHost, parsedClient := ExtractIP(strings.TrimSpace(xrip))
		if parsedClient != nil {
			return clientHost
		}
	}

	return directHost
}

// Middleware creates an HTTP middleware that injects the resolved client IP into the request context.
func (r *RealIPResolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		clientIP := r.ResolveClientIP(req)
		ctx := WithClientIP(req.Context(), clientIP)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

// RealIP creates a middleware using the provided trusted CIDRs and IPs.
func RealIP(trustedCIDRs []*net.IPNet, trustedIPs []net.IP) func(next http.Handler) http.Handler {
	resolver := &RealIPResolver{
		trustedCIDRs: trustedCIDRs,
		trustedIPs:   trustedIPs,
	}
	return resolver.Middleware
}

// GetClientIPFromRequest retrieves the client IP from the request context or falls back to RemoteAddr.
func GetClientIPFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ip := GetClientIP(r.Context()); ip != "" {
		return ip
	}
	host, _ := ExtractIP(r.RemoteAddr)
	return host
}
