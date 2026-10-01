// Package outbox relays platform.outbox_events to their consumers.
//
// Delivery is at-least-once: an event is only marked processed after its
// handler succeeds, and an unprocessed event is re-delivered once its claim
// lease expires. Handlers therefore MUST be idempotent (the ledger is, via a
// unique index on the journal source).
package outbox

import (
	"context"
	"fmt"
	"log/slog"
)

// Source is the persistence side of the outbox.
type Source interface {
	// PollUnprocessed claims up to limit events for this worker.
	PollUnprocessed(ctx context.Context, limit int) ([]map[string]interface{}, error)
	MarkProcessed(ctx context.Context, eventID string) error
}

// Handler consumes one event. Returning an error leaves the event unprocessed
// so it is retried after its lease.
type Handler func(ctx context.Context, payload map[string]interface{}) error

type Relay struct {
	Source    Source
	Handlers  map[string]Handler
	Log       *slog.Logger
	BatchSize int
}

type Stats struct {
	Processed int // handled and marked processed
	Failed    int // handler error or panic: left for retry
	Skipped   int // no handler registered: marked processed so it does not pile up
}

// RunOnce polls one batch and dispatches it. A malformed event or a panicking
// handler is contained to that event; it never takes the worker down.
func (r *Relay) RunOnce(ctx context.Context) (Stats, error) {
	var st Stats
	batch := r.BatchSize
	if batch <= 0 {
		batch = 10
	}
	events, err := r.Source.PollUnprocessed(ctx, batch)
	if err != nil {
		return st, fmt.Errorf("poll outbox: %w", err)
	}

	for _, ev := range events {
		id, _ := ev["id"].(string)
		eventType, _ := ev["event_type"].(string)
		payload, _ := ev["payload"].(map[string]interface{})
		if id == "" || eventType == "" || payload == nil {
			r.Log.Warn("malformed outbox event; leaving for retry", "event_id", id, "event_type", eventType)
			st.Failed++
			continue
		}

		h, ok := r.Handlers[eventType]
		if !ok {
			r.Log.Warn("no handler for outbox event type; marking processed", "event_id", id, "event_type", eventType)
			if err := r.Source.MarkProcessed(ctx, id); err != nil {
				r.Log.Error("mark processed failed", "event_id", id, "error", err)
				st.Failed++
				continue
			}
			st.Skipped++
			continue
		}

		if err := r.dispatch(ctx, h, payload); err != nil {
			r.Log.Error("outbox handler failed; will retry after lease",
				"event_id", id, "event_type", eventType, "error", err)
			st.Failed++
			continue
		}
		if err := r.Source.MarkProcessed(ctx, id); err != nil {
			// Handled but not recorded: it will be redelivered. Safe because handlers are idempotent.
			r.Log.Error("mark processed failed; event will be redelivered", "event_id", id, "error", err)
			st.Failed++
			continue
		}
		st.Processed++
	}
	return st, nil
}

func (r *Relay) dispatch(ctx context.Context, h Handler, payload map[string]interface{}) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("handler panicked: %v", rec)
		}
	}()
	return h(ctx, payload)
}
