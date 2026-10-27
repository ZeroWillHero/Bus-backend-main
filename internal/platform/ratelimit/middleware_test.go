package ratelimit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sl-bus/backend/internal/platform/auth"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

func TestMiddlewareLimitsAndResetsPerWindow(t *testing.T) {
	counter := NewMemoryCounter()
	clock := time.Unix(1_700_000_000, 0).Truncate(time.Minute).Add(15 * time.Second)
	counter.now = func() time.Time { return clock }
	rule := Rule{Name: "t", Limit: 3, Window: time.Minute}
	h := middleware(counter, rule, ByClientIP(0), func() time.Time { return clock })(okHandler())

	do := func(remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	for i := 0; i < 3; i++ {
		if rec := do("1.2.3.4:1"); rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: status %d", i+1, rec.Code)
		}
	}
	rec := do("1.2.3.4:1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request: status %d, want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"PLATFORM_RATE_LIMITED"`) {
		t.Fatalf("body = %s", rec.Body)
	}
	if got := rec.Header().Get("Retry-After"); got != "45" {
		t.Fatalf("Retry-After = %q, want 45", got)
	}
	// Other keys have their own bucket.
	if rec := do("5.6.7.8:1"); rec.Code != http.StatusNoContent {
		t.Fatalf("other IP: status %d", rec.Code)
	}

	clock = clock.Add(45 * time.Second) // next window
	if rec := do("1.2.3.4:1"); rec.Code != http.StatusNoContent {
		t.Fatalf("after window: status %d", rec.Code)
	}
}

type failingCounter struct{ Counter }

func (failingCounter) Incr(context.Context, string, time.Duration) (int64, error) {
	return 0, errors.New("redis down")
}

func TestMiddlewareFailsOpen(t *testing.T) {
	h := Middleware(failingCounter{}, Rule{Name: "t", Limit: 1, Window: time.Minute}, ByClientIP(0))(okHandler())
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status %d, want pass-through", rec.Code)
		}
	}
}

func TestByUserID(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if _, ok := ByUserID(r); ok {
		t.Fatal("no claims should skip limiting")
	}
	verifier := stubVerifier{claims: &auth.Claims{}}
	verifier.claims.Subject = "user-1"
	var got string
	h := auth.Middleware(verifier)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = ByUserID(r)
	}))
	r.Header.Set("Authorization", "Bearer x")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got != "user-1" {
		t.Fatalf("ByUserID = %q", got)
	}
}

type stubVerifier struct{ claims *auth.Claims }

func (s stubVerifier) Verify(string) (*auth.Claims, error) { return s.claims, nil }
