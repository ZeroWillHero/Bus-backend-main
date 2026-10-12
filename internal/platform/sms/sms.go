// Package sms sends text messages through a vendor-neutral Sender port
// (ARCHITECTURE.md §3.3 Strategy). Production uses SMSlenz
// (https://www.smslenz.lk/developers/api); local development logs messages
// instead. The vendor only delivers messages — one-time codes are
// generated, stored and checked by the identity context, never the vendor.
package sms

import (
	"context"
	"errors"
)

// Sender delivers one SMS. Implementations must not retry on their own:
// the vendor has no idempotency key, so a retry after a timeout can send
// the same code twice. Callers surface a "resend" to the user instead.
type Sender interface {
	Send(ctx context.Context, to, body string) (Receipt, error)
}

// Receipt is what the vendor reported for an accepted message. It never
// includes the message body, so it is safe to log and store.
type Receipt struct {
	ProviderRef string // SMSlenz campaign_id
	Segments    int    // billed SMS pages
}

var (
	// ErrInvalidNumber means the destination isn't an accepted mobile number.
	ErrInvalidNumber = errors.New("sms: invalid destination number")
	// ErrProviderAuth means the vendor rejected our credentials (401) or
	// suspended the account (403) — an operator problem, not a user one.
	ErrProviderAuth = errors.New("sms: provider rejected credentials")
	// ErrProviderRejected means the vendor refused this message (4xx).
	ErrProviderRejected = errors.New("sms: provider rejected message")
	// ErrProviderUnavailable means the vendor couldn't be reached or failed (5xx, timeout).
	ErrProviderUnavailable = errors.New("sms: provider unavailable")
)
