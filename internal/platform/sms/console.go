package sms

import (
	"context"
	"log/slog"
)

// ConsoleSender logs messages instead of sending them, for local
// development only — FromEnv refuses it outside APP_ENV=development,
// because the log line contains the one-time code.
type ConsoleSender struct{}

func (ConsoleSender) Send(_ context.Context, to, body string) (Receipt, error) {
	slog.Warn("DEV ONLY: SMS not sent (console sender)", "to", Mask(to), "body", body)
	return Receipt{ProviderRef: "console", Segments: 1}, nil
}
