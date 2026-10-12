package sms

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// FromEnv builds the Sender from SMSLENZ_* variables. With credentials set
// it returns an SMSlenz client (HTTPS base URL required). Without them it
// returns the console sender in development and an error anywhere else, so
// a misconfigured deploy fails closed instead of "sending" codes to a log.
// The returned client is nil when the console sender is used.
func FromEnv() (Sender, *SMSlenzClient, error) {
	cfg := SMSlenzConfig{
		BaseURL:  os.Getenv("SMSLENZ_BASE_URL"),
		UserID:   strings.TrimSpace(os.Getenv("SMSLENZ_USER_ID")),
		APIKey:   strings.TrimSpace(os.Getenv("SMSLENZ_API_KEY")),
		SenderID: strings.TrimSpace(os.Getenv("SMSLENZ_SENDER_ID")),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://www.smslenz.lk/api"
	}
	if cfg.UserID == "" || cfg.APIKey == "" {
		if os.Getenv("APP_ENV") == "development" {
			return ConsoleSender{}, nil, nil
		}
		return nil, nil, errors.New("sms: SMSLENZ_USER_ID and SMSLENZ_API_KEY are required (or APP_ENV=development for the console sender)")
	}
	if !strings.HasPrefix(cfg.BaseURL, "https://") {
		return nil, nil, errors.New("sms: SMSLENZ_BASE_URL must use https")
	}
	if cfg.SenderID == "" {
		return nil, nil, errors.New("sms: SMSLENZ_SENDER_ID is required")
	}
	client := NewSMSlenzClient(cfg, nil)
	return client, client, nil
}

// BalancePolicy turns a balance into what the admin dashboard and the
// worker alert act on. Both read it from the same env vars so the
// dashboard's "low" warning and the worker's alert always agree.
type BalancePolicy struct {
	UnitPriceLKR    float64 // SMS_UNIT_PRICE_LKR, default 0.59 (SMSlenz flat rate)
	LowThresholdLKR float64 // SMS_LOW_BALANCE_ALERT_LKR, default 500
}

func BalancePolicyFromEnv() BalancePolicy {
	p := BalancePolicy{UnitPriceLKR: 0.59, LowThresholdLKR: 500}
	if v, err := strconv.ParseFloat(os.Getenv("SMS_UNIT_PRICE_LKR"), 64); err == nil && v > 0 {
		p.UnitPriceLKR = v
	}
	if v, err := strconv.ParseFloat(os.Getenv("SMS_LOW_BALANCE_ALERT_LKR"), 64); err == nil && v >= 0 {
		p.LowThresholdLKR = v
	}
	return p
}

// IsLow reports whether the balance needs attention: below the threshold,
// or the provider account isn't active (sends would fail regardless).
func (p BalancePolicy) IsLow(s BalanceSnapshot) bool {
	return s.Status != "active" || s.MainBalanceLKR < p.LowThresholdLKR
}

// EstimatedMessages is how many single-segment SMS the main balance buys.
func (p BalancePolicy) EstimatedMessages(s BalanceSnapshot) int {
	if p.UnitPriceLKR <= 0 || s.MainBalanceLKR <= 0 {
		return 0
	}
	return int(s.MainBalanceLKR / p.UnitPriceLKR)
}
