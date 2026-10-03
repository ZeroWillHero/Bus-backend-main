package wsgw

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/redis/go-redis/v9"

	"github.com/sl-bus/backend/internal/platform/auth"
)

const tripID = "6f1c2a7e-3b9d-4e2a-9f51-0c8d7b2e4a10"

type fakeVerifier struct{}

func (fakeVerifier) Verify(token string) (*auth.Claims, error) {
	if token != "good" {
		return nil, errors.New("bad token")
	}
	c := &auth.Claims{}
	c.Subject = "user-1"
	return c, nil
}

// newServer serves the gateway on the real route. Redis points at a closed
// port: the handshake doesn't touch it, so only what happens before and at
// the upgrade is exercised here.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	g := NewGateway(rdb, fakeVerifier{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ws/trips/{tripID}/seats", g.HandleSeatsSubscription)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func dial(t *testing.T, srv *httptest.Server, path string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	return websocket.Dial(ctx, url, nil)
}

func TestHandleSeatsSubscription(t *testing.T) {
	srv := newServer(t)

	cases := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{"anonymous viewer", "/v1/ws/trips/" + tripID + "/seats", http.StatusSwitchingProtocols},
		{"valid token", "/v1/ws/trips/" + tripID + "/seats?token=good", http.StatusSwitchingProtocols},
		{"invalid token", "/v1/ws/trips/" + tripID + "/seats?token=bad", http.StatusUnauthorized},
		{"malformed trip id", "/v1/ws/trips/not-a-uuid/seats", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, resp, err := dial(t, srv, tc.path)
			if c != nil {
				defer func() { _ = c.CloseNow() }()
			}
			if resp == nil {
				t.Fatalf("no handshake response: %v", err)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d (err: %v)", resp.StatusCode, tc.wantStatus, err)
			}
		})
	}
}
