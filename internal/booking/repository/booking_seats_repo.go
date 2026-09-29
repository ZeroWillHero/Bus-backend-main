package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/booking/domain"
)

type BookingSeatRepository struct {
	pool *pgxpool.Pool
}

func NewBookingSeatRepository(pool *pgxpool.Pool) *BookingSeatRepository {
	return &BookingSeatRepository{pool: pool}
}

func (r *BookingSeatRepository) CreateBulk(ctx context.Context, seats []*domain.BookingSeat) error {
	for _, seat := range seats {
		_, err := r.pool.Exec(ctx,
			`INSERT INTO booking.booking_seats (booking_id, seat_number, passenger_name, passenger_nic, fare_amount)
			 VALUES ($1, $2, $3, $4, $5)`,
			seat.BookingID, seat.SeatNumber, seat.PassengerName, seat.PassengerNic, seat.FareAmount,
		)
		if err != nil {
			return fmt.Errorf("create_bulk: %w", err)
		}
	}
	return nil
}

func (r *BookingSeatRepository) FindByBookingID(ctx context.Context, bookingID string) ([]*domain.BookingSeat, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, booking_id, seat_number, passenger_name, passenger_nic, fare_amount
		 FROM booking.booking_seats
		 WHERE booking_id = $1
		 ORDER BY seat_number`,
		bookingID,
	)
	if err != nil {
		return nil, fmt.Errorf("find_by_booking_id: %w", err)
	}
	defer rows.Close()

	var seats []*domain.BookingSeat
	for rows.Next() {
		var s domain.BookingSeat
		if err := rows.Scan(&s.ID, &s.BookingID, &s.SeatNumber, &s.PassengerName, &s.PassengerNic, &s.FareAmount); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		seats = append(seats, &s)
	}

	return seats, rows.Err()
}
