package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/booking/domain"
)

type TicketRepository struct {
	pool *pgxpool.Pool
}

func NewTicketRepository(pool *pgxpool.Pool) *TicketRepository {
	return &TicketRepository{pool: pool}
}

func (r *TicketRepository) Create(ctx context.Context, t *domain.Ticket) (*domain.Ticket, error) {
	var issuedAt time.Time

	err := r.pool.QueryRow(ctx,
		`INSERT INTO booking.tickets (booking_id, qr_token)
		 VALUES ($1, $2)
		 RETURNING id, booking_id, qr_token, issued_at, checked_in_at`,
		t.BookingID, t.QRToken,
	).Scan(&t.ID, &t.BookingID, &t.QRToken, &issuedAt, &t.CheckedInAt)
	if err != nil {
		return nil, fmt.Errorf("create_ticket: %w", err)
	}

	t.IssuedAt = issuedAt
	return t, nil
}

func (r *TicketRepository) FindByBookingID(ctx context.Context, bookingID string) (*domain.Ticket, error) {
	var t domain.Ticket

	err := r.pool.QueryRow(ctx,
		`SELECT id, booking_id, qr_token, issued_at, checked_in_at
		 FROM booking.tickets
		 WHERE booking_id = $1`,
		bookingID,
	).Scan(&t.ID, &t.BookingID, &t.QRToken, &t.IssuedAt, &t.CheckedInAt)
	if err != nil {
		return nil, fmt.Errorf("find_by_booking_id: %w", err)
	}

	return &t, nil
}

func (r *TicketRepository) FindByQRToken(ctx context.Context, token string) (*domain.Ticket, error) {
	var t domain.Ticket

	err := r.pool.QueryRow(ctx,
		`SELECT id, booking_id, qr_token, issued_at, checked_in_at
		 FROM booking.tickets
		 WHERE qr_token = $1`,
		token,
	).Scan(&t.ID, &t.BookingID, &t.QRToken, &t.IssuedAt, &t.CheckedInAt)
	if err != nil {
		return nil, fmt.Errorf("find_by_qr_token: %w", err)
	}

	return &t, nil
}

func (r *TicketRepository) MarkCheckedIn(ctx context.Context, id string) (*domain.Ticket, error) {
	var t domain.Ticket
	now := time.Now()

	err := r.pool.QueryRow(ctx,
		`UPDATE booking.tickets
		 SET checked_in_at = $1
		 WHERE id = $2 AND checked_in_at IS NULL
		 RETURNING id, booking_id, qr_token, issued_at, checked_in_at`,
		now, id,
	).Scan(&t.ID, &t.BookingID, &t.QRToken, &t.IssuedAt, &t.CheckedInAt)
	if err != nil {
		return nil, fmt.Errorf("mark_checked_in: %w", err)
	}

	return &t, nil
}
