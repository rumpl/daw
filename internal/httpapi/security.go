package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// CSRFHeader carries the per-process token issued by /api/bootstrap. Because
// it is a custom header, a cross-site form or img request can never set it,
// and there is no CORS policy that would let a cross-origin fetch add it.
const CSRFHeader = "X-DAW-CSRF"

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// hostPolicy decides which Host / forwarded-host values this server answers.
//
// Loopback hosts are always allowed. Additional hostnames (a Tailscale Serve
// DNS name) must be configured explicitly via TAILSCALE_HOSTNAMES: we never
// infer trust from the request itself.
type hostPolicy struct {
	extraHosts map[string]bool
}

func newHostPolicy(extra []string) *hostPolicy {
	m := map[string]bool{}
	for _, h := range extra {
		h = strings.TrimSpace(strings.ToLower(h))
		if h != "" {
			m[h] = true
		}
	}
	return &hostPolicy{extraHosts: m}
}

func hostnameOnly(hostport string) string {
	h := strings.ToLower(strings.TrimSpace(hostport))
	if h == "" {
		return ""
	}
	if strings.HasPrefix(h, "[") { // IPv6 literal
		if i := strings.Index(h, "]"); i > 0 {
			return h[1:i]
		}
	}
	if hh, _, err := net.SplitHostPort(h); err == nil {
		return hh
	}
	return h
}

func isLoopbackHost(h string) bool {
	switch h {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func (p *hostPolicy) allowedHost(h string) bool {
	h = hostnameOnly(h)
	if h == "" {
		return false
	}
	return isLoopbackHost(h) || p.extraHosts[h]
}

// remoteIsLoopback reports whether the immediate peer connected over loopback.
// Forwarded headers are only trusted in that case: behind `tailscale serve`
// the proxy is on this very machine, so a loopback peer is the only situation
// in which X-Forwarded-* can be believed.
func remoteIsLoopback(r *http.Request) bool {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// effectiveOrigin derives the origin the browser believes it is talking to.
// It prefers forwarded headers only for loopback peers, and never trusts a
// forwarded host that the policy does not allow.
func (p *hostPolicy) effectiveOrigin(r *http.Request) (string, bool) {
	host := r.Host
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if remoteIsLoopback(r) {
		if fh := firstValue(r.Header.Get("X-Forwarded-Host")); fh != "" {
			if !p.allowedHost(fh) {
				return "", false
			}
			host = fh
			scheme = "https"
			if fp := firstValue(r.Header.Get("X-Forwarded-Proto")); fp != "" {
				scheme = strings.ToLower(fp)
			}
		}
	}
	if !p.allowedHost(host) {
		return "", false
	}
	return scheme + "://" + strings.ToLower(host), true
}

func firstValue(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// checkHost rejects any request whose Host (or trusted forwarded host) is not
// loopback or an explicitly configured Tailscale Serve hostname.
func (s *Server) checkHost(r *http.Request) bool {
	if remoteIsLoopback(r) {
		if fh := firstValue(r.Header.Get("X-Forwarded-Host")); fh != "" && !s.hosts.allowedHost(fh) {
			return false
		}
	}
	return s.hosts.allowedHost(r.Host)
}

// checkOrigin enforces same-origin for mutations. It accepts a request when:
//   - Sec-Fetch-Site says same-origin/none, or
//   - Origin matches the effective origin derived from the (trusted) host.
//
// A cross-site Sec-Fetch-Site or a mismatching Origin is always rejected.
func (s *Server) checkOrigin(r *http.Request) bool {
	switch strings.ToLower(r.Header.Get("Sec-Fetch-Site")) {
	case "cross-site", "same-site":
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		// No Origin header: only same-origin non-CORS requests omit it, and
		// the CSRF token check below still applies.
		return origin == ""
	}
	want, ok := s.hosts.effectiveOrigin(r)
	if !ok {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	got := strings.ToLower(u.Scheme + "://" + u.Host)
	if got == want {
		return true
	}
	// Behind Tailscale Serve the browser's origin is https://<name> while the
	// backend sees http://127.0.0.1:4788. The forwarded-host branch of
	// effectiveOrigin covers that; additionally accept an allowed host with
	// either scheme so a Serve deployment without X-Forwarded-Proto works.
	if s.hosts.allowedHost(u.Host) && !isLoopbackHost(hostnameOnly(u.Host)) {
		return true
	}
	return false
}

// checkCSRF compares the per-process token in constant time.
func (s *Server) checkCSRF(r *http.Request) bool {
	got := r.Header.Get(CSRFHeader)
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.csrf)) == 1
}

// checkTailscaleUser enforces ALLOWED_TAILSCALE_USERS for proxied requests.
// The Tailscale-User-Login header is trustworthy only because this backend
// listens on 127.0.0.1: nothing but the local `tailscale serve` process can
// reach it, so nothing else can forge the header.
func (s *Server) checkTailscaleUser(r *http.Request) bool {
	if len(s.allowedTSUsers) == 0 {
		return true
	}
	login := strings.ToLower(strings.TrimSpace(r.Header.Get("Tailscale-User-Login")))
	if login == "" {
		// A direct local request (no Serve in front) carries no header; it is
		// already constrained by the loopback bind.
		fh := firstValue(r.Header.Get("X-Forwarded-Host"))
		return fh == ""
	}
	return s.allowedTSUsers[login]
}

// securityHeaders applies the reviewed production header set. The CSP allows
// only same-origin scripts/styles built into the binary (no CDN), permits the
// inline style attribute React sets for layout, and blocks framing, objects
// and remote images.
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		head := w.Header()
		head.Set("Content-Security-Policy",
			"default-src 'none'; "+
				"script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"font-src 'self'; "+
				"connect-src 'self'; "+
				"form-action 'none'; "+
				"frame-ancestors 'none'; "+
				"base-uri 'none'; "+
				"object-src 'none'")
		head.Set("Referrer-Policy", "no-referrer")
		head.Set("X-Content-Type-Options", "nosniff")
		head.Set("X-Frame-Options", "DENY")
		head.Set("Cross-Origin-Opener-Policy", "same-origin")
		head.Set("Cross-Origin-Resource-Policy", "same-origin")
		head.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=(), payment=(), usb=()")
		h.ServeHTTP(w, r)
	})
}
