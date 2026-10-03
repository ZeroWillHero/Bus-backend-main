package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	fleethttp "github.com/sl-bus/backend/internal/fleet/http"
	fleetservice "github.com/sl-bus/backend/internal/fleet/service"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

func TestTripSeatsIsPublicWithStatusAndFare(t *testing.T) {
	pool := testutil.NewTestPool(t)
	tripID := testutil.SeedTrip(t, pool, testutil.TripSeed{
		AvailableSeats: 1,
		Seats: []testutil.SeatSeed{
			{SeatNumber: "1A", Status: "booked"},
			{SeatNumber: "1B", Status: "held", HoldExpiresAt: farFuture},
			{SeatNumber: "2A", Status: "available", SeatType: "window"},
		},
		Fares: map[string]float64{"standard": 1000, "window": 1050},
	})

	r := chi.NewRouter()
	r.Get("/v1/trips/{tripID}/seats", fleethttp.NewTripSeatsHandler(fleetservice.NewSearchService(pool)))
	get := func(id string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/trips/"+id+"/seats", nil))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// No Authorization header: browsing passengers aren't logged in.
	code, body := get(tripID)
	if code != http.StatusOK {
		t.Fatalf("status %d, body %v", code, body)
	}
	seats := body["data"].(map[string]any)["seats"].([]any)
	if len(seats) != 3 {
		t.Fatalf("got %d seats, want 3: %v", len(seats), seats)
	}
	want := map[string]struct {
		status string
		fare   float64
	}{
		"1A": {"booked", 1000},
		"1B": {"held", 1000},
		"2A": {"available", 1050},
	}
	for _, s := range seats {
		seat := s.(map[string]any)
		w := want[seat["seatNumber"].(string)]
		if seat["status"] != w.status || seat["fare"] != w.fare {
			t.Errorf("seat %v: got status %v fare %v, want %s %v", seat["seatNumber"], seat["status"], seat["fare"], w.status, w.fare)
		}
	}

	for _, id := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		if code, _ := get(id); code != http.StatusNotFound {
			t.Errorf("trip %q: status %d, want 404", id, code)
		}
	}
}
