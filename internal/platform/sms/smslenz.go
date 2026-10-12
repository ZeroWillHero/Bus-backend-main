package sms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SMSlenzConfig holds the account credentials from the SMSlenz API
// Settings page. APIKey is a secret: it is only ever sent in a POST body
// over HTTPS, never in a URL (the API also accepts GET, which would leak it
// into access logs), and never included in errors or logs.
type SMSlenzConfig struct {
	BaseURL  string // https://www.smslenz.lk/api
	UserID   string
	APIKey   string
	SenderID string // must be approved by SMSlenz; SMSlenzDEMO for testing
}

// SMSlenzClient implements Sender (and balance checks) against the SMSlenz
// REST API.
type SMSlenzClient struct {
	cfg  SMSlenzConfig
	http *http.Client
}

func NewSMSlenzClient(cfg SMSlenzConfig, httpClient *http.Client) *SMSlenzClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &SMSlenzClient{cfg: cfg, http: httpClient}
}

// smslenzResponse is the common envelope. Numeric fields are decoded
// loosely because the docs show them both as numbers and strings.
type smslenzResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type looseString string

func (s *looseString) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch t := v.(type) {
	case nil:
		*s = ""
	case string:
		*s = looseString(t)
	default:
		*s = looseString(strings.TrimSpace(string(b)))
	}
	return nil
}

func (c *SMSlenzClient) Send(ctx context.Context, to, body string) (Receipt, error) {
	var data struct {
		CampaignID looseString `json:"campaign_id"`
		Pages      looseString `json:"pages"`
	}
	err := c.post(ctx, "/send-sms", map[string]any{
		"sender_id": c.cfg.SenderID,
		"contact":   to,
		"message":   body,
	}, &data)
	if err != nil {
		return Receipt{}, err
	}
	pages, _ := strconv.Atoi(string(data.Pages))
	return Receipt{ProviderRef: string(data.CampaignID), Segments: pages}, nil
}

// AccountStatus is the subset of /account-status the worker monitors.
type AccountStatus struct {
	Status         string
	CreditBalance  float64 // main balance
	SenderBalances map[string]float64
}

func (c *SMSlenzClient) AccountStatus(ctx context.Context) (AccountStatus, error) {
	var data struct {
		Status           string      `json:"status"`
		SMSCreditBalance looseString `json:"sms_credit_balance"`
		SenderIDBalances []struct {
			SenderID         string      `json:"sender_id"`
			AllocatedBalance looseString `json:"allocated_balance"`
		} `json:"sender_id_balances"`
	}
	if err := c.post(ctx, "/account-status", map[string]any{}, &data); err != nil {
		return AccountStatus{}, err
	}
	out := AccountStatus{Status: data.Status, SenderBalances: map[string]float64{}}
	out.CreditBalance, _ = strconv.ParseFloat(string(data.SMSCreditBalance), 64)
	for _, sb := range data.SenderIDBalances {
		v, _ := strconv.ParseFloat(string(sb.AllocatedBalance), 64)
		out.SenderBalances[sb.SenderID] = v
	}
	return out, nil
}

func (c *SMSlenzClient) post(ctx context.Context, path string, fields map[string]any, out any) error {
	fields["user_id"] = c.cfg.UserID
	fields["api_key"] = c.cfg.APIKey
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// Don't wrap err: *url.Error repeats the URL, and keeping the
		// message generic guarantees nothing request-specific leaks.
		return fmt.Errorf("%w: request to %s failed", ErrProviderUnavailable, path)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("%w: reading %s response", ErrProviderUnavailable, path)
	}

	var env smslenzResponse
	_ = json.Unmarshal(raw, &env)
	vendorMsg := sanitize(env.Message)

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: %s %d %s", ErrProviderAuth, path, resp.StatusCode, vendorMsg)
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: %s %d %s", ErrProviderUnavailable, path, resp.StatusCode, vendorMsg)
	case resp.StatusCode >= 400:
		return fmt.Errorf("%w: %s %d %s", ErrProviderRejected, path, resp.StatusCode, vendorMsg)
	case !env.Success:
		return fmt.Errorf("%w: %s %d %s", ErrProviderRejected, path, resp.StatusCode, vendorMsg)
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("%w: unexpected %s response shape", ErrProviderUnavailable, path)
		}
	}
	return nil
}

// sanitize bounds a vendor-supplied message before it goes into an error
// (and from there into logs).
func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return ' '
		}
		return r
	}, s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
