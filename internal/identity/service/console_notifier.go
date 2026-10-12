//go:build legacy_otp

// Excluded from the build with the legacy OTP flow; it logged OTP codes in
// plain text. Dev SMS now goes through platform/sms.ConsoleSender.

package service

import (
	"context"
	"log/slog"
)

// ConsoleNotifier logs the OTP instead of sending an SMS — the dev/local
// implementation of domain.Notifier until an SMS vendor is chosen
// (TECH_STACK.md §5).
type ConsoleNotifier struct{}

func (ConsoleNotifier) SendOTP(_ context.Context, phone, code string) error {
	slog.Info("otp requested (console notifier — no SMS vendor wired yet)", "phone", phone, "code", code)
	return nil
}
