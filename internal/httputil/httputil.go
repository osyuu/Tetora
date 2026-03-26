// Package httputil provides shared HTTP helpers for handler packages.
package httputil

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP extracts the client IP from the request, checking X-Forwarded-For first.
func ClientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
