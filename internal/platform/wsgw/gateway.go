package wsgw

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

const (
	// MaxConnsPerIP caps concurrent seat-feed sockets from one client IP.
	MaxConnsPerIP = 10
	// MaxConnsTotal caps concurrent seat-feed sockets per API instance.
	MaxConnsTotal = 5000

	// clientBuffer is how many undelivered messages a client may queue
	// before it is dropped as a slow consumer.
	clientBuffer = 16
	// pingInterval/pingTimeout detect dead peers that never sent a close.
	pingInterval = 30 * time.Second
	pingTimeout  = 10 * time.Second
	// writeTimeout bounds a single frame write to the client.
	writeTimeout = 10 * time.Second
	// readLimit: clients only receive; anything larger than a control
	// frame's worth is abuse.
	readLimit = 4 * 1024

	channelPrefix = "trip:"
)

// Gateway handles WebSocket connections for live seat-status updates.
// One shared Redis pattern subscription (trip:*) is fanned out in-process to
// every connected client watching that trip; see hub.
type Gateway struct {
	redis       *redis.Client
	verifier    auth.Verifier
	log         *slog.Logger
	trustedHops int
	hub         *hub

	mu       sync.Mutex
	perIP    map[string]int
	total    int
	maxPerIP int
	maxTotal int
}

// NewGateway builds the gateway. trustedHops is TRUSTED_PROXY_HOPS, used to
// resolve the client IP for per-IP connection limits. Run must be started for
// clients to receive anything.
func NewGateway(rdb *redis.Client, verifier auth.Verifier, log *slog.Logger, trustedHops int) *Gateway {
	return &Gateway{
		redis:       rdb,
		verifier:    verifier,
		log:         log,
		trustedHops: trustedHops,
		hub:         newHub(rdb, log),
		perIP:       make(map[string]int),
		maxPerIP:    MaxConnsPerIP,
		maxTotal:    MaxConnsTotal,
	}
}

// Run owns the shared Redis subscription until ctx is cancelled, then closes
// every connected client with StatusGoingAway. Call it once, in a goroutine.
func (g *Gateway) Run(ctx context.Context) { g.hub.run(ctx) }

