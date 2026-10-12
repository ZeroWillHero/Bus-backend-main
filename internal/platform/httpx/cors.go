package httpx

import (
	"net/http"
	"strings"
)

// CORS allows browser clients (the operator web app) served from one of the
// given origins to call the API. An empty list disables CORS entirely, which
// is the safe default; "*" allows any origin and is meant for local dev only
// (the API authenticates with bearer tokens, not cookies, so credentials are
// never sent cross-site either way).
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	any := false
	for _, o := range allowedOrigins {
		o = strings.TrimSpace(strings.TrimRight(o, "/"))
		switch o {
		case "":
		case "*":
			any = true
		default:
			allowed[o] = struct{}{}
		}
	}
	return func(next http.Handler) http.Handler {
		if !any && len(allowed) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			_, ok := allowed[origin]
			if !ok && !any {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Expose-Headers", "X-Request-Id")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ParseOrigins splits a comma-separated CORS_ALLOWED_ORIGINS value.
func ParseOrigins(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return strings.Split(v, ",")
}
