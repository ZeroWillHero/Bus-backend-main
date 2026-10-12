package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	fleethttp "github.com/sl-bus/backend/internal/fleet/http"
	fleetservice "github.com/sl-bus/backend/internal/fleet/service"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

// Search dates are Sri Lanka calendar days. A bus leaving at 02:00 on the 15th
// (Asia/Colombo, UTC+5:30) is 20:30 UTC on the 14th; it used to be listed under
// the 14th and missing from the 15th.
func TestSearchDateIsASriLankaDay(t *testing.T) {
	pool := testutil.NewTestPool(t)
	tripID := testutil.SeedTrip(t, pool, testutil.TripSeed{
		AvailableSeats: 1,
		Seats:          []testutil.SeatSeed{{SeatNumber: "1A"}},
	})
	if _, err := pool.Exec(context.Background(),
		`UPDATE fleet.trips SET departure_at = '2031-03-14 20:30:00+00' WHERE id = $1`, tripID); err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Get("/v1/trips/search", fleethttp.NewSearchHandler(fleetservice.NewSearchService(pool)))
	found := func(date string) bool {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/trips/search?origin=Colombo&destination=Kandy&date="+date+"&limit=100", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("search %s: %d %s", date, rec.Code, rec.Body.String())
		}
		var out struct {
			Data []struct {
				TripID string `json:"tripId"`
			} `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		for _, x := range out.Data {
			if x.TripID == tripID {
				return true
			}
		}
		return false
	}
	if !found("2031-03-15") {
		t.Error("a trip departing 02:00 on the 15th (Sri Lanka time) is missing from the 15th")
	}
	if found("2031-03-14") {
		t.Error("a trip departing 02:00 on the 15th (Sri Lanka time) is listed under the 14th")
	}
}
