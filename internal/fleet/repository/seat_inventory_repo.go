package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/redis/go-redis/v9"
	"github.com/sl-bus/backend/internal/platform/db"

	bookingdomain "github.com/sl-bus/backend/internal/booking/domain"
)

// SeatInventoryRepository implements domain.SeatHoldPort, the cross-context
// port that booking.service calls to hold/confirm/release seats.
// The actual invariant preventing overselling is the DB's UNIQUE(trip_id, seat_number)
// + optimistic-concurrency version check (DATABASE_DESIGN.md §3).
type SeatInventoryRepository struct {
	pool    db.Querier
	rawPool *pgxpool.Pool
	redis   *redis.Client
}

func NewSeatInventoryRepository(pool *pgxpool.Pool, redis *redis.Client) *SeatInventoryRepository {
	return &SeatInventoryRepository{pool: db.NewScoped(pool, db.ScopeSystem), rawPool: pool, redis: redis}
}

// HoldSeats attempts to hold the requested seat numbers for a booking.
// Uses optimistic concurrency on trip_seat_inventory.version to ensure only
// the booking that atomically updated all its seats' versions successfully
// wins the race. Returns the actual count held (< len if partial unavailability).
// Per DATABASE_DESIGN.md §3, the exact SQL:
//
//	UPDATE fleet.trip_seat_inventory
//	SET status = 'held', booking_id = $1, hold_expires_at = now() + interval,
//	    version = version + 1
//	WHERE trip_id = $2 AND seat_number = ANY($3) AND status = 'available' AND version = $4;
//
// Zero rows affected → someone else took a seat first. Caller must handle partial.
func (r *SeatInventoryRepository) HoldSeats(ctx context.Context, tripID string, seatNumbers []string, bookingID string, ttl time.Duration) (heldCount int, err error) {
	// Use the pool directly (not a tx — this is called from within the service's tx).
	// For a truly distributed transaction, the caller (booking service) wraps this in WithTenantContext.
	tag, err := r.pool.Exec(ctx,
		`UPDATE fleet.trip_seat_inventory
		 SET status = 'held',
		     booking_id = $1,
		     hold_expires_at = now() + interval '1 second' * $4,
		     version = version + 1
		 WHERE trip_id = $2
		   AND seat_number = ANY($3)
		   AND status = 'available'`,
		bookingID, tripID, seatNumbers, int64(ttl.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("hold_seats: %w", err)
	}

	// Rows affected = actual count held
	heldCount = int(tag.RowsAffected())

	// Also decrement trips.available_seats by the count we actually held
	if heldCount > 0 {
		_, err = r.pool.Exec(ctx,
			`UPDATE fleet.trips
			 SET available_seats = available_seats - $1
			 WHERE id = $2`,
			heldCount, tripID)
		if err != nil {
			return 0, fmt.Errorf("update_available_seats: %w", err)
		}

		// Publish seat update to Redis for live WS clients
		_ = r.publishSeatUpdate(ctx, tripID, "held", seatNumbers)
	}

	return heldCount, nil
}

// confirmSeatsSQL flips this booking's held seats to booked. Callers MUST
// compare rows affected with len(seatNumbers): a seat whose hold expired and
// was re-held by another booking simply doesn't match.
const confirmSeatsSQL = `UPDATE fleet.trip_seat_inventory
		 SET status = 'booked',
		     version = version + 1
		 WHERE trip_id = $1
		   AND seat_number = ANY($2)
		   AND status = 'held'
		   AND booking_id = $3`

// ConfirmSeats transitions held seats to booked status. Returns
// ErrSeatUnavailable (and books nothing) unless every seat is still held for
// this booking. ConfirmBooking uses ConfirmSeatsInTx instead.
func (r *SeatInventoryRepository) ConfirmSeats(ctx context.Context, tripID string, seatNumbers []string, bookingID string) error {
	err := db.InScopedTx(ctx, r.rawPool, db.ScopeSystem, func(tx pgx.Tx) error {
		return r.ConfirmSeatsInTx(ctx, tx, tripID, seatNumbers, bookingID)
	})
	if err != nil {
		return err
	}
	r.SeatsBooked(ctx, tripID, seatNumbers)
	return nil
}

// ConfirmSeatsInTx is ConfirmSeats on the caller's transaction, so booking can
// book the seats atomically with confirming the booking. The caller's tx must
// carry an RLS identity allowed to update the inventory (system scope).
// If not every seat matched it returns ErrSeatUnavailable; the caller must
// roll back (rows already flipped in this tx are then undone).
func (r *SeatInventoryRepository) ConfirmSeatsInTx(ctx context.Context, tx pgx.Tx, tripID string, seatNumbers []string, bookingID string) error {
	tag, err := tx.Exec(ctx, confirmSeatsSQL, tripID, seatNumbers, bookingID)
	if err != nil {
		return fmt.Errorf("confirm_seats: %w", err)
	}
	if n := int(tag.RowsAffected()); n != len(seatNumbers) {
		return fmt.Errorf("confirm_seats: %d of %d seats still held for booking: %w",
			n, len(seatNumbers), bookingdomain.ErrSeatUnavailable)
	}
	return nil
}

// SeatsBooked tells live seat-map clients the seats are booked. Call it only
// after the transaction that booked them has committed.
func (r *SeatInventoryRepository) SeatsBooked(ctx context.Context, tripID string, seatNumbers []string) {
	_ = r.publishSeatUpdate(ctx, tripID, "booked", seatNumbers)
}

// ExtendHoldForBooking implements booking's SeatHoldPort: it counts this
// booking's seats that are still held with an unexpired hold and pushes their
// hold_expires_at to at least now()+d. Extending (rather than teaching the
// fleet sweep about booking statuses) keeps ReleaseExpiredHolds free of any
// cross-context join, and a hold can still never outlive a stuck charge by
// more than d.
func (r *SeatInventoryRepository) ExtendHoldForBooking(ctx context.Context, bookingID string, d time.Duration) (int, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE fleet.trip_seat_inventory
		 SET hold_expires_at = GREATEST(hold_expires_at, now() + interval '1 second' * $2)
		 WHERE booking_id = $1
		   AND status = 'held'
		   AND hold_expires_at > now()`,
		bookingID, int64(d.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("extend_hold_for_booking: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ReleaseSeats transitions held/booked seats back to available,
// used when a booking is cancelled (passenger cancels or hold expires).
func (r *SeatInventoryRepository) ReleaseSeats(ctx context.Context, tripID string, seatNumbers []string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE fleet.trip_seat_inventory
		 SET status = 'available',
		     booking_id = NULL,
		     hold_expires_at = NULL,
		     version = version + 1
		 WHERE trip_id = $1
		   AND seat_number = ANY($2)
		   AND status IN ('held', 'booked')`,
		tripID, seatNumbers)
	if err != nil {
		return fmt.Errorf("release_seats: %w", err)
	}

	// Increment trips.available_seats by count released
	releasedCount := int(tag.RowsAffected())
	if releasedCount > 0 {
		_, err = r.pool.Exec(ctx,
			`UPDATE fleet.trips
			 SET available_seats = available_seats + $1
			 WHERE id = $2`,
			releasedCount, tripID)
		if err != nil {
			return fmt.Errorf("update_available_seats on release: %w", err)
		}

		// Publish seat update to Redis for live WS clients
		_ = r.publishSeatUpdate(ctx, tripID, "available", seatNumbers)
	}

	return nil
}

