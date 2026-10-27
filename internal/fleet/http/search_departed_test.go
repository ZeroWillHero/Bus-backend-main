package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	fleethttp "github.com/sl-bus/backend/internal/fleet/http"
	fleetservice "github.com/sl-bus/backend/internal/fleet/service"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

// Search used to match only the travel date, so a bus that left this morning
// was still listed (and holdable) this afternoon. Departed trips and trips
// inside the booking cutoff must be hidden; later ones stay listed. The seat
// map of a departed trip is still served, but flagged not bookable.
func TestSearchHidesDepartedTrips(t *testing.T) {
	colombo := time.FixedZone("Asia/Colombo", 5*3600+1800) // no DST in Sri Lanka
	now := time.Now().In(colombo)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, colombo)
	if now.Sub(startOfDay) < 2*time.Minute || startOfDay.Add(24*time.Hour).Sub(now) < 40*time.Minute {
		t.Skip("too close to midnight in Sri Lanka to put all trips on today's date")
	}

	pool := testutil.NewTestPool(t)
	seed := func(departAt string) string {
		id := testutil.SeedTrip(t, pool, testutil.TripSeed{
			AvailableSeats: 1,
			Seats:          []testutil.SeatSeed{{SeatNumber: "1A"}},
		})
		if _, err := pool.Exec(context.Background(),
			`UPDATE fleet.trips SET departure_at = now() + $2::interval WHERE id = $1`, id, departAt); err != nil {
			t.Fatal(err)
		}
		return id
	}
	departed := seed("-1 minute")
	insideCutoff := seed("5 minutes")
	later := seed("30 minutes")

	r := chi.NewRouter()
	svc := fleetservice.NewSearchService(pool)
	r.Get("/v1/trips/search", fleethttp.NewSearchHandler(svc))
	r.Get("/v1/trips/{tripID}/seats", fleethttp.NewTripSeatsHandler(svc))

	for _, sort := range []string{"departure", "price"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/v1/trips/search?origin=Colombo&destination=Kandy&limit=100&sort="+sort+"&date="+now.Format("2006-01-02"), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Data []struct {
				TripID string `json:"tripId"`
			} `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		got := map[string]bool{}
		for _, x := range out.Data {
			got[x.TripID] = true
		}
		if got[departed] {
			t.Errorf("sort=%s: trip that departed earlier today is listed", sort)
		}
		if got[insideCutoff] {
			t.Errorf("sort=%s: trip departing within the booking cutoff is listed", sort)
		}
		if !got[later] {
			t.Errorf("sort=%s: trip departing later today is missing", sort)
		}
	}

	bookable := func(id string) bool {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/trips/"+id+"/seats", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("seats %s: %d %s", id, rec.Code, rec.Body.String())
		}
		var out struct {
			Data struct {
				Bookable *bool `json:"bookable"`
			} `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out.Data.Bookable == nil {
			t.Fatalf("seats %s: no bookable flag: %s", id, rec.Body.String())
		}
		return *out.Data.Bookable
	}
	if bookable(departed) {
		t.Error("seat map of a departed trip claims it is bookable")
	}
	if !bookable(later) {
		t.Error("seat map of a later trip claims it is not bookable")
	}
}
