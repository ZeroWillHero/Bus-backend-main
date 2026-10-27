package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeHandler(w http.ResponseWriter, r *http.Request) {
	var v map[string]any
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		WriteDecodeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func TestMaxBodyBytes(t *testing.T) {
	h := MaxBodyBytes(64)(http.HandlerFunc(decodeHandler))
	cases := []struct {
		name string
		body string
		want int
		code string
	}{
		{"under limit", `{"a":"b"}`, http.StatusNoContent, ""},
		{"over limit", `{"a":"` + strings.Repeat("x", 100) + `"}`, http.StatusRequestEntityTooLarge, "PLATFORM_PAYLOAD_TOO_LARGE"},
		{"malformed", `{"a":`, http.StatusBadRequest, "VALIDATION_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body)))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body)
			}
			if tc.code != "" && !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("body = %s, want code %s", rec.Body, tc.code)
			}
		})
	}
}

func TestDefaultMaxBodyBytesIsOneMiB(t *testing.T) {
	if DefaultMaxBodyBytes != 1<<20 {
		t.Fatalf("DefaultMaxBodyBytes = %d", DefaultMaxBodyBytes)
	}
}
