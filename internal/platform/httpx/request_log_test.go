package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLoggerNeverLogsQueryToken(t *testing.T) {
	const tok = "hdr.SECRET-JWT.sig" // dummy, not a real token
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := RequestID(RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello"))
	})))

	req := httptest.NewRequest(http.MethodGet, "/v1/ws/trips/abc/seats?token="+tok+"&x=1", nil)
	req.Header.Set(RequestIDHeader, "req-123")
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	if strings.Contains(out, tok) || strings.Contains(out, "SECRET-JWT") || strings.Contains(out, "token=") {
		t.Fatalf("log output leaks the token: %s", out)
	}

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, out)
	}
	if entry["path"] != "/v1/ws/trips/abc/seats" {
		t.Errorf("path = %v", entry["path"])
	}
	if entry["method"] != "GET" || entry["status"] != float64(http.StatusTeapot) ||
		entry["bytes"] != float64(5) || entry["request_id"] != "req-123" {
		t.Errorf("unexpected log entry: %v", entry)
	}
	if _, ok := entry["duration"]; !ok {
		t.Errorf("missing duration: %v", entry)
	}
}

func TestRequestLoggerDefaultsStatusTo200(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := RequestLogger(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if !strings.Contains(buf.String(), `"status":200`) {
		t.Fatalf("expected status 200 in %s", buf.String())
	}
}
