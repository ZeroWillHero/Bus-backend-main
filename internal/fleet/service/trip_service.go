package service

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/fleet/domain"
	fleetrepo "github.com/sl-bus/backend/internal/fleet/repository"
	"github.com/sl-bus/backend/internal/platform/auth"
	platformdb "github.com/sl-bus/backend/internal/platform/db"
)

// TripService backs FR-FLT-4/5/6.
type TripService struct {
	pool     *pgxpool.Pool
	approval domain.OperatorApprovalChecker
}

func NewTripService(pool *pgxpool.Pool, approval domain.OperatorApprovalChecker) *TripService {
	return &TripService{pool: pool, approval: approval}
}

// CreateTripsInput either sets DepartureAt (one-off) or Recurrence
// (FR-FLT-4's daily/weekly batch). FareOverrides maps seat_type -> fare;
// any seat_type on the bus's seat map not present there falls back to
// BaseFare.
type CreateTripsInput struct {
	RouteID       string
	BusID         string
	DepartureAt   *time.Time
	Recurrence    *domain.RecurrenceRule
	BaseFare      float64
	FareOverrides map[string]float64
}

func (s *TripService) CreateTrips(ctx context.Context, claims *auth.Claims, in CreateTripsInput) ([]*domain.Trip, error) {
	approved, err := s.approval.IsOperatorApproved(ctx, claims.OperatorID)
	if err != nil {
		return nil, err
	}
	if !approved {
		return nil, domain.ErrOperatorNotApproved
	}

	var departures []time.Time
	if in.Recurrence != nil {
		departures = domain.GenerateOccurrences(*in.Recurrence)
	} else if in.DepartureAt != nil {
		departures = []time.Time{*in.DepartureAt}
	}
	if len(departures) == 0 {
		return nil, domain.ErrInvalidSeatMap // no valid occurrence — reuse as generic "bad input" signal for now
	}

	var trips []*domain.Trip
	err = platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		busRepo := fleetrepo.NewBusRepository(tx)
		routeRepo := fleetrepo.NewRouteRepository(tx)
		seatMapRepo := fleetrepo.NewSeatMapRepository(tx)
		tripRepo := fleetrepo.NewTripRepository(tx)
		tripFareRepo := fleetrepo.NewTripFareRepository(tx)

		bus, err := busRepo.FindByID(ctx, in.BusID)
		if err != nil {
			return err
		}
		route, err := routeRepo.FindByID(ctx, in.RouteID)
		if err != nil {
			return err
		}
		if !domain.SameOperator(claims.OperatorID, bus, route) {
			return domain.ErrOperatorMismatch
		}
		seatMap, err := seatMapRepo.FindByBusID(ctx, in.BusID)
		if err != nil {
			return err
		}
		if err := domain.ValidateSeats(seatMap.Seats); err != nil {
			return err
		}

		seatTypes := distinctSeatTypes(seatMap.Seats)
		seatNumbers := make([]string, 0, len(seatMap.Seats))
		for _, seat := range seatMap.Seats {
			seatNumbers = append(seatNumbers, seat.Number)
		}

		for _, departureAt := range departures {
			trip, err := tripRepo.Create(ctx, &domain.Trip{
				OperatorID:     claims.OperatorID,
				RouteID:        in.RouteID,
				BusID:          in.BusID,
				DepartureAt:    departureAt,
				BaseFare:       in.BaseFare,
				AvailableSeats: seatMap.TotalSeats,
			})
			if err != nil {
				return err
			}

			for _, seatType := range seatTypes {
				fare := in.BaseFare
				if override, ok := in.FareOverrides[seatType]; ok {
					fare = override
				}
				if _, err := tripFareRepo.Create(ctx, trip.ID, domain.TripFare{SeatType: seatType, Fare: fare}); err != nil {
					return err
				}
			}

			if err := tripRepo.SeedSeatInventory(ctx, trip.ID, seatNumbers); err != nil {
				return err
			}

			trips = append(trips, trip)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return trips, nil
}

func distinctSeatTypes(seats []domain.Seat) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, s := range seats {
		if _, ok := seen[s.Type]; !ok {
			seen[s.Type] = struct{}{}
			out = append(out, s.Type)
		}
	}
	return out
}

func (s *TripService) Get(ctx context.Context, claims *auth.Claims, id string) (*domain.Trip, error) {
	var trip *domain.Trip
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewTripRepository(tx)
		var err error
		trip, err = repo.FindByID(ctx, id)
		return err
	})
	return trip, err
}

func (s *TripService) List(ctx context.Context, claims *auth.Claims, filter domain.TripListFilter, cursor string, limit int) ([]*domain.Trip, string, error) {
	var trips []*domain.Trip
	var nextCursor string
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewTripRepository(tx)
		var err error
		trips, nextCursor, err = repo.ListByOperator(ctx, claims.OperatorID, filter, cursor, limit)
		return err
	})
	return trips, nextCursor, err
}

// Cancel implements FR-FLT-6's cancel action — status/departure_at
// mutation only. Refund/rebooking workflows for existing bookings are
// explicitly Phase 3/4 scope (there are no bookings yet in Phase 2).
func (s *TripService) Cancel(ctx context.Context, claims *auth.Claims, id string) (*domain.Trip, error) {
	return s.transition(ctx, claims, id, domain.TripStatusCancelled)
}

func (s *TripService) transition(ctx context.Context, claims *auth.Claims, id string, to domain.TripStatus) (*domain.Trip, error) {
	var trip *domain.Trip
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewTripRepository(tx)
		current, err := repo.FindByID(ctx, id)
		if err != nil {
			return err
		}
		if !domain.CanTransitionStatus(current.Status, to) {
			return domain.ErrInvalidTripStatusTransition
		}
		trip, err = repo.UpdateStatus(ctx, id, to)
		return err
	})
	return trip, err
}

// Reschedule implements FR-FLT-6's reschedule action.
func (s *TripService) Reschedule(ctx context.Context, claims *auth.Claims, id string, departureAt time.Time) (*domain.Trip, error) {
	var trip *domain.Trip
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewTripRepository(tx)
		var err error
		trip, err = repo.Reschedule(ctx, id, departureAt)
		return err
	})
	return trip, err
}

// UpdateFares implements FR-FLT-5.
func (s *TripService) UpdateFares(ctx context.Context, claims *auth.Claims, tripID string, fares map[string]float64) ([]domain.TripFare, error) {
	var out []domain.TripFare
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewTripFareRepository(tx)
		for seatType, fare := range fares {
			updated, err := repo.Update(ctx, tripID, seatType, fare)
			if err != nil {
				return err
			}
			out = append(out, *updated)
		}
		return nil
	})
	return out, err
}
