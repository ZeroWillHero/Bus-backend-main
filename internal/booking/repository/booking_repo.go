package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/platform/db"

	"github.com/sl-bus/backend/internal/booking/domain"
)

type BookingRepository struct {
	pool db.Querier
}

func NewBookingRepository(pool *pgxpool.Pool) *BookingRepository {
	return &BookingRepository{pool: db.NewScoped(pool, db.ScopeCaller)}
}

func (r *BookingRepository) Create(ctx context.Context, b *domain.Booking) (*domain.Booking, error) {
	var createdAt, updatedAt time.Time

	err := r.pool.QueryRow(ctx,
		`INSERT INTO booking.bookings (trip_id, passenger_id, status, total_amount, currency, hold_expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, trip_id, passenger_id, status, total_amount, currency, hold_expires_at,
		          created_at, updated_at, confirmed_at, cancelled_at`,
		b.TripID, b.PassengerID, b.Status, b.TotalAmount, b.Currency, b.HoldExpiresAt,
	).Scan(&b.ID, &b.TripID, &b.PassengerID, &b.Status, &b.TotalAmount, &b.Currency, &b.HoldExpiresAt,
		&createdAt, &updatedAt, &b.ConfirmedAt, &b.CancelledAt)
	if err != nil {
		return nil, fmt.Errorf("create_booking: %w", err)
	}

	b.CreatedAt = createdAt
	b.UpdatedAt = updatedAt
	return b, nil
}

func (r *BookingRepository) FindByID(ctx context.Context, id string) (*domain.Booking, error) {
	var b domain.Booking

	err := r.pool.QueryRow(ctx,
		`SELECT id, trip_id, passenger_id, status, total_amount, currency, hold_expires_at,
		        created_at, updated_at, confirmed_at, cancelled_at
		 FROM booking.bookings
		 WHERE id = $1`,
		id,
	).Scan(&b.ID, &b.TripID, &b.PassengerID, &b.Status, &b.TotalAmount, &b.Currency, &b.HoldExpiresAt,
		&b.CreatedAt, &b.UpdatedAt, &b.ConfirmedAt, &b.CancelledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrBookingNotFound // absent, or hidden from this caller by RLS
	}
	if err != nil {
		return nil, fmt.Errorf("find_by_id: %w", err)
	}

	return &b, nil
}

func (r *BookingRepository) FindByPassenger(ctx context.Context, passengerID string, cursor string, limit int) ([]*domain.Booking, string, error) {
	// Simple pagination without cursor for now — TODO: implement cursor-based pagination
	rows, err := r.pool.Query(ctx,
		`SELECT id, trip_id, passenger_id, status, total_amount, currency, hold_expires_at,
		        created_at, updated_at, confirmed_at, cancelled_at
		 FROM booking.bookings
		 WHERE passenger_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`,
		passengerID, limit,
	)
	if err != nil {
		return nil, "", fmt.Errorf("find_by_passenger: %w", err)
	}
	defer rows.Close()

	var bookings []*domain.Booking
	for rows.Next() {
		var b domain.Booking
		if err := rows.Scan(&b.ID, &b.TripID, &b.PassengerID, &b.Status, &b.TotalAmount, &b.Currency, &b.HoldExpiresAt,
			&b.CreatedAt, &b.UpdatedAt, &b.ConfirmedAt, &b.CancelledAt); err != nil {
			return nil, "", fmt.Errorf("scan: %w", err)
		}
		bookings = append(bookings, &b)
	}

	return bookings, "", rows.Err()
}

const bookingColumns = `id, trip_id, passenger_id, status, total_amount, currency, hold_expires_at,
		          created_at, updated_at, confirmed_at, cancelled_at`

// transitionSQL is a compare-and-set on status: it only changes a booking that
// is still in the status the caller read, so two racing requests can never
// both act on the same state.
const transitionSQL = `UPDATE booking.bookings
		 SET status = $3, updated_at = now(),
		     confirmed_at = COALESCE($4, confirmed_at),
		     cancelled_at = COALESCE($5, cancelled_at)
		 WHERE id = $1 AND status = $2
		 RETURNING ` + bookingColumns

// TransitionStatus moves a booking from -> to only if it is still `from`.
// Zero rows means another request changed it first: ErrConcurrentUpdate.
func (r *BookingRepository) TransitionStatus(ctx context.Context, id string, from, to domain.BookingStatus, confirmedAt, cancelledAt *time.Time) (*domain.Booking, error) {
	return scanTransition(r.pool.QueryRow(ctx, transitionSQL, id, from, to, confirmedAt, cancelledAt))
}

func scanTransition(row pgx.Row) (*domain.Booking, error) {
	var b domain.Booking
	err := row.Scan(&b.ID, &b.TripID, &b.PassengerID, &b.Status, &b.TotalAmount, &b.Currency, &b.HoldExpiresAt,
		&b.CreatedAt, &b.UpdatedAt, &b.ConfirmedAt, &b.CancelledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrConcurrentUpdate
	}
	if err != nil {
		return nil, fmt.Errorf("transition_status: %w", err)
	}
	return &b, nil
}

// CountActiveHolds counts the passenger's unexpired held/payment_pending bookings.
func (r *BookingRepository) CountActiveHolds(ctx context.Context, passengerID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM booking.bookings
		 WHERE passenger_id = $1
		   AND status IN ('held', 'payment_pending')
		   AND hold_expires_at > now()`, passengerID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count_active_holds: %w", err)
	}
	return n, nil
}
