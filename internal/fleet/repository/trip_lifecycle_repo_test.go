package repository

import (
	"context"
	"testing"

	"github.com/sl-bus/backend/internal/platform/testutil"
)

// Trips used to stay 'scheduled' forever. The sweep completes trips whose
// arrival (departure + route run time, else the fallback duration) has
// passed, and leaves future, still-running and cancelled trips alone.
func TestCompleteArrivedTrips(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewTestPool(t)
	repo := NewTripLifecycleRepository(pool)

	seed := func(departAt, status string, lastStopOffsetMin int) string {
		id := testutil.SeedTrip(t, pool, testutil.TripSeed{Seats: []testutil.SeatSeed{{SeatNumber: "1A"}}, AvailableSeats: 1})
		if _, err := pool.Exec(ctx,
			`UPDATE fleet.trips SET departure_at = now() + $2::interval, status = $3 WHERE id = $1`,
			id, departAt, status); err != nil {
			t.Fatal(err)
		}
		if lastStopOffsetMin > 0 {
			if _, err := pool.Exec(ctx, `
				INSERT INTO fleet.route_stops (route_id, stop_name, sequence, arrival_offset_min)
				SELECT route_id, 'Mid', 1, $2 / 2 FROM fleet.trips WHERE id = $1
				UNION ALL
				SELECT route_id, 'End', 2, $2 FROM fleet.trips WHERE id = $1`, id, lastStopOffsetMin); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	pastNoStops := seed("-13 hours", "scheduled", 0)    // past the 12h fallback
	arrivedByRoute := seed("-2 hours", "scheduled", 90) // 90 min run, arrived 30 min ago
	runningByRoute := seed("-1 hour", "scheduled", 180) // still on the road
	runningNoStops := seed("-2 hours", "scheduled", 0)  // fallback not yet reached
	future := seed("1 day", "scheduled", 0)
	cancelled := seed("-13 hours", "cancelled", 0)

	if _, err := repo.CompleteArrivedTrips(ctx); err != nil {
		t.Fatalf("CompleteArrivedTrips: %v", err)
	}
	want := map[string]string{
		pastNoStops:    "completed",
		arrivedByRoute: "completed",
		runningByRoute: "scheduled",
		runningNoStops: "scheduled",
		future:         "scheduled",
		cancelled:      "cancelled",
	}
	for id, w := range want {
		var got string
		if err := pool.QueryRow(ctx, `SELECT status FROM fleet.trips WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("trip %s: status %s, want %s", id, got, w)
		}
	}

	// Idempotent: a second run (e.g. another replica) changes none of ours.
	if _, err := repo.CompleteArrivedTrips(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var stillScheduled int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM fleet.trips WHERE id = ANY($1) AND status = 'scheduled'`,
		[]string{runningByRoute, runningNoStops, future}).Scan(&stillScheduled); err != nil {
		t.Fatal(err)
	}
	if stillScheduled != 3 {
		t.Errorf("after second run %d of 3 not-yet-arrived trips are scheduled", stillScheduled)
	}
}