// HandleSeatsSubscription is the HTTP handler for WS upgrade at /v1/ws/trips/{tripID}/seats.
// The feed is public, like GET /v1/trips/{tripID}/seats, so passengers can watch
// seats before logging in. A JWT may still be passed as ?token=...; if one is
// sent it must be valid. Tokens in Sec-WebSocket-Protocol are not supported
// (browsers would need the server to echo the value back as the subprotocol).
func (g *Gateway) HandleSeatsSubscription(w http.ResponseWriter, r *http.Request) {
	tripID := r.PathValue("tripID")
	if tripID == "" {
		http.Error(w, "missing tripID", http.StatusBadRequest)
		return
	}
	// Anyone can subscribe, so only allow real trip ids.
	if _, err := uuid.Parse(tripID); err != nil {
		http.Error(w, "invalid tripID", http.StatusBadRequest)
		return
	}

	userID := "anonymous"
	if token := r.URL.Query().Get("token"); token != "" {
		// Any authenticated user may observe seat updates; never log the token.
		claims, err := g.verifier.Verify(token)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		userID = claims.Subject
	}

	ip := httpx.ClientIP(r, g.trustedHops)
	if !g.acquire(ip) {
		http.Error(w, "too many connections", http.StatusTooManyRequests)
		return
	}
	defer g.release(ip)

	cl := g.hub.subscribe(tripID)
	if cl == nil {
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	defer g.hub.unsubscribe(cl)

	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		g.log.Warn("websocket upgrade failed", "error", err)
		return
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(readLimit)

	// CloseRead runs the reader that processes ping/pong/close frames; ctx is
	// cancelled as soon as the peer closes or the connection breaks.
	ctx := c.CloseRead(r.Context())

	g.log.Info("seat feed client connected", "trip_id", tripID, "user_id", userID)
	defer g.log.Info("seat feed client disconnected", "trip_id", tripID, "user_id", userID)

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cl.done:
			_ = c.Close(cl.code, cl.reason)
			return
		case msg := <-cl.send:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, pingTimeout)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (g *Gateway) acquire(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.total >= g.maxTotal || g.perIP[ip] >= g.maxPerIP {
		return false
	}
	g.total++
	g.perIP[ip]++
	return true
}

func (g *Gateway) release(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.total--
	if g.perIP[ip]--; g.perIP[ip] <= 0 {
		delete(g.perIP, ip)
	}
}

// PublishSeatUpdate broadcasts a seat-status message to all subscribers of a trip's channel.
func (g *Gateway) PublishSeatUpdate(ctx context.Context, tripID string, message []byte) error {
	return g.redis.Publish(ctx, channelPrefix+tripID, message).Err()
}

// client is one socket's mailbox in the hub.
type client struct {
	tripID string
	send   chan []byte
	done   chan struct{} // closed when the hub drops the client
	code   websocket.StatusCode
	reason string
}

// hub holds the single Redis subscription and fans messages out to clients.
// Delivery never blocks: a client whose buffer is full is dropped.
type hub struct {
	redis *redis.Client
	log   *slog.Logger
	ready chan struct{} // closed once the first subscription is confirmed

	mu      sync.Mutex
	trips   map[string]map[*client]struct{}
	n       int
	stopped bool
}

func newHub(rdb *redis.Client, log *slog.Logger) *hub {
	return &hub{redis: rdb, log: log, ready: make(chan struct{}), trips: make(map[string]map[*client]struct{})}
}

func (h *hub) run(ctx context.Context) {
	defer h.stop()
	var once sync.Once
	backoff := time.Second
	for ctx.Err() == nil {
		err := h.listen(ctx, func() { once.Do(func() { close(h.ready) }) })
		if ctx.Err() != nil {
			return
		}
		h.log.Error("seat feed redis subscription failed; retrying", "error", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// listen subscribes once and relays until ctx ends or the subscription dies.
// go-redis reconnects a PubSub internally, so this only returns on errors it
// gave up on.
func (h *hub) listen(ctx context.Context, onReady func()) error {
	ps := h.redis.PSubscribe(ctx, channelPrefix+"*")
	defer func() { _ = ps.Close() }()
	if _, err := ps.Receive(ctx); err != nil {
		return err
	}
	onReady()
	ch := ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return errors.New("subscription channel closed")
			}
			if tripID, found := strings.CutPrefix(msg.Channel, channelPrefix); found {
				h.deliver(tripID, []byte(msg.Payload))
			}
		}
	}
}

func (h *hub) deliver(tripID string, payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for cl := range h.trips[tripID] {
		select {
		case cl.send <- payload:
		default:
			h.dropLocked(cl, websocket.StatusPolicyViolation, "slow consumer")
		}
	}
}

// subscribe registers a client for tripID, or returns nil once the hub has stopped.
func (h *hub) subscribe(tripID string) *client {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return nil
	}
	cl := &client{tripID: tripID, send: make(chan []byte, clientBuffer), done: make(chan struct{})}
	set := h.trips[tripID]
	if set == nil {
		set = make(map[*client]struct{})
		h.trips[tripID] = set
	}
	set[cl] = struct{}{}
	h.n++
	return cl
}

// unsubscribe is idempotent: the client may already have been dropped.
func (h *hub) unsubscribe(cl *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(cl)
}

func (h *hub) dropLocked(cl *client, code websocket.StatusCode, reason string) {
	if h.removeLocked(cl) {
		cl.code, cl.reason = code, reason
		close(cl.done)
	}
}

func (h *hub) removeLocked(cl *client) bool {
	set := h.trips[cl.tripID]
	if _, ok := set[cl]; !ok {
		return false
	}
	delete(set, cl)
	if len(set) == 0 {
		delete(h.trips, cl.tripID)
	}
	h.n--
	return true
}

func (h *hub) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopped = true
	for _, set := range h.trips {
		for cl := range set {
			h.dropLocked(cl, websocket.StatusGoingAway, "server shutting down")
		}
	}
}

// clients reports how many sockets are registered (for tests and metrics).
func (h *hub) clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}
