package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/sl-bus/backend/internal/platform/testutil"
)

// The expired-hold sweep freed seats silently: the worker built the repo with
// no Redis, and ReleaseExpiredHolds never published. Live seat maps kept
// showing the seats held. It must publish one 'available' update per trip,
// in publishSeatUpdate's format, listing exactly the released seats.
func TestReleaseExpiredHoldsPublishesSeatUpdates(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewTestPool(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	repo := NewSeatInventoryRepository(pool, rdb)

	expired := time.Now().Add(-time.Minute)
	tripA := testutil.SeedTrip(t, pool, testutil.TripSeed{Seats: []testutil.SeatSeed{
		{SeatNumber: "A1", Status: "held", BookingID: "00000000-0000-0000-0000-0000000000a1", HoldExpiresAt: expired},
		{SeatNumber: "A2", Status: "held", BookingID: "00000000-0000-0000-0000-0000000000a1", HoldExpiresAt: expired},
		{SeatNumber: "A3", Status: "held", BookingID: "00000000-0000-0000-0000-0000000000a3", HoldExpiresAt: time.Now().Add(time.Hour)},
	}})
	tripB := testutil.SeedTrip(t, pool, testutil.TripSeed{Seats: []testutil.SeatSeed{
		{SeatNumber: "B1", Status: "held", BookingID: "00000000-0000-0000-0000-0000000000b1", HoldExpiresAt: expired},
	}})

	sub := rdb.Subscribe(ctx, "trip:"+tripA, "trip:"+tripB)
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(ctx); err != nil { // subscription confirmation
		t.Fatal(err)
	}

	if _, err := repo.ReleaseExpiredHolds(ctx); err != nil {
		t.Fatalf("ReleaseExpiredHolds: %v", err)
	}

	type update struct {
		Action      string   `json:"action"`
		TripID      string   `json:"trip_id"`
		SeatNumbers []string `json:"seat_numbers"`
		NewStatus   string   `json:"new_status"`
	}
	got := map[string]update{}
	ch := sub.Channel()
	timeout := time.After(3 * time.Second)
	for len(got) < 2 {
		select {
		case m := <-ch:
			var u update
			if err := json.Unmarshal([]byte(m.Payload), &u); err != nil {
				t.Fatalf("bad payload %q: %v", m.Payload, err)
			}
			if m.Channel != "trip:"+u.TripID {
				t.Errorf("update for %s published on %s", u.TripID, m.Channel)
			}
			got[u.TripID] = u
		case <-timeout:
			t.Fatalf("got %d of 2 seat updates: %+v", len(got), got)
		}
	}
	check := func(trip string, seats ...string) {
		t.Helper()
		u := got[trip]
		if u.Action != "status_changed" || u.NewStatus != "available" {
			t.Errorf("trip %s: %+v", trip, u)
		}
		if len(u.SeatNumbers) != len(seats) {
			t.Fatalf("trip %s: seats %v, want %v", trip, u.SeatNumbers, seats)
		}
		for i := range seats {
			if u.SeatNumbers[i] != seats[i] {
				t.Errorf("trip %s: seats %v, want %v", trip, u.SeatNumbers, seats)
			}
		}
	}
	check(tripA, "A1", "A2") // A3's hold hasn't expired
	check(tripB, "B1")
}
