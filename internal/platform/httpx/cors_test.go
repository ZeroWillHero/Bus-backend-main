package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sl-bus/backend/internal/platform/httpx"
)

func serve(origins []string, method, origin string, preflight bool) *httptest.ResponseRecorder {
	h := httpx.CORS(origins)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	req := httptest.NewRequest(method, "/v1/auth/login", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if preflight {
		req.Header.Set("Access-Control-Request-Method", "POST")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCORS(t *testing.T) {
	if rec := serve(nil, "OPTIONS", "http://x.test", true); rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Code != http.StatusTeapot {
		t.Fatalf("empty allow-list must be a no-op, got %d %v", rec.Code, rec.Header())
	}
	allow := []string{"https://ops.example.com/"}
	rec := serve(allow, "OPTIONS", "https://ops.example.com", true)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "https://ops.example.com" {
		t.Fatalf("allowed preflight: %d %v", rec.Code, rec.Header())
	}
	if rec := serve(allow, "OPTIONS", "https://evil.test", true); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin must get no CORS headers")
	}
	rec = serve(allow, "POST", "https://ops.example.com", false)
	if rec.Code != http.StatusTeapot || rec.Header().Get("Access-Control-Allow-Origin") != "https://ops.example.com" {
		t.Fatalf("actual request: %d %v", rec.Code, rec.Header())
	}
}
