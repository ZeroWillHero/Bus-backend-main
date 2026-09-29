package repository

import (
	"context"
	"encoding/json"
	"fmt"

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

// PollUnprocessed retrieves unprocessed outbox events up to the limit.
// Used by the worker to relay events to Phase 4/5 handlers.
func (r *OutboxRepository) PollUnprocessed(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, event_type, payload, created_at
		FROM platform.outbox_events
		WHERE processed_at IS NULL
		ORDER BY created_at ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("poll unprocessed: %w", err)
	}
	defer rows.Close()

	var events []map[string]interface{}
	for rows.Next() {
		var id, eventType string
		var payload []byte
		var createdAt interface{}

		if err := rows.Scan(&id, &eventType, &payload, &createdAt); err != nil {
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
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

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
