package repository

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/platform/sqlcgen"
)

const defaultSearchLimit = 20

type SearchRepository struct {
	q *sqlcgen.Queries
}

func NewSearchRepository(db sqlcgen.DBTX) *SearchRepository {
	return &SearchRepository{q: sqlcgen.New(db)}
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
