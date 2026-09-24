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
