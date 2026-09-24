package domain

import "context"

// Notifier sends an OTP code to a phone number. The SMS vendor
// (Text.lk/Notify.lk vs Twilio) is an open decision (TECH_STACK.md §5) —
// the service layer depends on this port, not a concrete vendor client.
type Notifier interface {
	SendOTP(ctx context.Context, phone, code string) error
}
