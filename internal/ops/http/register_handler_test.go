package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sl-bus/backend/internal/ops/service"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

// Every case here is rejected before the service touches the database, so
// a nil pool is safe.
func registerBody(doc string) string {
	b, _ := json.Marshal(map[string]string{
		"businessName":       "Acme Travels",
		"businessRegNo":      "PV-1",
		"contactEmail":       "ops@acme.lk",
		"password":           "a-long-enough-password",
		"registrationDocUrl": doc,
	})
	return string(b)
}

func TestRegisterHandlerRejectsBadDocURL(t *testing.T) {
	h := httpx.MaxBodyBytes(httpx.DefaultMaxBodyBytes)(NewRegisterHandler(service.NewRegistrationService(nil, auth.NoopBreachChecker{})))
	tooLong := "data:image/png;base64," + strings.Repeat("A", service.MaxRegistrationDocURLLength)

	cases := []struct {
		name string
		body string
		want int
		in   string
	}{
		{"too long", registerBody(tooLong), http.StatusUnprocessableEntity, "registrationDocUrl"},
		{"http scheme", registerBody("http://example.com/doc.png"), http.StatusUnprocessableEntity, "registrationDocUrl"},
		{"javascript scheme", registerBody("javascript:alert(1)"), http.StatusUnprocessableEntity, "registrationDocUrl"},
		{"svg data uri", registerBody("data:image/svg+xml;base64,PHN2Zz4="), http.StatusUnprocessableEntity, "registrationDocUrl"},
		{"body over 1 MiB", registerBody(strings.Repeat("A", int(httpx.DefaultMaxBodyBytes))), http.StatusRequestEntityTooLarge, "PLATFORM_PAYLOAD_TOO_LARGE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/auth/register", strings.NewReader(tc.body)))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.in) {
				t.Fatalf("body = %s, want it to mention %s", rec.Body, tc.in)
			}
		})
	}
}

func TestValidateRegistrationDocURLAccepts(t *testing.T) {
	maxData := "data:image/jpeg;base64," + strings.Repeat("A", service.MaxRegistrationDocURLLength-len("data:image/jpeg;base64,"))
	for _, doc := range []string{"", "https://docs.example.com/permit.pdf", "data:image/png;base64,iVBORw0KGgo=", "data:image/webp;base64,UklGRg==", maxData} {
		if err := service.ValidateRegistrationDocURL(doc); err != nil {
			t.Errorf("ValidateRegistrationDocURL(%.40q) = %v", doc, err)
		}
	}
	// The cap must leave room for the rest of the body under the global limit.
	if service.MaxRegistrationDocURLLength >= httpx.DefaultMaxBodyBytes {
		t.Fatal("doc cap must be below the request body limit")
	}
}