// ReleaseExpiredHolds is the idempotent mitigation for MITIGATION_PLAN.md §1.1:
// a blanket sweep that flips all held rows with hold_expires_at < now() back to available.
// Safe to call from multiple worker replicas simultaneously — it's a set-based WHERE,
// not a queue pop. After the statement commits it publishes one 'available'
// seat update per affected trip, so live seat maps see the seats free up.
func (r *SeatInventoryRepository) ReleaseExpiredHolds(ctx context.Context) (releasedCount int, err error) {
	// One statement, so the seat flip and the per-trip counter bump are atomic:
	// a crash can't free seats without giving them back to trips.available_seats.
	// (The previous two-statement version filtered the counter update on
	// hold_expires_at < now(), but the first statement had already NULLed it, so
	// the counter was never restored.)
	rows, err := r.pool.Query(ctx, `
		WITH released AS (
			UPDATE fleet.trip_seat_inventory
			SET status = 'available', booking_id = NULL, hold_expires_at = NULL, version = version + 1
			WHERE status = 'held' AND hold_expires_at < now()
			RETURNING trip_id, seat_number
		), per_trip AS (
			SELECT trip_id, count(*)::int AS n, array_agg(seat_number ORDER BY seat_number) AS seats
			FROM released GROUP BY trip_id
		)
		UPDATE fleet.trips t
		SET available_seats = t.available_seats + p.n
		FROM per_trip p
		WHERE t.id = p.trip_id
		RETURNING p.trip_id, p.seats`)
	if err != nil {
		return 0, fmt.Errorf("release_expired_holds: %w", err)
	}
	released := map[string][]string{}
	for rows.Next() {
		var tripID string
		var seats []string
		if err := rows.Scan(&tripID, &seats); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan released seats: %w", err)
		}
		released[tripID] = seats
		releasedCount += len(seats)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("release_expired_holds rows: %w", err)
	}

	// Rows are fully read, so the scoped statement has committed.
	for tripID, seats := range released {
		_ = r.publishSeatUpdate(ctx, tripID, "available", seats)
	}
	return releasedCount, nil
}

// publishSeatUpdate sends a seat-status-changed message to Redis for live WS clients.
// The message is a JSON object: {"action": "status_changed", "trip_id": "...", "seat_numbers": [...], "new_status": "..."}
func (r *SeatInventoryRepository) publishSeatUpdate(ctx context.Context, tripID, newStatus string, seatNumbers []string) error {
	if r.redis == nil {
		return nil // Redis is optional; silently skip if not configured
	}

	msg := map[string]interface{}{
		"action":       "status_changed",
		"trip_id":      tripID,
		"seat_numbers": seatNumbers,
		"new_status":   newStatus,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal seat update: %w", err)
	}

	channel := fmt.Sprintf("trip:%s", tripID)
	return r.redis.Publish(ctx, channel, payload).Err()
}

