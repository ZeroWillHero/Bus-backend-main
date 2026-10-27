package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/platform/db"
)

// TripLifecycleRepository holds the worker's set-based trip status sweeps.
// It acts as the system identity, since the sweep crosses every operator.
type TripLifecycleRepository struct {
	pool db.Querier
}

func NewTripLifecycleRepository(pool *pgxpool.Pool) *TripLifecycleRepository {
	return &TripLifecycleRepository{pool: db.NewScoped(pool, db.ScopeSystem)}
}

// CompleteArrivedTrips marks scheduled trips 'completed' once they have
// arrived. Arrival is departure_at plus the route's largest stop
// arrival_offset_min (the run time to the last stop), or departure_at +
// domain.TripFallbackDuration when the route has no offsets. Cancelled and
// already-completed trips are never touched. One idempotent UPDATE, so
// concurrent worker replicas just race to a no-op.
func (r *TripLifecycleRepository) CompleteArrivedTrips(ctx context.Context) (int, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE fleet.trips t
		SET status = 'completed', updated_at = now()
		WHERE t.status = 'scheduled'
		  AND t.departure_at < now()
		  AND t.departure_at + COALESCE(
		        (SELECT make_interval(mins => max(rs.arrival_offset_min))
		         FROM fleet.route_stops rs WHERE rs.route_id = t.route_id),
		        interval '1 second' * $1
		      ) < now()`, int64(domain.TripFallbackDuration/time.Second))
	if err != nil {
		return 0, fmt.Errorf("complete_arrived_trips: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
