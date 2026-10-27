package repository

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/platform/sqlcgen"
)

const defaultSearchLimit = 20

// bookingCutoffSecs is domain.BookingCutoff as the SQL parameter every
// bookability check takes.
var bookingCutoffSecs = int32(domain.BookingCutoff / time.Second)

type SearchRepository struct {
	db sqlcgen.DBTX
	q  *sqlcgen.Queries
}

func NewSearchRepository(db sqlcgen.DBTX) *SearchRepository {
	return &SearchRepository{db: db, q: sqlcgen.New(db)}
}

func (r *SearchRepository) Search(ctx context.Context, c domain.SearchCriteria) ([]domain.TripSearchResult, string, error) {
	limit := c.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	travelDate := pgtype.Date{Time: c.TravelDate, Valid: true}
	busClass := text(c.BusClass)
	minPrice := numericPtr(c.MinPrice)
	maxPrice := numericPtr(c.MaxPrice)

	if c.SortBy == domain.SortByPrice {
		return r.searchByPrice(ctx, c, limit, travelDate, busClass, minPrice, maxPrice)
	}
	return r.searchByDeparture(ctx, c, limit, travelDate, busClass, minPrice, maxPrice)
}

func (r *SearchRepository) searchByPrice(ctx context.Context, c domain.SearchCriteria, limit int, travelDate pgtype.Date, busClass pgtype.Text, minPrice, maxPrice pgtype.Numeric) ([]domain.TripSearchResult, string, error) {
	params := sqlcgen.SearchTripsByPriceParams{
		Limit:       int32(limit),
		Origin:      c.Origin,
		Destination: c.Destination,
		TravelDate:  travelDate,
		BusClass:    busClass,
		MinPrice:    minPrice,
		MaxPrice:    maxPrice,
		// Departed trips match the date but can't be booked.
		BookingCutoffSecs: bookingCutoffSecs,
	}
	if c.Cursor != "" {
		price, id, err := decodeSearchCursor(c.Cursor)
		if err != nil {
			return nil, "", err
		}
		params.CursorPrice = numeric(price)
		cid, err := parseUUID(id)
		if err != nil {
			return nil, "", err
		}
		params.CursorID = cid
	}

	rows, err := r.q.SearchTripsByPrice(ctx, params)
	if err != nil {
		return nil, "", err
	}
	out := make([]domain.TripSearchResult, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.TripSearchResult{
			TripID:         uuidString(row.ID),
			OperatorID:     uuidString(row.OperatorID),
			OperatorName:   row.OperatorName,
			Origin:         row.Origin,
			Destination:    row.Destination,
			DepartureAt:    row.DepartureAt.Time,
			BusClass:       row.BusClass,
			Amenities:      amenitiesFromJSON(row.Amenities),
			BaseFare:       numericToFloat64(row.BaseFare),
			AvailableSeats: int(row.AvailableSeats),
		})
	}
	nextCursor := ""
	if len(out) == limit {
		last := out[len(out)-1]
		nextCursor = encodeSearchCursor(strconv.FormatFloat(last.BaseFare, 'f', -1, 64), last.TripID)
	}
	return out, nextCursor, nil
}

func (r *SearchRepository) searchByDeparture(ctx context.Context, c domain.SearchCriteria, limit int, travelDate pgtype.Date, busClass pgtype.Text, minPrice, maxPrice pgtype.Numeric) ([]domain.TripSearchResult, string, error) {
	params := sqlcgen.SearchTripsByDepartureParams{
		Limit:       int32(limit),
		Origin:      c.Origin,
		Destination: c.Destination,
		TravelDate:  travelDate,
		BusClass:    busClass,
		MinPrice:    minPrice,
		MaxPrice:    maxPrice,
		// Departed trips match the date but can't be booked.
		BookingCutoffSecs: bookingCutoffSecs,
	}
	if c.Cursor != "" {
		departureAt, id, err := decodeCursor(c.Cursor)
		if err != nil {
			return nil, "", err
		}
		params.CursorDepartureAt = pgtype.Timestamptz{Time: departureAt, Valid: true}
		cid, err := parseUUID(id)
		if err != nil {
			return nil, "", err
		}
		params.CursorID = cid
	}

	rows, err := r.q.SearchTripsByDeparture(ctx, params)
	if err != nil {
		return nil, "", err
	}
	out := make([]domain.TripSearchResult, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.TripSearchResult{
			TripID:         uuidString(row.ID),
			OperatorID:     uuidString(row.OperatorID),
			OperatorName:   row.OperatorName,
			Origin:         row.Origin,
			Destination:    row.Destination,
			DepartureAt:    row.DepartureAt.Time,
			BusClass:       row.BusClass,
			Amenities:      amenitiesFromJSON(row.Amenities),
			BaseFare:       numericToFloat64(row.BaseFare),
			AvailableSeats: int(row.AvailableSeats),
		})
	}
	nextCursor := ""
	if len(out) == limit {
		last := out[len(out)-1]
		nextCursor = encodeCursor(last.DepartureAt, last.TripID)
	}
	return out, nextCursor, nil
}

// TripSeats returns the seat map of a visible trip (scheduled, approved
// operator — the same visibility rule as search) with each seat's live
// inventory status and fare. The fare is the trip's fare for the seat's
// class, falling back to the base fare, exactly as SeatInventoryRepository.
// SeatFares prices a hold. Only seats with an inventory row are returned,
// since only those can be held. bookable is false once the trip is within
// domain.BookingCutoff of departure (the map is still shown, but holds will
// be refused). Returns domain.ErrTripNotFound when the trip doesn't exist or
// isn't visible.
func (r *SearchRepository) TripSeats(ctx context.Context, tripID string) (seats []domain.TripSeat, bookable bool, err error) {
	var visible bool
	if err := r.db.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM fleet.trips t
				JOIN ops.operators o ON o.id = t.operator_id
				WHERE t.id = $1 AND t.status = 'scheduled' AND o.status = 'approved'
			),
			EXISTS (
				SELECT 1 FROM fleet.trips t
				WHERE t.id = $1 AND t.departure_at > now() + interval '1 second' * $2
			)`, tripID, bookingCutoffSecs).Scan(&visible, &bookable); err != nil {
		return nil, false, err
	}
	if !visible {
		return nil, false, domain.ErrTripNotFound
	}

	rows, err := r.db.Query(ctx, `
		SELECT s.seat_number, s.seat_type, s.row_no, s.col_no, inv.status,
		       COALESCE(f.fare, t.base_fare)::float8
		FROM fleet.trips t
		JOIN fleet.seat_maps sm ON sm.bus_id = t.bus_id
		JOIN fleet.seats s ON s.seat_map_id = sm.id
		JOIN fleet.trip_seat_inventory inv ON inv.trip_id = t.id AND inv.seat_number = s.seat_number
		LEFT JOIN fleet.trip_fares f ON f.trip_id = t.id AND f.seat_type = s.seat_type
		WHERE t.id = $1
		ORDER BY s.row_no, s.col_no`, tripID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := []domain.TripSeat{}
	for rows.Next() {
		var s domain.TripSeat
		if err := rows.Scan(&s.SeatNumber, &s.SeatType, &s.Row, &s.Col, &s.Status, &s.Fare); err != nil {
			return nil, false, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return out, bookable, nil
}
