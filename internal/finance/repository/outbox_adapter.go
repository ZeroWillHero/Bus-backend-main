package repository

import (
	"context"

	bookingrepo "github.com/sl-bus/backend/internal/booking/repository"
	"github.com/sl-bus/backend/internal/finance/domain/ports"
)

// OutboxAdapter bridges booking.OutboxRepository to finance.ports.OutboxRepository
type OutboxAdapter struct {
	bookingOutbox *bookingrepo.OutboxRepository
}

func NewOutboxAdapter(bookingOutbox *bookingrepo.OutboxRepository) ports.OutboxRepository {
	return &OutboxAdapter{bookingOutbox: bookingOutbox}
}

func (a *OutboxAdapter) PollUnprocessed(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	return a.bookingOutbox.PollUnprocessed(ctx, limit)
}

func (a *OutboxAdapter) MarkProcessed(ctx context.Context, eventID string) error {
	return a.bookingOutbox.MarkProcessed(ctx, eventID)
}
