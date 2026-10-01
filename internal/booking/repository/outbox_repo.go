package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OutboxRepository struct {
	pool *pgxpool.Pool
}

func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

// Write writes an outbox event atomically within the caller's transaction.
// The tx parameter should be a pgx.Tx from within a WithTenantContext.
// For now, we accept context as tx since WithTenantContext isn't yet integrated.
func (r *OutboxRepository) Write(ctx context.Context, tx interface{}, eventType string, payload interface{}) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	// If tx is provided, use it; otherwise use the pool (not ideal but temporary)
	if pgxTx, ok := tx.(pgx.Tx); ok {
		_, err = pgxTx.Exec(ctx,
			`INSERT INTO platform.outbox_events (event_type, payload) VALUES ($1, $2)`,
			eventType, payloadJSON)
	} else {
		_, err = r.pool.Exec(ctx,
			`INSERT INTO platform.outbox_events (event_type, payload) VALUES ($1, $2)`,
			eventType, payloadJSON)
	}

	if err != nil {
		return fmt.Errorf("write outbox: %w", err)
	}

	return nil
}

// DefaultOutboxLease is how long a polled event stays invisible to other
// workers. If the claiming worker crashes or the handler fails, the event
// becomes claimable again after the lease, which doubles as a retry backoff.
const DefaultOutboxLease = 60 * time.Second

// PollUnprocessed CLAIMS up to limit unprocessed events and returns them.
//
// A plain "SELECT ... LIMIT" would hand the same rows to every worker. The
// claim uses FOR UPDATE SKIP LOCKED so concurrent pollers never receive the
// same row, and stamps claimed_until so the claim outlives this statement (the
// row lock alone is released as soon as the statement commits). Delivery stays
// at-least-once: handlers must be idempotent.
func (r *OutboxRepository) PollUnprocessed(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	rows, err := r.pool.Query(ctx, `
		WITH claimed AS (
			SELECT id FROM platform.outbox_events
			WHERE processed_at IS NULL
			  AND (claimed_until IS NULL OR claimed_until < now())
			ORDER BY created_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE platform.outbox_events e
		SET claimed_until = now() + make_interval(secs => $2),
		    attempts = e.attempts + 1
		FROM claimed
		WHERE e.id = claimed.id
		RETURNING e.id, e.event_type, e.payload, e.created_at, e.attempts
	`, limit, DefaultOutboxLease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("poll unprocessed: %w", err)
	}
	defer rows.Close()

	var events []map[string]interface{}
	for rows.Next() {
		var id, eventType string
		var payload []byte
		var createdAt time.Time
		var attempts int

		if err := rows.Scan(&id, &eventType, &payload, &createdAt, &attempts); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}

		var payloadMap map[string]interface{}
		if err := json.Unmarshal(payload, &payloadMap); err != nil {
			return nil, fmt.Errorf("unmarshal payload: %w", err)
		}

		events = append(events, map[string]interface{}{
			"id":         id,
			"event_type": eventType,
			"payload":    payloadMap,
			"created_at": createdAt,
			"attempts":   attempts,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	// UPDATE ... RETURNING does not preserve the CTE's ORDER BY.
	sort.SliceStable(events, func(i, j int) bool {
		return events[i]["created_at"].(time.Time).Before(events[j]["created_at"].(time.Time))
	})
	return events, nil
}

// MarkProcessed marks an outbox event as processed.
// Called after an event has been successfully relayed.
func (r *OutboxRepository) MarkProcessed(ctx context.Context, eventID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE platform.outbox_events
		SET processed_at = now()
		WHERE id = $1
	`, eventID)
	if err != nil {
		return fmt.Errorf("mark processed: %w", err)
	}
	return nil
}
