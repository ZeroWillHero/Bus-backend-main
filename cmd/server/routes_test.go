package main

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/api"
)

// docsOnly are served by the API but are not part of the OpenAPI contract.
var docsOnly = map[string]bool{
	"GET /docs":         true,
	"GET /openapi.yaml": true,
}

func normalize(route string) string {
	route = strings.ReplaceAll(route, "/*/", "/")
	route = strings.TrimSuffix(route, "/*")
	if len(route) > 1 {
		route = strings.TrimSuffix(route, "/")
	}
	return route
}

// TestOpenAPISpecMatchesRoutes fails when a route exists without being
// documented in api/openapi.yaml, or when the spec documents a route that
// doesn't exist, so the published docs and generated client types cannot
// silently drift from the server.
func TestOpenAPISpecMatchesRoutes(t *testing.T) {
	noop := func(http.ResponseWriter, *http.Request) {}
	r := chi.NewRouter()
	mountRoutes(r, routeDeps{Health: noop, RegisterOperator: noop, Search: noop, SeatUpdatesWS: noop})

	registered := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + normalize(route)
		if !docsOnly[key] {
			registered[key] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("openapi.yaml is not a valid OpenAPI document: %v", err)
	}

	documented := map[string]bool{}
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			documented[method+" "+normalize(path)] = true
		}
	}

	var undocumented, stale []string
	for k := range registered {
		if !documented[k] {
			undocumented = append(undocumented, k)
		}
	}
	for k := range documented {
		if !registered[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(stale)
	if len(undocumented) > 0 {
		t.Errorf("routes missing from api/openapi.yaml:\n  %s", strings.Join(undocumented, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("api/openapi.yaml documents routes the server does not register:\n  %s", strings.Join(stale, "\n  "))
	}
}
