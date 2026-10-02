package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/insights"
	insightshttp "github.com/sl-bus/backend/internal/insights/http"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

type envelope struct {
	Success bool                  `json:"success"`
	Data    json.RawMessage       `json:"data"`
	Error   struct{ Code string } `json:"error"`
	Meta    struct {
		NextCursor string `json:"nextCursor"`
		HasMore    *bool  `json:"hasMore"`
	} `json:"meta"`
}

func TestInsightsRoutesRolesAndErrors(t *testing.T) {
	pool := testutil.NewTestPool(t)
	op := testutil.SeedOperator(t, pool)
	other := testutil.SeedOperator(t, pool)
	for i := 0; i < 3; i++ { // enough events to page through
		var route string
		if err := pool.QueryRow(context.Background(),
			`INSERT INTO fleet.routes (operator_id, origin, destination) VALUES ($1,'A','B') RETURNING id`, op).Scan(&route); err != nil {
			t.Fatal(err)
		}
		var bus string
		if err := pool.QueryRow(context.Background(),
			`INSERT INTO fleet.buses (operator_id, registration_no, bus_class) VALUES ($1,'T-'||gen_random_uuid()::text,'normal') RETURNING id`, op).Scan(&bus); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO fleet.trips (operator_id, route_id, bus_id, departure_at, base_fare, available_seats) VALUES ($1,$2,$3, now() + interval '1 day', 100, 0)`,
			op, route, bus); err != nil {
			t.Fatal(err)
		}
	}

	issuer := auth.NewJWTIssuer("insights-http-test")
	r := chi.NewRouter()
	insightshttp.Mount(r, insightshttp.Deps{Insights: insights.NewService(pool), Verifier: issuer})

	get := func(role auth.Role, operatorID, path string) (int, envelope) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if role != "" {
			tok, err := issuer.Issue("11111111-1111-1111-1111-111111111111", role, operatorID, true)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var env envelope
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		return rec.Code, env
	}

	const range1 = "?from=2026-10-01&to=2026-10-02"
	cases := []struct {
		name       string
		role       auth.Role
		operatorID string
		path       string
		status     int
		code       string
	}{
		{"no token", "", "", "/me/dashboard", 401, "AUTH_TOKEN_INVALID"},
		{"admin dashboard", auth.RoleOperatorAdmin, op, "/me/dashboard", 200, ""},
		{"staff dashboard", auth.RoleOperatorStaff, op, "/me/dashboard", 200, ""},
		{"staff activity", auth.RoleOperatorStaff, op, "/me/activity", 200, ""},
		{"by explicit own id", auth.RoleOperatorAdmin, op, "/" + op + "/dashboard", 200, ""},
		{"finance dashboard not allowed", auth.RoleFinanceAdmin, op, "/me/dashboard", 403, "AUTH_FORBIDDEN"},
		{"passenger dashboard", auth.RolePassenger, "", "/me/dashboard", 403, "AUTH_FORBIDDEN"},
		{"other operator", auth.RoleOperatorAdmin, other, "/" + op + "/dashboard", 403, "AUTH_FORBIDDEN"},
		{"staff series hidden", auth.RoleOperatorStaff, op, "/me/analytics/series" + range1, 403, "AUTH_FORBIDDEN"},
		{"staff summary hidden", auth.RoleOperatorStaff, op, "/me/analytics/summary" + range1, 403, "AUTH_FORBIDDEN"},
		{"admin series", auth.RoleOperatorAdmin, op, "/me/analytics/series" + range1, 200, ""},
		{"finance summary", auth.RoleFinanceAdmin, op, "/me/analytics/summary" + range1, 200, ""},
		{"month to date", auth.RoleOperatorAdmin, op, "/me/analytics/summary?period=month_to_date", 200, ""},
		{"range too large", auth.RoleOperatorAdmin, op, "/me/analytics/series?from=2026-01-01&to=2026-12-31", 422, "ANALYTICS_RANGE_TOO_LARGE"},
		{"bad date", auth.RoleOperatorAdmin, op, "/me/analytics/series?from=x&to=2026-12-31", 422, "VALIDATION_FAILED"},
		{"bad tz", auth.RoleOperatorAdmin, op, "/me/dashboard?tz=Nowhere/Land", 422, "VALIDATION_FAILED"},
		{"bad limit", auth.RoleOperatorAdmin, op, "/me/activity?limit=abc", 422, "VALIDATION_FAILED"},
		{"platform needs id", auth.RolePlatformAdmin, "", "/me/dashboard", 422, "VALIDATION_FAILED"},
		{"platform with id", auth.RolePlatformAdmin, "", "/" + op + "/dashboard", 200, ""},
	}
	for _, c := range cases {
		status, env := get(c.role, c.operatorID, c.path)
		if status != c.status {
			t.Errorf("%s: status = %d, want %d", c.name, status, c.status)
			continue
		}
		if c.code != "" && env.Error.Code != c.code {
			t.Errorf("%s: error code = %q, want %q", c.name, env.Error.Code, c.code)
		}
		if c.status == 200 && !env.Success {
			t.Errorf("%s: success = false on 200", c.name)
		}
	}

	// Pagination metadata uses the standard envelope.
	status, env := get(auth.RoleOperatorAdmin, op, "/me/activity?limit=2")
	if status != 200 || env.Meta.HasMore == nil || !*env.Meta.HasMore || env.Meta.NextCursor == "" {
		t.Fatalf("first page: status %d, meta %+v", status, env.Meta)
	}
	status, env2 := get(auth.RoleOperatorAdmin, op, "/me/activity?limit=2&cursor="+env.Meta.NextCursor)
	if status != 200 || string(env2.Data) == string(env.Data) {
		t.Errorf("second page: status %d, same data as first = %v", status, string(env2.Data) == string(env.Data))
	}
}
