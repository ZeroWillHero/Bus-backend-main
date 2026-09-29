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
