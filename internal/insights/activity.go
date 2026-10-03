package insights

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sl-bus/backend/internal/platform/auth"
)

// Activity event types — a stable enum; the app phrases and localises them.
// Events are derived from the operational tables (not platform.outbox_events,
// which has no operator column and is deny-all under RLS), so they are
// tenant-scoped by construction and need no extra bookkeeping on write.
//
// Not yet emitted (no source data exists): payment_received, refund_issued
// (a refund is a cancelled confirmed booking, reported as booking_cancelled),
// trip_rescheduled, operator_suspended.
const (
	EventBookingConfirmed = "booking_confirmed"
	EventBookingCancelled = "booking_cancelled"
	EventTripCreated      = "trip_created"
	EventTripCancelled    = "trip_cancelled"
	EventCheckIn          = "check_in"
	EventPayoutInitiated  = "payout_initiated"
	EventPayoutExecuted   = "payout_executed"
	EventOperatorApproved = "operator_approved"
)

const (
	DefaultActivityLimit = 20
	MaxActivityLimit     = 100
)

type ActivityItem struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurredAt"`
	TripID     *string   `json:"tripId,omitempty"`
	BookingID  *string   `json:"bookingId,omitempty"`
	PayoutID   *string   `json:"payoutId,omitempty"`
	RouteName  *string   `json:"routeName,omitempty"`
	Seats      *int      `json:"seats,omitempty"`
	Amount     *string   `json:"amount,omitempty"`
	Currency   *string   `json:"currency,omitempty"`
}

// Activity returns the operator's newest events first. operator_staff get
// no amounts and no payout events; no passenger data is ever included.
func (s *Service) Activity(ctx context.Context, claims *auth.Claims, operatorID string, limit int, cursor string) ([]ActivityItem, string, error) {
	opID, err := resolveOperator(claims, operatorID)
	if err != nil {
		return nil, "", err
	}
	if limit == 0 {
		limit = DefaultActivityLimit
	}
	if limit < 1 || limit > MaxActivityLimit {
		return nil, "", invalid("limit must be between 1 and %d", MaxActivityLimit)
	}
	var cursorAt *time.Time
	var cursorID string
	if cursor != "" {
		at, id, err := decodeActivityCursor(cursor)
		if err != nil {
			return nil, "", invalid("cursor is not valid")
		}
		cursorAt, cursorID = &at, id
	}
	financial := claims.Role != auth.RoleOperatorStaff

	var items []ActivityItem
	err = s.withTx(ctx, claims, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			WITH ev AS (
			  SELECT 'booking_confirmed' AS type, b.confirmed_at AS at, 'bc:' || b.id AS id,
			         t.id AS trip_id, b.id AS booking_id, NULL::uuid AS payout_id,
			         t.route_id, b.total_amount AS amount
			  FROM booking.bookings b JOIN fleet.trips t ON t.id = b.trip_id
			  WHERE t.operator_id = $1 AND b.confirmed_at IS NOT NULL
			  UNION ALL
			  SELECT 'booking_cancelled', b.cancelled_at, 'bx:' || b.id,
			         t.id, b.id, NULL::uuid, t.route_id, b.total_amount
			  FROM booking.bookings b JOIN fleet.trips t ON t.id = b.trip_id
			  WHERE t.operator_id = $1 AND b.confirmed_at IS NOT NULL AND b.cancelled_at IS NOT NULL
			  UNION ALL
			  SELECT 'trip_created', t.created_at, 'tc:' || t.id,
			         t.id, NULL::uuid, NULL::uuid, t.route_id, NULL::numeric
			  FROM fleet.trips t WHERE t.operator_id = $1
			  UNION ALL
			  SELECT 'trip_cancelled', t.updated_at, 'tx:' || t.id,
			         t.id, NULL::uuid, NULL::uuid, t.route_id, NULL::numeric
			  FROM fleet.trips t WHERE t.operator_id = $1 AND t.status = 'cancelled'
			  UNION ALL
			  SELECT 'check_in', tk.checked_in_at, 'ci:' || tk.id,
			         t.id, b.id, NULL::uuid, t.route_id, NULL::numeric
			  FROM booking.tickets tk
			  JOIN booking.bookings b ON b.id = tk.booking_id
			  JOIN fleet.trips t ON t.id = b.trip_id
			  WHERE t.operator_id = $1 AND tk.checked_in_at IS NOT NULL
			  UNION ALL
			  SELECT 'payout_initiated', p.initiated_at, 'pi:' || p.id,
			         NULL::uuid, NULL::uuid, p.id, NULL::uuid, p.amount
			  FROM finance.payouts p WHERE p.operator_id = $1 AND p.initiated_at IS NOT NULL AND $2
			  UNION ALL
			  SELECT 'payout_executed', p.executed_at, 'pe:' || p.id,
			         NULL::uuid, NULL::uuid, p.id, NULL::uuid, p.amount
			  FROM finance.payouts p WHERE p.operator_id = $1 AND p.executed_at IS NOT NULL AND $2
			  UNION ALL
			  SELECT 'operator_approved', o.approved_at, 'oa:' || o.id,
			         NULL::uuid, NULL::uuid, NULL::uuid, NULL::uuid, NULL::numeric
			  FROM ops.operators o WHERE o.id = $1 AND o.approved_at IS NOT NULL
			)
			SELECT ev.type, ev.at, ev.id, ev.trip_id::text, ev.booking_id::text, ev.payout_id::text,
			       r.origin, r.destination,
			       CASE WHEN ev.booking_id IS NULL THEN NULL
			            ELSE (SELECT count(*)::int FROM booking.booking_seats bs WHERE bs.booking_id = ev.booking_id) END,
			       ev.amount::numeric(14,2)::text
			FROM ev LEFT JOIN fleet.routes r ON r.id = ev.route_id
			WHERE ($3::timestamptz IS NULL OR ev.at < $3 OR (ev.at = $3 AND ev.id < $4))
			ORDER BY ev.at DESC, ev.id DESC
			LIMIT $5`,
			opID, financial, cursorAt, cursorID, limit+1)
		if err != nil {
			return fmt.Errorf("activity: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var it ActivityItem
			var origin, destination *string
			if err := rows.Scan(&it.Type, &it.OccurredAt, &it.ID, &it.TripID, &it.BookingID, &it.PayoutID,
				&origin, &destination, &it.Seats, &it.Amount); err != nil {
				return err
			}
			if origin != nil && destination != nil {
				name := routeName(*origin, *destination)
				it.RouteName = &name
			}
			if it.Amount != nil && financial {
				cur := Currency
				it.Currency = &cur
			} else {
				it.Amount = nil
			}
			items = append(items, it)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, "", err
	}

	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeActivityCursor(last.OccurredAt, last.ID)
	}
	if items == nil {
		items = []ActivityItem{}
	}
	return items, next, nil
}

func encodeActivityCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeActivityCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", err
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" {
		return time.Time{}, "", fmt.Errorf("malformed cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	return t, id, err
}