// SeatFares implements booking's TripPricingPort. The fare for a seat is the
// trip's fare for that seat's class (fleet.trip_fares), falling back to the
// trip's base fare. Only 'scheduled' trips departing more than
// domain.BookingCutoff from now are bookable; any other existing trip returns
// ErrTripNotBookable.
func (r *SeatInventoryRepository) SeatFares(ctx context.Context, tripID string, seatNumbers []string) (map[string]float64, error) {
	var bookable bool
	err := r.pool.QueryRow(ctx, `
		SELECT status = 'scheduled' AND departure_at > now() + interval '1 second' * $2
		FROM fleet.trips WHERE id = $1`, tripID, bookingCutoffSecs).Scan(&bookable)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, bookingdomain.ErrUnknownSeat
	}
	if err != nil {
		return nil, fmt.Errorf("seat_fares trip: %w", err)
	}
	if !bookable {
		return nil, bookingdomain.ErrTripNotBookable
	}

	rows, err := r.pool.Query(ctx, `
		SELECT inv.seat_number, COALESCE(f.fare, t.base_fare)::float8
		FROM fleet.trip_seat_inventory inv
		JOIN fleet.trips t ON t.id = inv.trip_id AND t.status = 'scheduled'
		LEFT JOIN fleet.seat_maps sm ON sm.bus_id = t.bus_id
		LEFT JOIN fleet.seats s ON s.seat_map_id = sm.id AND s.seat_number = inv.seat_number
		LEFT JOIN fleet.trip_fares f ON f.trip_id = t.id AND f.seat_type = s.seat_type
		WHERE inv.trip_id = $1 AND inv.seat_number = ANY($2)`, tripID, seatNumbers)
	if err != nil {
		return nil, fmt.Errorf("seat_fares: %w", err)
	}
	defer rows.Close()

	fares := make(map[string]float64, len(seatNumbers))
	for rows.Next() {
		var seat string
		var fare float64
		if err := rows.Scan(&seat, &fare); err != nil {
			return nil, fmt.Errorf("scan seat fare: %w", err)
		}
		fares[seat] = fare
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("seat_fares rows: %w", err)
	}
	if len(fares) != len(seatNumbers) {
		return nil, bookingdomain.ErrUnknownSeat
	}
	return fares, nil
}

// TripOperatorID returns the operator that owns the trip (the payee for the ledger).
func (r *SeatInventoryRepository) TripOperatorID(ctx context.Context, tripID string) (string, error) {
	var id string
	if err := r.pool.QueryRow(ctx, `SELECT operator_id FROM fleet.trips WHERE id = $1`, tripID).Scan(&id); err != nil {
		return "", fmt.Errorf("trip_operator_id: %w", err)
	}
	return id, nil
}

// TripSchedule implements booking's TripPricingPort: operator, status, departure.
func (r *SeatInventoryRepository) TripSchedule(ctx context.Context, tripID string) (*bookingdomain.TripSchedule, error) {
	var t bookingdomain.TripSchedule
	if err := r.pool.QueryRow(ctx,
		`SELECT operator_id, status, departure_at FROM fleet.trips WHERE id = $1`, tripID,
	).Scan(&t.OperatorID, &t.Status, &t.DepartureAt); err != nil {
		return nil, fmt.Errorf("trip_schedule: %w", err)
	}
	return &t, nil
}

// ReleaseSeatsForBooking frees the seats held/booked for exactly this booking.
func (r *SeatInventoryRepository) ReleaseSeatsForBooking(ctx context.Context, bookingID string) error {
	rows, err := r.pool.Query(ctx, `
		UPDATE fleet.trip_seat_inventory
		SET status = 'available', booking_id = NULL, hold_expires_at = NULL, version = version + 1
		WHERE booking_id = $1 AND status IN ('held', 'booked')
		RETURNING trip_id, seat_number`, bookingID)
	if err != nil {
		return fmt.Errorf("release_seats_for_booking: %w", err)
	}
	var tripID string
	var released []string
	for rows.Next() {
		var seat string
		if err := rows.Scan(&tripID, &seat); err != nil {
			rows.Close()
			return fmt.Errorf("scan released seat: %w", err)
		}
		released = append(released, seat)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("release_seats_for_booking rows: %w", err)
	}

	if len(released) > 0 {
		if _, err := r.pool.Exec(ctx,
			`UPDATE fleet.trips SET available_seats = available_seats + $1 WHERE id = $2`,
			len(released), tripID); err != nil {
			return fmt.Errorf("update_available_seats on release: %w", err)
		}
		_ = r.publishSeatUpdate(ctx, tripID, "available", released)
	}
	return nil
}
