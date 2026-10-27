package ratelimit

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

// Per-endpoint limits for the unauthenticated auth surface and booking
// creation (wired in cmd/server/main.go).
var (
	LoginLimit         = Rule{Name: "login", Limit: 20, Window: time.Minute}
	RegisterLimit      = Rule{Name: "register", Limit: 5, Window: time.Hour}
	MFAVerifyLimit     = Rule{Name: "mfa_verify", Limit: 20, Window: time.Minute}
	TokenRefreshLimit  = Rule{Name: "token_refresh", Limit: 60, Window: time.Minute}
	BookingCreateLimit = Rule{Name: "booking_create", Limit: 20, Window: 10 * time.Minute}
)

// Rule is a fixed-window limit: at most Limit requests per key per Window.
// Name namespaces the counter keys so rules never share a bucket.
type Rule struct {
	Name   string
	Limit  int64
	Window time.Duration
}

// KeyFunc extracts the bucket key (client IP, user id, ...) from a request.
// ok=false skips limiting for that request.
type KeyFunc func(r *http.Request) (key string, ok bool)

// Middleware enforces rule per key using a clock-aligned fixed window, so
// Retry-After is exact. Over the limit it responds 429 PLATFORM_RATE_LIMITED.
// If the counter errors (Redis down) it fails open and logs, so an outage
// can't block login.
func Middleware(c Counter, rule Rule, key KeyFunc) func(http.Handler) http.Handler {
	return middleware(c, rule, key, time.Now)
}

func middleware(c Counter, rule Rule, key KeyFunc, now func() time.Time) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k, ok := key(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			t := now()
			window := t.UnixNano() / int64(rule.Window)
			bucket := "mw:" + rule.Name + ":" + k + ":" + strconv.FormatInt(window, 10)
			n, err := c.Incr(r.Context(), bucket, rule.Window)
			if err != nil {
				slog.Warn("rate limiter unavailable; allowing request", "rule", rule.Name, "error", err)
				next.ServeHTTP(w, r)
				return
			}
			if n > rule.Limit {
				end := time.Unix(0, (window+1)*int64(rule.Window))
				secs := int64((end.Sub(t) + time.Second - 1) / time.Second)
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
				httpx.WriteError(w, http.StatusTooManyRequests, httpx.APIError{
					Code:    "PLATFORM_RATE_LIMITED",
					Message: "Too many requests. Please try again later.",
				}, nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ByClientIP keys on httpx.ClientIP with the given trusted proxy hop count.
func ByClientIP(trustedHops int) KeyFunc {
	return func(r *http.Request) (string, bool) {
		return httpx.ClientIP(r, trustedHops), true
	}
}

// ByUserID keys on the authenticated subject; it must run after
// auth.Middleware. Requests without claims are not limited here (the auth
// gate rejects them anyway).
func ByUserID(r *http.Request) (string, bool) {
	claims, ok := auth.FromContext(r.Context())
	if !ok || claims.Subject == "" {
		return "", false
	}
	return claims.Subject, true
}
