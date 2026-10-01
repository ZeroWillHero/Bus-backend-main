package apidocs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUIServesSwaggerPointingAtSpec(t *testing.T) {
	rec := httptest.NewRecorder()
	UI(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), `url: "/openapi.yaml"`) {
		t.Error("page does not load /openapi.yaml")
	}
}

func TestSpecServesEmbeddedDocument(t *testing.T) {
	rec := httptest.NewRecorder()
	Spec(rec, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "openapi: 3.0.3") {
		t.Error("response is not the OpenAPI document")
	}
}
