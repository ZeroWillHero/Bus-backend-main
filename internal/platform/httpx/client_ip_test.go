package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	cases := []struct {
		name string
		hops int
		xff  []string
		want string
	}{
		{"no proxy ignores header", 0, []string{"1.1.1.1"}, "10.0.0.9"},
		{"no proxy no header", 0, nil, "10.0.0.9"},
		{"one hop takes right-most", 1, []string{"203.0.113.7"}, "203.0.113.7"},
		{"one hop ignores spoofed left-most", 1, []string{"6.6.6.6, 203.0.113.7"}, "203.0.113.7"},
		{"one hop across repeated headers", 1, []string{"6.6.6.6", "7.7.7.7, 203.0.113.7"}, "203.0.113.7"},
		{"two hops", 2, []string{"6.6.6.6, 203.0.113.7, 172.18.0.5"}, "203.0.113.7"},
		{"header shorter than hops falls back to peer", 2, []string{"203.0.113.7"}, "10.0.0.9"},
		{"missing header falls back to peer", 1, nil, "10.0.0.9"},
		{"garbage entry falls back to peer", 1, []string{"1.1.1.1, not-an-ip"}, "10.0.0.9"},
		{"ipv6", 1, []string{"2001:db8::1"}, "2001:db8::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = "10.0.0.9:51234"
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(r, tc.hops); got != tc.want {
				t.Fatalf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
