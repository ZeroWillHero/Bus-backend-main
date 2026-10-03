package wsgw

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/sl-bus/backend/internal/platform/auth"
)

// Gateway handles WebSocket connections for live seat-status updates.
// Clients subscribe to trip:<trip_id> channels on opening, and the gateway
// relays messages from Redis pub/sub to all connected clients on that trip.
type Gateway struct {
	redis    *redis.Client
	verifier auth.Verifier
	log      *slog.Logger
}

func NewGateway(redis *redis.Client, verifier auth.Verifier, log *slog.Logger) *Gateway {
	return &Gateway{
		redis:    redis,
		verifier: verifier,
		log:      log,
	}
}

// HandleSeatsSubscription is the HTTP handler for WS upgrade at /v1/ws/trips/{tripID}/seats.
// The feed is public, like GET /v1/trips/{tripID}/seats, so passengers can watch
// seats before logging in. A JWT may still be passed via query param (token=...)
// or Sec-WebSocket-Protocol header; if one is sent it must be valid.
func (g *Gateway) HandleSeatsSubscription(w http.ResponseWriter, r *http.Request) {
	tripID := r.PathValue("tripID")
	if tripID == "" {
		http.Error(w, "missing tripID", http.StatusBadRequest)
		return
	}
	// Anyone can subscribe, so only allow real trip ids as Redis channel names.
	if _, err := uuid.Parse(tripID); err != nil {
		http.Error(w, "invalid tripID", http.StatusBadRequest)
		return
	}

	// Extract optional JWT from query param or Sec-WebSocket-Protocol header
	token := r.URL.Query().Get("token")
	if token == "" {
		// Fallback to Sec-WebSocket-Protocol header
		protocol := r.Header.Get("Sec-WebSocket-Protocol")
		if protocol != "" {
			token = protocol
		}
	}

	userID := "anonymous"
	if token != "" {
		// We don't enforce role here (any authenticated user can observe seat updates)
		claims, err := g.verifier.Verify(token)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		userID = claims.Subject
	}

	// Upgrade connection
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		g.log.Error("websocket upgrade failed", "error", err)
		return
	}
	defer func() { _ = c.Close(websocket.StatusInternalError, "internal error") }()

	// Set read/write deadlines for graceful shutdown (15min idle timeout per plan)
	c.SetReadLimit(64 * 1024) // 64KB max message size
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()

	// Subscribe to Redis channel for this trip
	channel := fmt.Sprintf("trip:%s", tripID)
	sub := g.redis.Subscribe(r.Context(), channel)
	defer func() { _ = sub.Close() }()

	// Create a context that times out after 15 minutes of inactivity
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()

	g.log.Info("client subscribed", "trip_id", tripID, "user_id", userID)

	// Relay messages from Redis to WebSocket
	for {
		// Use a select to watch both the Redis subscription and the timeout
		select {
		case <-ctx.Done():
			g.log.Info("client connection idle timeout", "trip_id", tripID)
			_ = c.Close(websocket.StatusNormalClosure, "idle timeout")
			return

		default:
			// Read next message from Redis with a timeout
			msg, err := sub.ReceiveMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				g.log.Error("redis receive error", "error", err)
				return
			}

			// Send to WebSocket client
			err = c.Write(ctx, websocket.MessageText, []byte(msg.Payload))
			if err != nil {
				g.log.Info("websocket write error", "error", err)
				return
			}

			// Reset the idle timer by recreating context
			ctx, cancel = context.WithTimeout(r.Context(), 15*time.Minute)
			defer cancel()
		}
	}
}

// PublishSeatUpdate broadcasts a seat-status message to all subscribers of a trip's channel.
// Called from fleet.SeatInventoryRepository after seat state changes.
func (g *Gateway) PublishSeatUpdate(ctx context.Context, tripID string, message []byte) error {
	channel := fmt.Sprintf("trip:%s", tripID)
	return g.redis.Publish(ctx, channel, message).Err()
}
