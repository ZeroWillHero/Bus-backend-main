package sms

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeLKMobile(t *testing.T) {
	ok := map[string]string{
		"0771234567":      "+94771234567",
		"771234567":       "+94771234567",
		"94771234567":     "+94771234567",
		"+94771234567":    "+94771234567",
		"+94 77 123 4567": "+94771234567",
		"0094771234567":   "+94771234567",
		"070-123-4567":    "+94701234567",
	}
	for in, want := range ok {
		if got, err := NormalizeLKMobile(in); err != nil || got != want {
			t.Errorf("NormalizeLKMobile(%q) = %q %v, want %q", in, got, err, want)
		}
	}
	bad := []string{"", "0112345678", "0731234567", "+447700900123", "07712345678", "077123456", "077x234567", "+1 94771234567", "77+1234567"}
	for _, in := range bad {
		if got, err := NormalizeLKMobile(in); !errors.Is(err, ErrInvalidNumber) {
			t.Errorf("NormalizeLKMobile(%q) = %q %v, want ErrInvalidNumber", in, got, err)
		}
	}
}

func TestMask(t *testing.T) {
	if got := Mask("+94771234567"); got != "+94 7•• ••• 567" {
		t.Fatalf("Mask = %q", got)
	}
	if got := Mask("garbage"); got != "•••" {
		t.Fatalf("Mask(garbage) = %q", got)
	}
}

const testKey = "super-secret-api-key"

func newTestClient(t *testing.T, handler http.HandlerFunc) *SMSlenzClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewSMSlenzClient(SMSlenzConfig{BaseURL: srv.URL + "/api/", UserID: "12", APIKey: testKey, SenderID: "SMSlenzDEMO"}, srv.Client())
}

func TestSendPostsJSONBodyAndParsesReceipt(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/send-sms" {
			t.Errorf("request = %s %s, want POST /api/send-sms", r.Method, r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("credentials must not go in the query string: %q", r.URL.RawQuery)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = io.WriteString(w, `{"success":true,"message":"SMS sent successfully","data":{"status":"success","campaign_id":43,"pages":1,"sms_credit_balance":"99.41"}}`)
	})

	rec, err := c.Send(context.Background(), "+94771234567", "123456 is your SL-Bus code.")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if rec.ProviderRef != "43" || rec.Segments != 1 {
		t.Fatalf("receipt = %+v", rec)
	}
	want := map[string]any{"user_id": "12", "api_key": testKey, "sender_id": "SMSlenzDEMO", "contact": "+94771234567", "message": "123456 is your SL-Bus code."}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("body[%s] = %v, want %v", k, got[k], v)
		}
	}
}

func TestSendMapsErrorsWithoutLeakingTheKey(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   error
	}{
		{401, `{"success":false,"message":"Invalid API key"}`, ErrProviderAuth},
		{403, `{"success":false,"message":"Account suspended"}`, ErrProviderAuth},
		{422, `{"success":false,"message":"Invalid sender id"}`, ErrProviderRejected},
		{200, `{"success":false,"message":"Insufficient credit"}`, ErrProviderRejected},
		{502, `<html>bad gateway</html>`, ErrProviderUnavailable},
	}
	for _, tc := range cases {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		})
		_, err := c.Send(context.Background(), "+94771234567", "hi")
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), testKey) {
			t.Errorf("status %d: error leaks the API key: %v", tc.status, err)
		}
	}
}

func TestSendUnreachableProvider(t *testing.T) {
	c := NewSMSlenzClient(SMSlenzConfig{BaseURL: "http://127.0.0.1:1/api", UserID: "12", APIKey: testKey, SenderID: "X"}, nil)
	_, err := c.Send(context.Background(), "+94771234567", "hi")
	if !errors.Is(err, ErrProviderUnavailable) || strings.Contains(err.Error(), testKey) {
		t.Fatalf("err = %v", err)
	}
}

func TestAccountStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"success":true,"data":{"status":"active","sms_credit_balance":1250.5,"sender_id_balances":[{"sender_id":"SLBus","allocated_balance":"300","active":true}]}}`)
	})
	st, err := c.AccountStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != "active" || st.CreditBalance != 1250.5 || st.SenderBalances["SLBus"] != 300 {
		t.Fatalf("status = %+v", st)
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("SMSLENZ_USER_ID", "")
	t.Setenv("SMSLENZ_API_KEY", "")
	t.Setenv("SMSLENZ_SENDER_ID", "SMSlenzDEMO")
	t.Setenv("SMSLENZ_BASE_URL", "")

	t.Setenv("APP_ENV", "production")
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("missing credentials outside development must fail")
	}
	t.Setenv("APP_ENV", "development")
	if s, _, err := FromEnv(); err != nil || s != (ConsoleSender{}) {
		t.Fatalf("development without credentials = %v %v, want console sender", s, err)
	}

	t.Setenv("SMSLENZ_USER_ID", "12")
	t.Setenv("SMSLENZ_API_KEY", testKey)
	if _, c, err := FromEnv(); err != nil || c == nil {
		t.Fatalf("with credentials = %v %v, want SMSlenz client", c, err)
	}
	t.Setenv("SMSLENZ_BASE_URL", "http://www.smslenz.lk/api")
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("plain-http base URL must be rejected")
	}
}
