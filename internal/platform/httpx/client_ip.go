package httpx

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the caller's IP address for per-IP rate limiting.
//
// trustedHops is the number of reverse proxies in front of the API that
// append to X-Forwarded-For (TRUSTED_PROXY_HOPS: 0 in dev, 1 behind Caddy).
// With 0 the header is ignored entirely and the TCP peer is used. With N it
// takes the Nth entry from the right — the address the outermost trusted
// proxy saw — and never the left-most, which the client controls and can
// spoof. If the header is shorter than N or the entry isn't an IP, it falls
// back to the TCP peer.
func ClientIP(r *http.Request, trustedHops int) string {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	if trustedHops <= 0 {
		return peer
	}

	var entries []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		for _, e := range strings.Split(h, ",") {
			if e = strings.TrimSpace(e); e != "" {
				entries = append(entries, e)
			}
		}
	}
	idx := len(entries) - trustedHops
	if idx < 0 {
		return peer
	}
	ip := net.ParseIP(entries[idx])
	if ip == nil {
		return peer
	}
	return ip.String()
}
