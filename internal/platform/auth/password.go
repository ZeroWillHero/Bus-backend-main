package auth

import (
	"context"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword hashes a plaintext password for storage in identity.users.password_hash.
func HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword reports whether plain matches the stored bcrypt hash.
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// PasswordBreachChecker checks a candidate password against a known-breach
// list (AUTHENTICATION_DESIGN.md §3.4 — the k-anonymity HIBP-style check),
// at signup and password change. The vendor/API for this is not yet chosen
// (see TECH_STACK.md), so callers depend on this port rather than a
// concrete HTTP client.
type PasswordBreachChecker interface {
	IsBreached(ctx context.Context, plain string) (bool, error)
}

// NoopBreachChecker never flags a password as breached. It's the Phase 1
// default until a real HIBP-style client is wired in; swapping it out
// later is a one-line change at the call site in cmd/server/main.go.
type NoopBreachChecker struct{}

func (NoopBreachChecker) IsBreached(context.Context, string) (bool, error) {
	return false, nil
}
