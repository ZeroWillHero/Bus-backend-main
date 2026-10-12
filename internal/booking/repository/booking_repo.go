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

func (r *BookingRepository) UpdateStatus(ctx context.Context, id string, status domain.BookingStatus, confirmedAt, cancelledAt *time.Time) (*domain.Booking, error) {
	var b domain.Booking
	now := time.Now()

	err := r.pool.QueryRow(ctx,
		`UPDATE booking.bookings
		 SET status = $1, updated_at = $2, confirmed_at = $3, cancelled_at = $4
		 WHERE id = $5
		 RETURNING id, trip_id, passenger_id, status, total_amount, currency, hold_expires_at,
		          created_at, updated_at, confirmed_at, cancelled_at`,
		status, now, confirmedAt, cancelledAt, id,
	).Scan(&b.ID, &b.TripID, &b.PassengerID, &b.Status, &b.TotalAmount, &b.Currency, &b.HoldExpiresAt,
		&b.CreatedAt, &b.UpdatedAt, &b.ConfirmedAt, &b.CancelledAt)
	if err != nil {
		return nil, fmt.Errorf("update_status: %w", err)
	}

	return &b, nil
}
