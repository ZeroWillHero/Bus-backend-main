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

	"github.com/alicebob/miniredis/v2"
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

// newServer serves the gateway on the real route backed by miniredis, with
// the hub running and its subscription confirmed.
func newServer(t *testing.T) (*httptest.Server, *Gateway, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	g := NewGateway(rdb, fakeVerifier{}, slog.New(slog.NewTextHandler(io.Discard, nil)), 0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-g.hub.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("hub never subscribed")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ws/trips/{tripID}/seats", g.HandleSeatsSubscription)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, g, mr
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const seatsPath = "/v1/ws/trips/" + tripID + "/seats"

func dial(t *testing.T, srv *httptest.Server, path string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	return websocket.Dial(ctx, url, nil)
}

func TestHandleSeatsSubscription(t *testing.T) {
	srv, _, _ := newServer(t)

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

func TestClientReceivesPublishedMessage(t *testing.T) {
	srv, g, mr := newServer(t)
	c, _, err := dial(t, srv, seatsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	waitFor(t, "client registered", func() bool { return g.hub.clients() == 1 })

	want := `{"seat":"A1","status":"held"}`
	mr.Publish("trip:"+tripID, want)
	mr.Publish("trip:00000000-0000-0000-0000-000000000000", `{"other":"trip"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, got, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText || string(got) != want {
		t.Fatalf("got %v %q, want text %q", typ, got, want)
	}
}

func TestDisconnectRemovesClient(t *testing.T) {
	srv, g, _ := newServer(t)
	for _, graceful := range []bool{true, false} {
		c, _, err := dial(t, srv, seatsPath)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "client registered", func() bool { return g.hub.clients() == 1 })
		if graceful {
			_ = c.Close(websocket.StatusNormalClosure, "bye")
		} else {
			_ = c.CloseNow()
		}
		waitFor(t, "client removed", func() bool { return g.hub.clients() == 0 })
		waitFor(t, "connection slot released", func() bool {
			g.mu.Lock()
			defer g.mu.Unlock()
			return g.total == 0 && len(g.perIP) == 0
		})
	}
}

func TestPerIPLimit(t *testing.T) {
	srv, g, _ := newServer(t)
	g.maxPerIP = 2
	for i := 0; i < 2; i++ {
		c, _, err := dial(t, srv, seatsPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.CloseNow() }()
	}
	c, resp, _ := dial(t, srv, seatsPath)
	if c != nil {
		_ = c.CloseNow()
	}
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("third connection: resp = %v, want 429", resp)
	}
}

func TestGlobalLimit(t *testing.T) {
	srv, g, _ := newServer(t)
	g.maxTotal = 1
	c, _, err := dial(t, srv, seatsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	c2, resp, _ := dial(t, srv, seatsPath)
	if c2 != nil {
		_ = c2.CloseNow()
	}
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second connection: resp = %v, want 429", resp)
	}
}

func TestSlowConsumerDroppedWithoutBlockingFast(t *testing.T) {
	_, g, mr := newServer(t)
	slow := g.hub.subscribe(tripID) // never drained
	fast := g.hub.subscribe(tripID)

	// Publish in lockstep with the fast reader: the slow client's buffer
	// fills and it is dropped, while the fast one gets every message.
	const n = clientBuffer * 3
	for i := 0; i < n; i++ {
		mr.Publish("trip:"+tripID, "m")
		select {
		case <-fast.send:
		case <-time.After(5 * time.Second):
			t.Fatalf("fast client blocked at message %d", i)
		}
	}
	select {
	case <-slow.done:
	default:
		t.Fatal("slow client was not dropped")
	}
	if slow.code != websocket.StatusPolicyViolation || slow.reason != "slow consumer" {
		t.Fatalf("slow client closed with %v %q", slow.code, slow.reason)
	}
	if g.hub.clients() != 1 {
		t.Fatalf("hub clients = %d, want 1 (fast only)", g.hub.clients())
	}
}

func TestSlowSocketIsClosed(t *testing.T) {
	srv, g, mr := newServer(t)
	c, _, err := dial(t, srv, seatsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	waitFor(t, "client registered", func() bool { return g.hub.clients() == 1 })
	// Payloads big enough to fill the socket buffers while the client reads nothing.
	big := strings.Repeat("x", 256*1024)
	for i := 0; i < 200 && g.hub.clients() > 0; i++ {
		mr.Publish("trip:"+tripID, big)
	}
	waitFor(t, "slow socket dropped", func() bool { return g.hub.clients() == 0 })
}

func TestSingleRedisSubscriptionForManyClients(t *testing.T) {
	srv, g, mr := newServer(t)
	const n = 8
	for i := 0; i < n; i++ {
		c, _, err := dial(t, srv, seatsPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.CloseNow() }()
	}
	waitFor(t, "clients registered", func() bool { return g.hub.clients() == n })
	if got := mr.PubSubNumPat(); got != 1 {
		t.Fatalf("redis pattern subscriptions = %d, want 1", got)
	}
	if got := mr.PubSubChannels(""); len(got) != 0 {
		t.Fatalf("unexpected per-channel subscriptions: %v", got)
	}
}

func TestShutdownClosesClients(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	g := NewGateway(rdb, fakeVerifier{}, slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { g.Run(ctx); close(done) }()
	<-g.hub.ready
	cl := g.hub.subscribe(tripID)
	cancel()
	<-done
	select {
	case <-cl.done:
	default:
		t.Fatal("client not closed on shutdown")
	}
	if g.hub.subscribe(tripID) != nil {
		t.Fatal("subscribe after shutdown should fail")
	}
}
