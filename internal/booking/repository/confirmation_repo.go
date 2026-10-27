package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/booking/domain"
	"github.com/sl-bus/backend/internal/platform/db"
)

// SeatTxConfirmer is the slice of fleet's seat inventory the confirm
// transaction needs (implemented by fleet's SeatInventoryRepository).
type SeatTxConfirmer interface {
	// ConfirmSeatsInTx books every seat or returns ErrSeatUnavailable.
	ConfirmSeatsInTx(ctx context.Context, tx pgx.Tx, tripID string, seatNumbers []string, bookingID string) error
	// SeatsBooked notifies live clients after commit.
	SeatsBooked(ctx context.Context, tripID string, seatNumbers []string)
}

// ConfirmationRepository is the unit of work behind domain.BookingConfirmer.
type ConfirmationRepository struct {
	pool   *pgxpool.Pool
	seats  SeatTxConfirmer
	outbox *OutboxRepository
}

var _ domain.BookingConfirmer = (*ConfirmationRepository)(nil)

func NewConfirmationRepository(pool *pgxpool.Pool, seats SeatTxConfirmer, outbox *OutboxRepository) *ConfirmationRepository {
	return &ConfirmationRepository{pool: pool, seats: seats, outbox: outbox}
}

// CommitConfirmation writes seats, booking status, ticket and outbox event in
// ONE transaction; any failure rolls all of it back.
//
// RLS identity: the transaction starts in the CALLER's scope, so the booking
// update and ticket insert are still bounded by the passenger's RLS policies,
// exactly as the single-statement repositories do. Only the seat inventory
// statement switches to system scope (fleet writes inventory as the platform
// today, since a passenger can't update an operator's trip rows) and then
// switches back. The settings are SET LOCAL, so nothing outlives the tx.
// The outbox insert is allowed for any identity (WITH CHECK true).
func (r *ConfirmationRepository) CommitConfirmation(ctx context.Context, c domain.ConfirmCommit) (*domain.Booking, *domain.Ticket, error) {
	var booking *domain.Booking
	var ticket domain.Ticket
	err := db.InScopedTx(ctx, r.pool, db.ScopeCaller, func(tx pgx.Tx) error {
		if err := db.UseScope(ctx, tx, db.ScopeSystem); err != nil {
			return err
		}
		if err := r.seats.ConfirmSeatsInTx(ctx, tx, c.TripID, c.SeatNumbers, c.BookingID); err != nil {
			return err
		}
		if err := db.UseScope(ctx, tx, db.ScopeCaller); err != nil {
			return err
		}

		var err error
		confirmedAt := c.ConfirmedAt
		booking, err = scanTransition(tx.QueryRow(ctx, transitionSQL, c.BookingID,
			domain.BookingStatusPaymentPending, domain.BookingStatusConfirmed, &confirmedAt, nil))
		if err != nil {
			return err
		}

		if err := tx.QueryRow(ctx,
			`INSERT INTO booking.tickets (id, booking_id, qr_token)
			 VALUES ($1, $2, $3)
			 RETURNING id, booking_id, qr_token, issued_at, checked_in_at`,
			c.TicketID, c.BookingID, c.QRToken,
		).Scan(&ticket.ID, &ticket.BookingID, &ticket.QRToken, &ticket.IssuedAt, &ticket.CheckedInAt); err != nil {
			return fmt.Errorf("create_ticket: %w", err)
		}

		return r.outbox.Write(ctx, tx, c.EventType, c.Payload)
	})
	if err != nil {
		return nil, nil, err
	}
	r.seats.SeatsBooked(ctx, c.TripID, c.SeatNumbers)
	return booking, &ticket, nil
}
